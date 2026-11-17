package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"nudge/internal/slack"
	"reflect"
	"strings"
	"testing"
)

type fallbackSender struct {
	names   []string
	removed string
}

func (s *fallbackSender) AddReaction(_ context.Context, _, _, _, name string) error {
	s.names = append(s.names, name)
	if name == processingReaction {
		return &slack.APIError{Code: "invalid_name", Status: 200}
	}
	return nil
}
func (s *fallbackSender) RemoveReaction(_ context.Context, _, _, _, name string) error {
	s.removed = name
	return nil
}
func (s *fallbackSender) PostMessageInThread(context.Context, string, string, string, string) error {
	return nil
}
func TestMissingCustomEmojiFallsBackAndCleansUp(t *testing.T) {
	s := &fallbackSender{}
	h := &SlackEventHandler{installations: conversationInstallations{}, history: make(conversationTestStore), client: s}
	h.replyWithSummary(conversationEvent("merhaba"))
	if !reflect.DeepEqual(s.names, []string{processingReaction, fallbackProcessingReaction}) || s.removed != fallbackProcessingReaction {
		t.Fatalf("wrong reaction lifecycle: %+v", s)
	}
}
func TestDirectMessagesAndBotLoops(t *testing.T) {
	e := conversationEvent("hello")
	e.Event.Type = "message"
	e.Event.ChannelType = "im"
	e.Event.Channel = "D123"
	if !conversationalEvent(e) {
		t.Fatal("DM ignored")
	}
	e.Event.BotID = "B1"
	if conversationalEvent(e) {
		t.Fatal("bot loop")
	}
	e.Event.BotID = ""
	e.Event.Subtype = "message_changed"
	if conversationalEvent(e) {
		t.Fatal("message edit processed twice")
	}
	e.Event.Subtype = ""
	e.Event.ChannelType = "channel"
	if conversationalEvent(e) {
		t.Fatal("unmentioned channel message processed")
	}
}
func TestProxyClientAddressRequiresExplicitTrust(t *testing.T) {
	r := httptest.NewRequest("GET", "http://local/slack/install", nil)
	r.RemoteAddr = "10.0.0.1:123"
	r.Header.Set("X-Forwarded-For", "1.1.1.1, 203.0.113.8, 10.0.0.2")
	if clientAddress(r, nil) != "10.0.0.1" {
		t.Fatal("spoofed header trusted")
	}
	if clientAddress(r, []netip.Prefix{netip.MustParsePrefix("10.0.0.0/24")}) != "203.0.113.8" {
		t.Fatal("wrong proxy hop")
	}
}

func TestHTTPErrorObservationsAndProxyBuckets(t *testing.T) {
	var observations []HTTPObservation
	mux := http.NewServeMux()
	mux.HandleFunc("GET /dashboard/ai", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Unavailable", http.StatusServiceUnavailable)
	})
	mux.HandleFunc("GET /slack/install", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	middleware := HTTPMiddleware(mux, HTTPOptions{TrustedProxies: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/24")}, ReportError: func(_ *http.Request, e HTTPObservation) { observations = append(observations, e) }})
	request := httptest.NewRequest("GET", "http://local/dashboard/ai?token=SECRET", nil)
	response := httptest.NewRecorder()
	middleware.ServeHTTP(response, request)
	if len(observations) != 1 || observations[0].Status != 503 || observations[0].Route != "GET /dashboard/ai" || observations[0].RequestID != response.Header().Get("X-Request-ID") {
		t.Fatal("missing error correlation", observations)
	}
	for i := 0; i < 61; i++ {
		r := httptest.NewRequest("GET", "http://local/slack/install", nil)
		r.RemoteAddr = "10.0.0.1:123"
		r.Header.Set("X-Forwarded-For", "203.0.113.1")
		w := httptest.NewRecorder()
		middleware.ServeHTTP(w, r)
		if i == 60 && w.Code != 429 {
			t.Fatal("rate limit not applied")
		}
	}
	request = httptest.NewRequest("GET", "http://local/slack/install", nil)
	request.RemoteAddr = "10.0.0.1:123"
	request.Header.Set("X-Forwarded-For", "203.0.113.2")
	response = httptest.NewRecorder()
	middleware.ServeHTTP(response, request)
	if response.Code != 204 {
		t.Fatal("one client locked out every proxy user")
	}
}
func TestSlackInstallAssetsAndManifest(t *testing.T) {
	_, members, mux := workspaceFixture(t)
	for _, path := range []string{"/dashboard/nudgy-icon.png", "/dashboard/nudgy-thinking-emoji.png"} {
		w := workspaceRequest(members, mux, 1, "GET", path, nil)
		if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "image/png") {
			t.Fatal("branding asset unavailable", path, w.Code)
		}
	}
	w := workspaceRequest(members, mux, 1, "GET", "/dashboard/slack-manifest", nil)
	if w.Code != 200 || strings.Contains(w.Body.String(), "YOUR_NUDGY_HOST") || !strings.Contains(w.Body.String(), "message.im") {
		t.Fatal("manifest not configured", w.Code)
	}
	if w = workspaceRequest(members, mux, 2, "GET", "/dashboard/slack-manifest", nil); w.Code != 403 {
		t.Fatal("member obtained manager manifest")
	}
}
