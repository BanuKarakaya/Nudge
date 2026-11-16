package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"nudge/internal/domain"
	"nudge/internal/repository"
	"nudge/internal/service"
)

func TestReadingAccountsAvailableToMembersAndIsolated(t *testing.T) {
	for _, kind := range []string{"feedbin", "raindrop"} {
		t.Run(kind, func(t *testing.T) {
			h, s, mux := workspaceFixture(t)
			var read, deleted int64
			h.RegisterReadingAccount(kind, kind, func(_ context.Context, id int64) (bool, error) { read = id; return true, nil }, func(_ context.Context, id int64) error { deleted = id; return nil })
			w := workspaceRequest(s, mux, 2, "GET", "/dashboard/accounts", nil)
			if w.Code != 200 || read != 2 || !strings.Contains(w.Body.String(), "/dashboard/connect/"+kind) || !strings.Contains(w.Body.String(), "/dashboard/disconnect/"+kind) {
				t.Fatal("personal account absent or wrong identity", w.Code, read)
			}
			w = workspaceRequest(s, mux, 2, "POST", "/dashboard/connect/"+kind, url.Values{"user_id": {"1"}})
			u, err := url.Parse(w.Header().Get("Location"))
			if err != nil || w.Code != 303 || u.Path != "/"+kind+"/install" || !h.links.Verify(u.Query().Get("token"), 2, kind, time.Now()) {
				t.Fatal("member could not start own personal connection", w.Code)
			}
			for _, method := range []string{"GET", "POST"} {
				w = workspaceRequest(s, mux, 1, method, "/dashboard/integrations/"+kind, nil)
				if w.Code != 403 {
					t.Fatal("personal provider entered shared scope path", w.Code)
				}
			}
			w = workspaceRequest(s, mux, 1, "GET", "/dashboard/integrations", nil)
			if strings.Contains(w.Body.String(), "/dashboard/connect/"+kind) {
				t.Fatal("personal provider shown as shared integration")
			}
			w = workspaceRequest(s, mux, 2, "POST", "/dashboard/disconnect/"+kind, url.Values{"csrf": {"bad"}})
			if w.Code != 403 || deleted != 0 {
				t.Fatal("disconnect accepted without CSRF")
			}
			w = workspaceRequest(s, mux, 2, "POST", "/dashboard/disconnect/"+kind, url.Values{"user_id": {"1"}})
			if w.Code != 303 || deleted != 2 {
				t.Fatal("disconnect used wrong identity", w.Code, deleted)
			}
		})
	}
}

type reconnectFeedbinStore struct {
	repository.FeedbinConnectionRepository
	saved domain.FeedbinConnection
}

func (s *reconnectFeedbinStore) GetByUserID(context.Context, int64) (domain.FeedbinConnection, error) {
	return domain.FeedbinConnection{Email: "OLD_PRIVATE_EMAIL", Password: "OLD_PRIVATE_PASSWORD"}, nil
}
func (s *reconnectFeedbinStore) Save(_ context.Context, c domain.FeedbinConnection) error {
	s.saved = c
	return nil
}

type reconnectFeedbinClient struct{}

func (reconnectFeedbinClient) ValidateCredentials(context.Context, string, string) error { return nil }

func TestFeedbinOwnerCanReplaceExistingConnection(t *testing.T) {
	portal, store, mux := workspaceFixture(t)
	repo := &reconnectFeedbinStore{}
	h := NewFeedbinHandler(service.NewFeedbinService(repo, reconnectFeedbinClient{}), portal.links)
	h.WithWorkspacePortal(portal)
	h.RegisterRoutes(mux)
	link, _ := portal.links.URL(portal.base, 2, "feedbin", time.Now())
	r := httptest.NewRequest("GET", link, nil)
	session := testProviderSession(store, r, 2)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `name="password"`) || strings.Contains(w.Body.String(), "OLD_PRIVATE") {
		t.Fatal("existing connection prevented renewal or leaked credentials", w.Code)
	}
	var binding *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == "nudgy_feedbin_session" {
			binding = c
		}
	}
	if binding == nil {
		t.Fatal("missing session binding")
	}
	u, _ := url.Parse(link)
	for _, id := range []string{"1", "2"} {
		form := url.Values{"user_id": {id}, "token": {u.Query().Get("token")}, "email": {"new@example.test"}, "password": {"synthetic"}}
		r = httptest.NewRequest("POST", "https://nudgy.test/feedbin/connect", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Origin", portal.base)
		r.AddCookie(session)
		r.AddCookie(binding)
		w = httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if id == "1" {
			if w.Code == 200 || repo.saved.UserID != 0 {
				t.Fatal("other account changed")
			}
		} else if w.Code != 200 || repo.saved.UserID != 2 || repo.saved.Email != "new@example.test" {
			t.Fatal("owner renewal failed", w.Code)
		}
	}
}
