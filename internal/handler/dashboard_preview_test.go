package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"nudge/internal/connector"
	"nudge/internal/workspace"
)

// This preview is compiled only into the test binary. It never loads .env,
// opens a database, calls a provider, or adds authentication bypasses to server.
// Run with NUDGY_PREVIEW_ADDR=127.0.0.1:18891 go test ./internal/handler
// -run '^TestDashboardPreviewServer$' -timeout 0 -count=1 -v.
type previewSettingsStore struct{ *settingsPageStore }

func (s *previewSettingsStore) AuditLog(context.Context, int64) (json.RawMessage, error) {
	return json.RawMessage(`[{"id":1,"actor_id":1,"action":"policy_changed","target":"Örnek araç izni","created_at":"2026-11-16T12:00:00Z"}]`), nil
}

var previewPostForm = regexp.MustCompile(`(?s)<form\b[^>]*method="post"[^>]*>.*?</form>`)
var previewControl = regexp.MustCompile(`<(button|input|select|textarea)\b`)

const previewNotice = `<aside class="notice" role="note"><div><strong>Yerel önizleme · Örnek veriler</strong><p>Menü ve istek detaylarını gezebilirsin. Gerçek Slack hesabına bağlı değil; hesap bağlama, kaydetme ve silme kapalı.</p></div></aside>`

func dashboardPreview(t *testing.T) http.Handler {
	t.Helper()
	h, members, mux, ai, _ := aiFixture(t)
	h.WithMonitor(inspectorFixture{})
	h.store = &previewSettingsStore{&settingsPageStore{workspaceMemory: members, zone: "Europe/Istanbul", enabled: true}}
	ai.byTeam["T"] = workspace.AICredentials{AISettings: workspace.AISettings{Provider: "openrouter", Model: "deepseek/deepseek-v4.1-flash-1017", HasKey: true}}
	events := &SlackEventHandler{}
	h.WithActions(&settingsActions{}, events)
	connections := &connectorMemory{}
	h.WithConnectors(&connector.Service{Store: connections, Members: members, Remote: connections}, events)
	token := strings.Repeat("1", 64)
	members.sessions[workspaceHash(token)] = 1
	var mu sync.Mutex // The shared test stores intentionally use ordinary maps.
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.URL.Hostname()
		if host == "" {
			host = strings.Split(r.Host, ":")[0]
		}
		if host != "127.0.0.1" && host != "localhost" {
			http.Error(w, "Local preview only", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "Yerel önizlemede değişiklikler kapalı. Gerçek hesap bağlantısı kullanılmıyor.", http.StatusMethodNotAllowed)
			return
		}
		switch r.URL.Path {
		case "/", "/overview.html":
			http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
			return
		case "/inspector.html":
			http.Redirect(w, r, "/dashboard/operations?job=42", http.StatusSeeOther)
			return
		case "/accounts.html":
			http.Redirect(w, r, "/dashboard/accounts", http.StatusSeeOther)
			return
		case "/setup.html":
			http.Redirect(w, r, "/dashboard/setup", http.StatusSeeOther)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		request := r.Clone(r.Context())
		request.Header.Del("Cookie")
		request.AddCookie(&http.Cookie{Name: workspaceCookie, Value: token})
		result := httptest.NewRecorder()
		mux.ServeHTTP(result, request)
		body := result.Body.Bytes()
		if strings.HasPrefix(result.Header().Get("Content-Type"), "text/html") {
			html := strings.Replace(string(body), `<div class="workspace-content">`, `<div class="workspace-content">`+previewNotice, 1)
			html = previewPostForm.ReplaceAllStringFunc(html, func(form string) string {
				return previewControl.ReplaceAllString(form, `<$1 disabled title="Önizlemede değişiklikler kapalı"`)
			})
			body = []byte(html)
		}
		for key, values := range result.Header() {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
		w.Header().Del("Content-Length")
		w.WriteHeader(result.Code)
		if r.Method != http.MethodHead {
			_, _ = w.Write(body)
		}
	})
}

func TestDashboardPreviewNavigation(t *testing.T) {
	preview := dashboardPreview(t)
	// Cover every sidebar section, the inspector, secondary links, downloads and
	// the polling endpoint, which a plain file server cannot serve.
	for _, path := range []string{
		"/dashboard", "/dashboard/data", "/dashboard/audit", "/dashboard/preferences",
		"/dashboard/actions", "/dashboard/operations", "/dashboard/usage", "/dashboard/setup",
		"/dashboard/ai", "/dashboard/integrations", "/dashboard/accounts", "/dashboard/team",
		"/dashboard/connectors", "/dashboard/integrations/github", "/dashboard/operations?job=42",
		"/dashboard/live/data?section=overview", "/dashboard/live/data?section=operations&job=42",
		"/dashboard/data/export", "/dashboard/audit/export", "/dashboard/slack-manifest",
		"/dashboard/styles.css", "/dashboard/workspace.css", "/dashboard/monitor.js",
		"/dashboard/nudgy-icon.png", "/dashboard/nudgy-thinking-emoji.png",
	} {
		result := httptest.NewRecorder()
		preview.ServeHTTP(result, httptest.NewRequest("GET", "http://127.0.0.1:18891"+path, nil))
		if result.Code != 200 {
			t.Errorf("%s: HTTP %d %s", path, result.Code, result.Body.String())
		}
		if strings.HasPrefix(result.Header().Get("Content-Type"), "text/html") && strings.Contains(result.Body.String(), "</html>") && !strings.Contains(result.Body.String(), previewNotice) {
			t.Errorf("%s missing preview notice", path)
		}
	}
	for _, path := range []string{"/dashboard/ai", "/dashboard/data/delete", "/dashboard/connect/github"} {
		result := httptest.NewRecorder()
		preview.ServeHTTP(result, httptest.NewRequest("POST", "http://127.0.0.1:18891"+path, strings.NewReader("api_key=must-not-be-used")))
		if result.Code != http.StatusMethodNotAllowed {
			t.Fatal("preview allowed mutation", path)
		}
	}
	result := httptest.NewRecorder()
	preview.ServeHTTP(result, httptest.NewRequest("GET", "http://external.example/dashboard", nil))
	if result.Code != http.StatusForbidden {
		t.Fatal("preview accepted external Host")
	}
}

func TestDashboardPreviewServer(t *testing.T) {
	addr := os.Getenv("NUDGY_PREVIEW_ADDR")
	if addr == "" {
		t.Skip("opt-in local preview")
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil || host != "127.0.0.1" {
		t.Fatal("preview must bind to 127.0.0.1:<port>")
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	server := &http.Server{Handler: dashboardPreview(t), ReadHeaderTimeout: 5 * time.Second}
	defer server.Close()
	fmt.Printf("Local sample dashboard: http://%s/dashboard\n", listener.Addr())
	if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
		t.Fatal(err)
	}
}
