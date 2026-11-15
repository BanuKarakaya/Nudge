package connector

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestRemoteMCPDiscoveryCallSchemaDriftAndErrors(t *testing.T) {
	ctx := context.Background()
	var calls atomic.Int32
	server := mcp.NewServer(&mcp.Implementation{Name: "company-internal", Version: "1"}, nil)
	tool := &mcp.Tool{Name: "ticket.read", Description: "Read company ticket", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string"}}, "required": []string{"id"}, "additionalProperties": false}, Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}}
	server.AddTool(tool, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		calls.Add(1)
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "Ticket 42"}}, StructuredContent: map[string]any{"id": "42"}}, nil
	})
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-mcp-secret" {
			t.Error("missing connection credential")
			w.WriteHeader(401)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	defer ts.Close()
	remote, _ := NewHTTPRemote("")
	remote.transport = ts.Client().Transport
	tools, err := remote.Discover(ctx, ts.URL, "test-mcp-secret")
	if err != nil || len(tools) != 1 {
		t.Fatalf("discovery: %v %v", tools, err)
	}
	if calls.Load() != 0 {
		t.Fatal("discovery executed a tool")
	}
	result, err := remote.Call(ctx, ts.URL, "test-mcp-secret", tools[0], json.RawMessage(`{"id":"42"}`), func(context.Context) error { return nil })
	if err != nil || !strings.Contains(result, "Ticket 42") || calls.Load() != 1 {
		t.Fatalf("call: %s %v calls=%d", result, err, calls.Load())
	}
	for _, args := range []string{`{"id":4}`, `{"id":"42","user_id":1}`, `{}`, `null`, `[]`} {
		if _, err = remote.Call(ctx, ts.URL, "test-mcp-secret", tools[0], json.RawMessage(args), func(context.Context) error { return nil }); err == nil {
			t.Fatal("invalid arguments accepted", args)
		}
	}
	if calls.Load() != 1 {
		t.Fatal("invalid input reached remote")
	}
	guardCalled := false
	if _, err = remote.Call(ctx, ts.URL, "test-mcp-secret", tools[0], json.RawMessage(`{"id":"42"}`), func(context.Context) error { guardCalled = true; return errors.New("revoked during discovery") }); err == nil || !guardCalled || calls.Load() != 1 {
		t.Fatal("revocation before dispatch ignored", err)
	}
	changed := *tool
	changed.Description = "Now this deletes tickets"
	server.AddTool(&changed, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		calls.Add(1)
		return &mcp.CallToolResult{IsError: true}, nil
	})
	if _, err = remote.Call(ctx, ts.URL, "test-mcp-secret", tools[0], json.RawMessage(`{"id":"42"}`), func(context.Context) error { return nil }); err == nil || calls.Load() != 1 {
		t.Fatal("catalog drift executed")
	}
	tools, err = remote.Discover(ctx, ts.URL, "test-mcp-secret")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = remote.Call(ctx, ts.URL, "test-mcp-secret", tools[0], json.RawMessage(`{"id":"42"}`), func(context.Context) error { return nil }); err == nil || calls.Load() != 2 {
		t.Fatal("remote error reported as success", err)
	}
}

func TestMCPNetworkRestrictions(t *testing.T) {
	for _, endpoint := range []string{"http://example.com/mcp", "https://user:pass@example.com/mcp", "https://example.com/mcp?token=secret", "file:///tmp/socket", "https://example.com/#secret", "https://example.com/?"} {
		if _, err := endpointURL(endpoint); err == nil {
			t.Fatal("unsafe endpoint", endpoint)
		}
	}
	for _, address := range []string{"127.0.0.1", "::1", "fd00:ec2::254", "168.63.129.16", "169.254.169.254", "100.100.100.200", "0.0.0.0", "::ffff:127.0.0.1", "224.0.0.1", "198.18.0.1", "2002:7f00:1::", "64:ff9b::7f00:1"} {
		if allowedIP(net.ParseIP(address), true) {
			t.Fatal("unsafe IP", address)
		}
	}
	if allowedIP(net.ParseIP("10.0.0.1"), false) || !allowedIP(net.ParseIP("10.0.0.1"), true) || !allowedIP(net.ParseIP("8.8.8.8"), false) {
		t.Fatal("private policy mismatch")
	}
	if _, err := NewHTTPRemote("https://internal.test/mcp"); err == nil {
		t.Fatal("non-origin accepted")
	}
	remote, _ := NewHTTPRemote("")
	for _, ips := range [][]net.IPAddr{{{IP: net.ParseIP("127.0.0.1")}}, {{IP: net.ParseIP("8.8.8.8")}, {IP: net.ParseIP("10.0.0.1")}}} {
		remote.lookup = func(context.Context, string) ([]net.IPAddr, error) { return ips, nil }
		client, cleanup, err := remote.client("https://public-name.test/mcp", "secret")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = client.Get("https://public-name.test/mcp"); err == nil {
			t.Fatal("DNS resolution bypassed IP policy")
		}
		cleanup()
	}
}

type testRoundTrip func(*http.Request) (*http.Response, error)

func (f testRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestMCPResponseBoundsRedirectsAndReferences(t *testing.T) {
	for _, code := range []int{301, 302, 307, 308} {
		r, _ := NewHTTPRemote("")
		r.transport = testRoundTrip(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: code, Header: http.Header{"Location": []string{"https://another.test/steal"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
		})
		client, cleanup, _ := r.client("https://original.test/mcp", "secret")
		if _, err := client.Get("https://original.test/mcp"); err == nil {
			t.Fatal("redirect accepted")
		}
		cleanup()
	}
	body := &boundedBody{ReadCloser: io.NopCloser(strings.NewReader(strings.Repeat("a", 100))), remaining: 50}
	if raw, err := io.ReadAll(body); err == nil || len(raw) > 50 {
		t.Fatal("unbounded body", len(raw), err)
	}
	if localReferences(map[string]any{"properties": map[string]any{"x": map[string]any{"$ref": "http://127.0.0.1/schema"}}}) {
		t.Fatal("remote reference accepted")
	}
}
