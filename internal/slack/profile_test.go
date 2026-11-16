package slack

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestDisplayNameOnlyAcceptsRequestedSlackIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
		status           int
	}{
		{"display", `{"ok":true,"user":{"id":"U1","profile":{"display_name":" Banu ","real_name":"Other","email":"PRIVATE"}}}`, "Banu", 200},
		{"fallback", `{"ok":true,"user":{"id":"U1","profile":{"real_name":"Banu"}}}`, "Banu", 200},
		{"wrong_identity", `{"ok":true,"user":{"id":"U2","profile":{"display_name":"PRIVATE"}}}`, "", 200},
		{"provider_error", `{"ok":false,"error":"PRIVATE"}`, "", 200},
		{"rate_limit", `PRIVATE`, "", 429},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := APIClient{HTTPClient: &http.Client{Transport: codingTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host != "slack.com" || r.URL.Path != "/api/users.info" || r.URL.Query().Get("user") != "U1" || r.Header.Get("Authorization") != "Bearer test-token" {
					t.Fatal("invalid profile request")
				}
				return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body)), Header: make(http.Header)}, nil
			})}}
			name, err := client.UserDisplayName(context.Background(), "test-token", "U1")
			if name != tc.want || (err != nil) != (tc.want == "") {
				t.Fatalf("name=%q err=%v", name, err)
			}
			if err != nil && strings.Contains(err.Error(), "PRIVATE") {
				t.Fatal("provider error leaked")
			}
		})
	}
}
