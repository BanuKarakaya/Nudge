package slack

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type codingTransport func(*http.Request) (*http.Response, error)

func (f codingTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestCodingMessagesKeepThreadTextAndBlocks(t *testing.T) {
	var calls int
	c := APIClient{HTTPClient: &http.Client{Transport: codingTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != "POST" || r.URL.Host != "slack.com" || r.Header.Get("Authorization") != "Bearer bot" {
			t.Fatal("incorrect Slack request")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["channel"] != "C" || len(body["blocks"].([]any)) != 1 {
			t.Fatal("missing channel/blocks")
		}
		if calls == 1 {
			if r.URL.Path != "/api/chat.postMessage" || body["thread_ts"] != "1.0001" || body["text"] != "Code ready" || body["unfurl_links"] != false {
				t.Fatal("reply lost message/thread")
			}
		} else {
			if r.URL.Path != "/api/chat.update" || body["ts"] != "2.0001" {
				t.Fatal("updated wrong message")
			}
			if _, ok := body["text"]; ok {
				t.Fatal("update overwrote fallback text")
			}
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ok":true}`)), Header: make(http.Header)}, nil
	})}}
	blocks := []map[string]any{{"type": "actions"}}
	if err := c.PostMessageInThreadWithBlocks(context.Background(), "bot", "C", "1.0001", "Code ready", blocks); err != nil {
		t.Fatal(err)
	}
	if err := c.UpdateMessageBlocks(context.Background(), "bot", "C", "2.0001", blocks); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatal("missing calls")
	}
}
