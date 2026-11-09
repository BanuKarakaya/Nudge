package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"nudge/internal/coding"
	"nudge/internal/service"
	"nudge/internal/tracker"
)

type handlerTrackerStore struct {
	tracker.Store
	states map[string]tracker.State
	saved  tracker.Connection
}

func (*handlerTrackerStore) SlackTeam(_ context.Context, id int64) (string, error) {
	if id == 1 {
		return "T", nil
	}
	return "", tracker.ErrDenied
}
func (s *handlerTrackerStore) PutState(_ context.Context, p, h string, v tracker.State) error {
	s.states[p+h] = v
	return nil
}
func (s *handlerTrackerStore) ConsumeState(_ context.Context, p, h string) (tracker.State, error) {
	v, ok := s.states[p+h]
	delete(s.states, p+h)
	if !ok {
		return v, tracker.ErrConnect
	}
	return v, nil
}
func (s *handlerTrackerStore) Save(_ context.Context, c tracker.Connection) error {
	s.saved = c
	return nil
}
func (s *handlerTrackerStore) WithConnection(_ context.Context, p string, id int64, fn func(*tracker.Connection) error) error {
	if s.saved.UserID != id || s.saved.Provider != p {
		return tracker.ErrConnect
	}
	return fn(&s.saved)
}

type handlerTrackerProvider struct{ tracker.Client }

func (*handlerTrackerProvider) Exchange(context.Context, string, string) (tracker.Credentials, error) {
	return tracker.Credentials{Access: "private-access", Refresh: "private-refresh", Expires: time.Now().Add(time.Hour)}, nil
}
func (*handlerTrackerProvider) Account(context.Context, string) (tracker.Account, error) {
	return tracker.Account{ID: "a", Name: "Account"}, nil
}
func (*handlerTrackerProvider) Resources(context.Context, string) ([]tracker.Resource, error) {
	return []tracker.Resource{{ID: "selected", Name: "Selected <script>alert(1)</script>"}}, nil
}
func trackerHandlerFixture(t *testing.T, kind string) (*TrackerHandler, *handlerTrackerStore) {
	t.Helper()
	ch, _, _, _ := codingHandlerFixture(t)
	store := &handlerTrackerStore{states: map[string]tracker.State{}}
	p := &handlerTrackerProvider{Client: tracker.Client{Provider: kind, ClientID: "client", ClientSecret: "secret", RedirectURL: "https://nudgy.test/" + kind + "/oauth/callback"}}
	return NewTrackerHandler(tracker.New(store, p), ch.installations, ch.client, ch.links, "https://nudgy.test"), store
}
func TestTrackerOAuthCookieSelectionAndReplay(t *testing.T) {
	for _, kind := range []string{"jira", "trello"} {
		t.Run(kind, func(t *testing.T) {
			h, store := trackerHandlerFixture(t, kind)
			public, admin := http.NewServeMux(), http.NewServeMux()
			h.RegisterRoutes(public, admin)
			link, _ := h.links.URL(h.publicURL, 1, kind, time.Now())
			start := httptest.NewRecorder()
			public.ServeHTTP(start, httptest.NewRequest("GET", link, nil))
			if start.Code != 302 {
				t.Fatal(start.Code, start.Body.String())
			}
			redirect, _ := url.Parse(start.Header().Get("Location"))
			state := redirect.Query().Get("state")
			callback := "https://nudgy.test/" + kind + "/oauth/callback?state=" + state + "&code=code"
			bad := httptest.NewRecorder()
			public.ServeHTTP(bad, httptest.NewRequest("GET", callback, nil))
			if bad.Code != 400 {
				t.Fatal("missing cookie accepted")
			}
			r := httptest.NewRequest("GET", callback, nil)
			r.AddCookie(start.Result().Cookies()[0])
			w := httptest.NewRecorder()
			public.ServeHTTP(w, r)
			if w.Code != 200 || strings.Contains(w.Body.String(), "<script>") || strings.Contains(w.Body.String(), "private-access") {
				t.Fatal("unsafe selection page", w.Code)
			}
			if store.saved.UserID != 0 {
				t.Fatal("OAuth activated connection before selection")
			}
			var cookie *http.Cookie
			for _, c := range w.Result().Cookies() {
				if strings.Contains(c.Name, "selection") {
					cookie = c
				}
			}
			if cookie == nil || !cookie.HttpOnly || !cookie.Secure {
				t.Fatal("missing selection cookie")
			}
			form := url.Values{"state": {cookie.Value}, "resource": {"selected"}}
			post := func(origin string) *httptest.ResponseRecorder {
				r := httptest.NewRequest("POST", "https://nudgy.test/"+kind+"/select", strings.NewReader(form.Encode()))
				r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				r.Header.Set("Origin", origin)
				r.AddCookie(cookie)
				w := httptest.NewRecorder()
				public.ServeHTTP(w, r)
				return w
			}
			if post("https://evil.test").Code != 403 {
				t.Fatal("cross-site selection accepted")
			}
			if post("null").Code != 403 {
				t.Fatal("opaque origin accepted")
			}
			if got := post(h.publicURL); got.Code != 200 || store.saved.Provider != kind || store.saved.UserID != 1 {
				t.Fatal("selection not saved", got.Code)
			}
			if post(h.publicURL).Code != 400 {
				t.Fatal("selection replay accepted")
			}
			r = httptest.NewRequest("GET", callback, nil)
			r.AddCookie(start.Result().Cookies()[0])
			w = httptest.NewRecorder()
			public.ServeHTTP(w, r)
			if w.Code != 400 {
				t.Fatal("OAuth replay accepted")
			}
		})
	}
}
func TestTrackerConnectionIntentPrivateDeliveryAndToolIsolation(t *testing.T) {
	for _, kind := range []string{"jira", "trello"} {
		t.Run(kind, func(t *testing.T) {
			h, _ := trackerHandlerFixture(t, kind)
			dm := &failedConnectionDM{}
			h.client = dm
			events := &SlackEventHandler{}
			events.WithTracker(h)
			if connectionProvider(h.service.Name()+" hesabımı bağla") != kind {
				t.Fatal("missing direct route")
			}
			for _, text := range []string{h.service.Name() + " hesabımı bağlama", h.service.Name() + " hesabımı nasıl bağlarım?", `"` + kind + ` hesabımı bağla"`} {
				if connectionProvider(text) != "" {
					t.Fatal("non-consent request routed", text)
				}
			}
			id := coding.Identity{UserID: 1, Team: "T"}
			reply, ok := events.connectionReply(context.Background(), kind, id, nil)
			if !ok || !strings.Contains(reply, "gönderdim") || strings.Contains(reply, "token=") {
				t.Fatal("private link failed/leaked", reply)
			}
			dm.stage = "post"
			reply, ok = events.connectionReply(context.Background(), kind, id, nil)
			if ok || strings.Contains(reply, "gönderdim") || strings.Contains(reply, "private transport") {
				t.Fatal("delivery failure reported success", reply)
			}
			base := service.NewAgentTools(nil, nil, nil, 1, time.Now(), time.UTC)
			tools := h.Tools(base, tracker.Identity{UserID: 1, Team: "T"})
			if _, err := tools.Call(context.Background(), kind+"_connect", json.RawMessage(`{"user_id":2}`)); err == nil {
				t.Fatal("model selected identity")
			}
			if _, err := tools.Call(context.Background(), kind+"_delete", json.RawMessage(`{}`)); !errors.Is(err, tracker.ErrArguments) {
				t.Fatal("unsupported mutation")
			}
			wrong := h.Tools(base, tracker.Identity{UserID: 1, Team: "OTHER"})
			if _, err := wrong.Call(context.Background(), kind+"_connect", json.RawMessage(`{}`)); !errors.Is(err, tracker.ErrDenied) {
				t.Fatal("workspace mismatch accepted")
			}
		})
	}
}
