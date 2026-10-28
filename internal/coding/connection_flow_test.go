package coding

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestConnectionCannotCompleteBeforeInstallationVerification(t *testing.T) {
	for _, scenario := range []string{"skip selection", "spoofed installation", "expired selection", "expired OAuth", "legacy OAuth"} {
		t.Run(scenario, func(t *testing.T) {
			s, store, _, _ := fixture(t)
			previous := store.connection
			ctx := context.Background()
			state, _, err := s.BeginConnect(ctx, 1)
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "spoofed installation":
				state, _, err = s.ContinueConnect(ctx, state, 999)
			case "expired selection":
				v := store.states[stateHash(state)]
				v.Expires = time.Now().Add(-time.Minute)
				store.states[stateHash(state)] = v
				if _, _, err := s.ContinueConnect(ctx, state, 2); !errors.Is(err, ErrConnect) {
					t.Fatalf("expired selection: %v", err)
				}
				return
			case "expired OAuth":
				state, _, err = s.ContinueConnect(ctx, state, 2)
				v := store.states[stateHash(state)]
				v.Expires = time.Now().Add(-time.Minute)
				store.states[stateHash(state)] = v
			case "legacy OAuth":
				v := store.states[stateHash(state)]
				v.Phase = "legacy"
				store.states[stateHash(state)] = v
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.CompleteConnectAccount(ctx, state, "code"); err == nil {
				t.Fatal("saved incomplete connection")
			}
			if store.connection != previous || len(store.tasks) != 0 {
				t.Fatal("failed setup replaced the connection or created a task")
			}
		})
	}
}

func TestSelectionStateIsSingleUseAndCannotBecomeOAuthState(t *testing.T) {
	s, store, _, _ := fixture(t)
	ctx := context.Background()
	selection, _, _ := s.BeginConnect(ctx, 1)
	oauth, _, err := s.ContinueConnect(ctx, selection, 2)
	if err != nil {
		t.Fatal(err)
	}
	if oauth == selection || store.states[stateHash(oauth)].InstallationID != 2 || len(store.states[stateHash(oauth)].Verifier) < 43 {
		t.Fatal("selection and PKCE state not separated")
	}
	if _, _, err := s.ContinueConnect(ctx, selection, 2); err == nil {
		t.Fatal("selection replay accepted")
	}
	if _, _, err := s.ContinueConnect(ctx, oauth, 2); err == nil {
		t.Fatal("OAuth state accepted as selection state")
	}
}

func TestSelectedInstallationRequiresUserAccountAndRepositoryAccess(t *testing.T) {
	for _, tc := range []struct {
		name                              string
		selected, owner, app              int64
		kind                              string
		empty, suspended, readOnly, allow bool
	}{
		{"selected personal repo", 2, 55, 7, "User", false, false, false, true},
		{"existing selection", 0, 55, 7, "User", false, false, false, true},
		{"selected organization", 2, 99, 7, "Organization", false, false, false, true},
		{"coworker installation", 2, 99, 7, "User", false, false, false, false},
		{"coworker fallback", 0, 99, 7, "User", false, false, false, false},
		{"unselected installation", 999, 55, 7, "User", false, false, false, false},
		{"wrong app", 2, 55, 8, "User", false, false, false, false},
		{"no repo selected", 2, 55, 7, "User", true, false, false, false},
		{"suspended", 2, 55, 7, "User", false, true, false, false},
		{"no write role", 2, 55, 7, "User", false, false, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := GitHub{AppID: 7, AppSlug: "nudgy-github", HTTPClient: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
				if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer ghu_user" {
					t.Fatal("setup performed a write or used wrong token")
				}
				switch r.URL.Path {
				case "/user/installations":
					installation := map[string]any{"id": 2, "app_id": tc.app, "account": map[string]any{"id": tc.owner, "type": tc.kind}, "permissions": map[string]string{"contents": "write", "pull_requests": "write"}}
					if tc.suspended {
						installation["suspended_at"] = "2026-01-01T00:00:00Z"
					}
					body, _ := json.Marshal(map[string]any{"installations": []any{installation}})
					return response(string(body)), nil
				case "/user/installations/2/repositories":
					if tc.empty {
						return response(`{"repositories":[]}`), nil
					}
					return response(`{"repositories":[{"id":10,"full_name":"developer/repo","default_branch":"main","permissions":{"push":true}}]}`), nil
				case "/repos/developer/repo/collaborators/developer/permission":
					role := "write"
					if tc.readOnly {
						role = "read"
					}
					return response(`{"permission":"` + role + `","user":{"id":55}}`), nil
				default:
					t.Fatalf("unexpected setup request %s", r.URL.Path)
					return nil, ErrUnavailable
				}
			})}}
			err := g.VerifyInstallation(context.Background(), "ghu_user", tc.selected, Account{ID: 55, Login: "developer"})
			if (err == nil) != tc.allow {
				t.Fatalf("allow=%t err=%v", tc.allow, err)
			}
			if !strings.Contains(g.InstallationURL("state"), "/apps/nudgy-github/installations/new?state=state") {
				t.Fatal("wrong installation link")
			}
		})
	}
}
