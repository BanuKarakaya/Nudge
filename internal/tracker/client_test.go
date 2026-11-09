package tracker

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(status int, raw string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(raw)), Header: http.Header{}}
}
func testClient(kind string, f roundTrip) Client {
	return Client{Provider: kind, ClientID: "client", ClientSecret: "secret", RedirectURL: "https://nudgy.test/" + kind + "/oauth/callback", HTTPClient: &http.Client{Transport: f}}
}
func TestOAuthParametersAndTokenExchange(t *testing.T) {
	for _, kind := range []string{"jira", "trello"} {
		t.Run(kind, func(t *testing.T) {
			c := testClient(kind, func(r *http.Request) (*http.Response, error) {
				if r.URL.String() != "https://auth.atlassian.com/oauth/token" || r.Method != "POST" || r.Header.Get("Authorization") != "" {
					t.Fatal("token request destination")
				}
				var body map[string]string
				if json.NewDecoder(r.Body).Decode(&body) != nil || body["client_secret"] != "secret" || body["code"] != "code" {
					t.Fatal("bad token request")
				}
				if (body["code_verifier"] == "verifier") != (kind == "trello") {
					t.Fatal("PKCE exchange mismatch")
				}
				return response(200, `{"access_token":"access","refresh_token":"refresh","expires_in":3600}`), nil
			})
			if c.Validate() != nil {
				t.Fatal("valid config rejected")
			}
			u, _ := url.Parse(c.AuthorizeURL("state", "verifier"))
			q := u.Query()
			if q.Get("state") != "state" || q.Get("redirect_uri") != c.RedirectURL || !strings.Contains(q.Get("scope"), "offline_access") {
				t.Fatal(q)
			}
			if kind == "jira" {
				if q.Get("audience") != "api.atlassian.com" {
					t.Fatal(q)
				}
			} else {
				hash := sha256.Sum256([]byte("verifier"))
				if q.Get("code_challenge") != base64.RawURLEncoding.EncodeToString(hash[:]) {
					t.Fatal("PKCE challenge")
				}
			}
			if _, err := c.Exchange(context.Background(), "code", "verifier"); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestAPIErrorsNoCredentialLeaksAndNoRedirects(t *testing.T) {
	for _, status := range []int{302, 400, 401, 403, 404, 429, 500} {
		calls := 0
		c := testClient("jira", func(r *http.Request) (*http.Response, error) {
			calls++
			if r.Header.Get("Authorization") != "Bearer secret-token" {
				t.Fatal("missing bearer")
			}
			resp := response(status, `{"error":"private-token"}`)
			resp.Header.Set("Location", "https://evil.test/")
			return resp, nil
		})
		_, err := c.Account(context.Background(), "secret-token")
		if err == nil || strings.Contains(err.Error(), "private-token") || calls != 1 {
			t.Fatal(status, err, calls)
		}
		if err := c.request(context.Background(), "https://evil.test/", "secret-token", nil, &struct{}{}); !errors.Is(err, ErrArguments) {
			t.Fatal("external API destination accepted")
		}
	}
}
func TestJiraResourcesPaginationAndBoundedIssueReads(t *testing.T) {
	ctx := context.Background()
	calls := []string{}
	c := testClient("jira", func(r *http.Request) (*http.Response, error) {
		calls = append(calls, r.URL.Path)
		switch r.URL.Path {
		case "/oauth/token/accessible-resources":
			return response(200, `[{"id":"cloud","name":"Site","url":"https://site.atlassian.net","scopes":["read:jira-work"]}]`), nil
		case "/ex/jira/cloud/rest/api/3/project/search":
			if r.URL.Query().Get("startAt") == "0" {
				return response(200, `{"values":[{"id":"10","name":"One","key":"ONE"}],"total":2,"isLast":false}`), nil
			}
			return response(200, `{"values":[{"id":"20","name":"Two","key":"TWO"}],"total":2,"isLast":true}`), nil
		case "/ex/jira/cloud/rest/api/3/search/jql":
			if r.URL.Query().Get("jql") != "project = 10 ORDER BY created DESC, key DESC" || r.URL.Query().Get("nextPageToken") != "opaque-token" {
				t.Fatal("scope or cursor lost", r.URL)
			}
			return response(200, `{"issues":[{"id":"100","key":"ONE-1","fields":{"summary":"Issue","project":{"id":"10"},"status":{"name":"Open"}}}],"nextPageToken":"next","isLast":false}`), nil
		case "/ex/jira/cloud/rest/api/3/issue/TWO-1":
			return response(200, `{"id":"200","key":"TWO-1","fields":{"project":{"id":"20"}}}`), nil
		}
		t.Fatal("unexpected request", r.URL)
		return nil, nil
	})
	resources, err := c.Resources(ctx, "token")
	if err != nil || len(resources) != 2 || resources[0].ID != "cloud:10" {
		t.Fatal(resources, err)
	}
	page, err := c.Items(ctx, "token", resources[0], "opaque-token")
	if err != nil || page.NextCursor != "next" || page.Items[0].URL != "https://site.atlassian.net/browse/ONE-1" {
		t.Fatal(page, err)
	}
	if _, err = c.Item(ctx, "token", resources[0], "TWO-1"); !errors.Is(err, ErrDenied) {
		t.Fatal("issue outside selected project", err)
	}
	before := len(calls)
	if _, err = c.Item(ctx, "token", resources[0], "../secret"); !errors.Is(err, ErrArguments) || len(calls) != before {
		t.Fatal("path injection")
	}
}
func TestTrelloCardsPaginationAndBoardBoundary(t *testing.T) {
	board := "aaaaaaaaaaaaaaaaaaaaaaaa"
	other := "bbbbbbbbbbbbbbbbbbbbbbbb"
	cursor := "cccccccccccccccccccccccc"
	c := testClient("trello", func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "/boards/") {
			if r.URL.Query().Get("sort") != "-id" || r.URL.Query().Get("before") != cursor || r.URL.Query().Get("limit") != "50" {
				t.Fatal("missing stable pagination")
			}
			cards := []map[string]any{}
			for i := 50; i > 0; i-- {
				cards = append(cards, map[string]any{"id": fmt.Sprintf("%024x", i), "idBoard": board, "name": "Card", "due": nil})
			}
			raw, _ := json.Marshal(cards)
			return response(200, string(raw)), nil
		}
		return response(200, fmt.Sprintf(`{"id":%q,"idBoard":%q}`, cursor, other)), nil
	})
	r := Resource{ID: board}
	page, err := c.Items(context.Background(), "token", r, cursor)
	if err != nil || len(page.Items) != 50 || page.NextCursor != fmt.Sprintf("%024x", 1) {
		t.Fatal(page, err)
	}
	if _, err = c.Item(context.Background(), "token", r, cursor); !errors.Is(err, ErrDenied) {
		t.Fatal("card outside selected board", err)
	}
}
