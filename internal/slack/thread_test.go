package slack

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

type threadTransport func(*http.Request) (*http.Response, error)

func (f threadTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func threadResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}

func TestThreadHistoryPaginatesAndScopesMessages(t *testing.T) {
	calls := 0
	client := APIClient{HTTPClient: &http.Client{Transport: threadTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		q := r.URL.Query()
		if r.Method != "GET" || r.URL.Path != "/api/conversations.replies" || r.Header.Get("Authorization") != "Bearer secret" ||
			q.Get("channel") != "C1" || q.Get("ts") != "1.0" || q.Get("latest") != "4.0" || q.Get("inclusive") != "false" || q.Get("limit") != "100" {
			t.Fatal("wrong request scope")
		}
		if calls == 1 {
			if q.Get("cursor") != "" {
				t.Fatal("unexpected initial cursor")
			}
			return threadResponse(200, `{"ok":true,"messages":[{"ts":"1.0","user":"U1","text":"root"},{"ts":"2.0","thread_ts":"1.0","user":"U2","text":"code"}],"response_metadata":{"next_cursor":"next page"}}`), nil
		}
		if q.Get("cursor") != "next page" {
			t.Fatal("missing cursor")
		}
		return threadResponse(200, `{"ok":true,"messages":[{"ts":"2.0","thread_ts":"1.0","text":"duplicate"},{"ts":"3.0","thread_ts":"1.0","bot_id":"B1","text":"reply"},{"ts":"3.5","thread_ts":"other","text":"wrong thread"},{"ts":"4.0","thread_ts":"1.0","text":"current"},{"ts":"5.0","thread_ts":"1.0","text":"future"}]}`), nil
	})}}
	history, err := client.ThreadHistory(context.Background(), "secret", "C1", "1.0", "4.0")
	if err != nil || calls != 2 || history.Truncated || len(history.Messages) != 3 || history.Messages[1].User != "U2" || history.Messages[2].BotID != "B1" {
		t.Fatalf("history=%+v calls=%d err=%v", history, calls, err)
	}
}

func TestThreadHistoryKeepsRootAndNewestReplies(t *testing.T) {
	calls := 0
	client := APIClient{HTTPClient: &http.Client{Transport: threadTransport(func(*http.Request) (*http.Response, error) {
		calls++
		var messages []ThreadMessage
		for i := (calls-1)*100 + 1; i <= calls*100; i++ {
			messages = append(messages, ThreadMessage{TS: fmt.Sprintf("%d.000000", i), ThreadTS: "1.000000", User: "U", Text: "text"})
		}
		cursor := ""
		if calls == 1 {
			cursor = "next"
		}
		body, _ := json.Marshal(map[string]any{"ok": true, "messages": messages, "response_metadata": map[string]string{"next_cursor": cursor}})
		return threadResponse(200, string(body)), nil
	})}}
	history, err := client.ThreadHistory(context.Background(), "token", "C", "1.000000", "201.000000")
	if err != nil || !history.Truncated || len(history.Messages) != 100 || history.Messages[0].TS != "1.000000" || history.Messages[1].TS != "102.000000" || history.Messages[99].TS != "200.000000" {
		t.Fatalf("history=%+v err=%v", history, err)
	}
}

func TestThreadHistoryFailuresPreservePartialStatus(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"permission", 200, `{"ok":false,"error":"missing_scope"}`},
		{"rate limit", 429, `not json`},
		{"malformed", 200, `{`},
		{"missing cursor", 200, `{"ok":true,"has_more":true}`},
		{"repeated cursor", 200, `{"ok":true,"response_metadata":{"next_cursor":"next"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			client := APIClient{HTTPClient: &http.Client{Transport: threadTransport(func(*http.Request) (*http.Response, error) {
				calls++
				if calls == 1 {
					return threadResponse(200, `{"ok":true,"messages":[{"ts":"1.0","text":"root"}],"response_metadata":{"next_cursor":"next"}}`), nil
				}
				return threadResponse(tc.status, tc.body), nil
			})}}
			history, err := client.ThreadHistory(context.Background(), "token", "C", "1.0", "3.0")
			if err == nil || !history.Truncated || len(history.Messages) != 1 || calls != 2 {
				t.Fatalf("history=%+v calls=%d err=%v", history, calls, err)
			}
		})
	}
}

func TestInstallURLRequestsThreadHistoryScopes(t *testing.T) {
	u, err := url.Parse((OAuthClient{ClientID: "client"}).InstallURL("state"))
	if err != nil {
		t.Fatal(err)
	}
	scopes := "," + u.Query().Get("scope") + ","
	for _, scope := range []string{"app_mentions:read", "channels:history", "groups:history", "im:history", "mpim:history", "chat:write", "im:write"} {
		if !strings.Contains(scopes, ","+scope+",") {
			t.Fatalf("missing %s", scope)
		}
	}
}

func TestThreadHistoryStopsAtPageBudgetAndCancellation(t *testing.T) {
	calls := 0
	client := APIClient{HTTPClient: &http.Client{Transport: threadTransport(func(r *http.Request) (*http.Response, error) {
		if err := r.Context().Err(); err != nil {
			return nil, err
		}
		calls++
		return threadResponse(200, fmt.Sprintf(`{"ok":true,"response_metadata":{"next_cursor":"page-%d"}}`, calls)), nil
	})}}
	history, err := client.ThreadHistory(context.Background(), "token", "C", "1.0", "3.0")
	if err == nil || !history.Truncated || calls != 20 {
		t.Fatalf("unbounded pagination: calls=%d err=%v", calls, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	history, err = client.ThreadHistory(ctx, "token", "C", "1.0", "3.0")
	if err == nil || !history.Truncated || calls != 20 {
		t.Fatalf("cancellation ignored: calls=%d err=%v", calls, err)
	}
}
