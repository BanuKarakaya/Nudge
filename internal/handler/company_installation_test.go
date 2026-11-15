package handler

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"nudge/internal/repository/postgres"
	"nudge/internal/secure"
	"nudge/internal/slack"
)

// Exercises real HTTP cookies and the production repositories together. Only
// Slack's remote code exchange is synthetic; no real account is contacted.
func TestCompanyInstallationBrowserAndPostgres(t *testing.T) {
	dsn := os.Getenv("NUDGE_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires a disposable PostgreSQL database")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := fmt.Sprintf("company_%d", time.Now().UnixNano())
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = admin.Exec(ctx, "DROP SCHEMA "+quoted+" CASCADE") }()
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	db, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := postgres.NewWorkspaceStore(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	key := base64.StdEncoding.EncodeToString(make([]byte, 32))
	cipher, err := secure.NewCipherFromBase64(key)
	if err != nil {
		t.Fatal(err)
	}
	links, err := secure.NewLinkSigner(key)
	if err != nil {
		t.Fatal(err)
	}
	installs := postgres.NewInstallationRepository(db, cipher)
	portal := NewWorkspaceHandler(store, links, "", installs, nil)
	mux := http.NewServeMux()
	server := httptest.NewServer(HTTPMiddleware(mux))
	defer server.Close()
	portal.base = server.URL
	if err = portal.Validate(); err != nil {
		t.Fatal(err)
	}
	portal.RegisterRoutes(mux)
	oauth := slack.OAuthClient{ClientID: "synthetic", ClientSecret: "synthetic", RedirectURL: server.URL + "/slack/oauth/callback", HTTPClient: &http.Client{Transport: onboardingTransport(func(r *http.Request) (*http.Response, error) {
		if err := r.ParseForm(); err != nil {
			return nil, err
		}
		team := r.PostForm.Get("code")
		if team != "COMPANY_A" && team != "COMPANY_B" {
			return nil, fmt.Errorf("unexpected exchange")
		}
		body := fmt.Sprintf(`{"ok":true,"access_token":"synthetic-%s","team":{"id":"%s","name":"%s"},"authed_user":{"id":"OWNER"}}`, team, team, team)
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})}}
	NewSlackHandler(oauth, installs).WithWorkspacePortal(portal).RegisterRoutes(mux)
	request := func(client *http.Client, path string) (int, string, http.Header) {
		t.Helper()
		r, e := client.Get(server.URL + path)
		if e != nil {
			t.Fatal(e)
		}
		defer r.Body.Close()
		b, e := io.ReadAll(r.Body)
		if e != nil {
			t.Fatal(e)
		}
		return r.StatusCode, string(b), r.Header
	}
	clients := map[string]*http.Client{}
	for _, team := range []string{"COMPANY_A", "COMPANY_B"} {
		jar, _ := cookiejar.New(nil)
		client := &http.Client{Jar: jar, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		clients[team] = client
		status, _, header := request(client, "/slack/install")
		if status != 302 {
			t.Fatalf("install status %d", status)
		}
		u, err := url.Parse(header.Get("Location"))
		if err != nil {
			t.Fatal(err)
		}
		callback := "/slack/oauth/callback?" + url.Values{"state": {u.Query().Get("state")}, "code": {team}}.Encode()
		status, _, _ = request(&http.Client{Timeout: 10 * time.Second}, callback)
		if status != 400 {
			t.Fatalf("unbound browser accepted: %d", status)
		}
		status, _, header = request(client, callback)
		if status != 303 || header.Get("Location") != "/dashboard" {
			t.Fatalf("callback: %d", status)
		}
		status, body, _ := request(client, "/dashboard/setup")
		if status != 200 || !strings.Contains(body, team) {
			t.Fatalf("missing workspace panel for %s: %d", team, status)
		}
		status, _, _ = request(client, callback)
		if status != 400 {
			t.Fatalf("OAuth replay accepted: %d", status)
		}
		var saved string
		if err := db.QueryRow(ctx, "SELECT bot_token FROM slack_installations WHERE team_id=$1", team).Scan(&saved); err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(saved, "v2:") || strings.Contains(saved, "synthetic-") {
			t.Fatal("plaintext bot credential stored")
		}
		user, err := installs.GetUserIDBySlackUserID(ctx, team, "OWNER")
		if err != nil {
			t.Fatal(err)
		}
		member, err := store.Member(ctx, user)
		if err != nil || !member.Manager || member.Team != team {
			t.Fatal("installer role/tenant lost")
		}
	}
	// A caller-supplied tenant does not move an authenticated browser's scope.
	status, body, _ := request(clients["COMPANY_A"], "/dashboard/setup?team_id=COMPANY_B&user_id=999")
	if status != 200 || !strings.Contains(body, "COMPANY_A") || strings.Contains(body, "COMPANY_B") {
		t.Fatal("tenant identity was taken from query parameters")
	}
	// Revocation closes company A's sessions without breaking company B.
	if err := installs.Deactivate(ctx, "COMPANY_A", time.Now().Add(time.Minute).Unix()); err != nil {
		t.Fatal(err)
	}
	_, body, _ = request(clients["COMPANY_A"], "/dashboard/setup")
	if strings.Contains(body, "COMPANY_A") {
		t.Fatal("revoked installation retained panel access")
	}
	status, body, _ = request(clients["COMPANY_B"], "/dashboard/setup")
	if status != 200 || !strings.Contains(body, "COMPANY_B") {
		t.Fatal("revocation crossed workspace boundary")
	}
}
