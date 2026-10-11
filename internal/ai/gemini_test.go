package ai

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type testTransport func(*http.Request) (*http.Response, error)

func (f testTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestGenerateRetriesTruncationWithoutReturningPartialText(t *testing.T) {
	for _, finishes := range [][]string{{"MAX_TOKENS", "STOP"}, {"MAX_TOKENS", "MAX_TOKENS"}} {
		calls := 0
		client := GeminiClient{APIKey: "test", HTTPClient: &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
			finish := finishes[calls]
			calls++
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"candidates":[{"finishReason":"` + finish + `","content":{"parts":[{"text":"Complete."}]}}]}`)), Header: make(http.Header)}, nil
		})}}
		output, err := client.generate(context.Background(), "test", 100)
		if calls != 2 {
			t.Fatalf("expected one retry, got %d calls", calls)
		}
		if finishes[1] == "STOP" && (err != nil || output != "Complete.") {
			t.Fatalf("completed response rejected: %q %v", output, err)
		}
		if finishes[1] != "STOP" && (err == nil || output != "") {
			t.Fatalf("partial response returned: %q %v", output, err)
		}
	}
}
