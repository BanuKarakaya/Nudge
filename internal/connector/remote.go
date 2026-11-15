package connector

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// HTTPRemote only uses HTTPS. Private origins are an explicit server-operator
// allowlist, never a workspace-manager supplied flag. Proxies and redirects are
// disabled; DNS results are checked and the connection dials the checked IP.
type HTTPRemote struct {
	private map[string]bool
	lookup  func(context.Context, string) ([]net.IPAddr, error)
	// Tests replace the transport with an in-process MCP server.
	transport http.RoundTripper
}

func NewHTTPRemote(privateOrigins string) (*HTTPRemote, error) {
	r := &HTTPRemote{private: map[string]bool{}, lookup: net.DefaultResolver.LookupIPAddr}
	for _, origin := range strings.Split(privateOrigins, ",") {
		origin = strings.TrimSpace(origin)
		if origin == "" {
			continue
		}
		u, err := endpointURL(origin)
		if err != nil || (u.Path != "" && u.Path != "/") {
			return nil, errors.New("NUDGY_MCP_PRIVATE_ORIGINS requires comma-separated HTTPS origins")
		}
		r.private[u.Scheme+"://"+u.Host] = true
	}
	return r, nil
}
func endpointURL(endpoint string) (*url.URL, error) {
	u, err := url.Parse(endpoint)
	if err != nil || len(endpoint) > 2048 || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.ContainsAny(u.Host, "%\\") {
		return nil, ErrUnavailable
	}
	return u, nil
}
func allowedIP(ip net.IP, private bool) bool {
	a, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	a = a.Unmap()
	if !a.IsGlobalUnicast() || a.IsLoopback() || a.IsLinkLocalUnicast() || a.IsUnspecified() {
		return false
	}
	if a.String() == "fd00:ec2::254" || a.String() == "168.63.129.16" {
		return false
	}
	if a.IsPrivate() {
		return private
	}
	// Also exclude shared carrier, documentation, benchmarking and translation
	// ranges. IPv6 is restricted to global unicast 2000::/3 unless private is allowed.
	for _, prefix := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "192.88.99.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001::/23", "2001:db8::/32", "2002::/16"} {
		if netip.MustParsePrefix(prefix).Contains(a) {
			return false
		}
	}
	return a.Is4() || netip.MustParsePrefix("2000::/3").Contains(a)
}
func (r *HTTPRemote) client(endpoint, token string) (*http.Client, func(), error) {
	u, err := endpointURL(endpoint)
	if err != nil {
		return nil, nil, err
	}
	transport := &http.Transport{TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 15 * time.Second, MaxResponseHeaderBytes: 32768, DisableKeepAlives: true}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil || !strings.EqualFold(host, u.Hostname()) {
			return nil, ErrUnavailable
		}
		ips, err := r.lookup(ctx, host)
		if err != nil || len(ips) == 0 {
			return nil, ErrUnavailable
		}
		for _, ip := range ips {
			if !allowedIP(ip.IP, r.private[u.Scheme+"://"+u.Host]) {
				return nil, ErrUnavailable
			}
		}
		var dialer net.Dialer
		dialer.Timeout = 5 * time.Second
		return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
	}
	var base http.RoundTripper = transport
	if r.transport != nil {
		base = r.transport
	}
	client := &http.Client{Timeout: 40 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, Transport: boundedTransport{base, token, u.String()}}
	return client, transport.CloseIdleConnections, nil
}

type boundedTransport struct {
	base            http.RoundTripper
	token, endpoint string
}

func (t boundedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.String() != t.endpoint {
		return nil, ErrUnavailable
	}
	copy := req.Clone(req.Context())
	copy.Header = req.Header.Clone()
	copy.Header.Set("Authorization", "Bearer "+t.token)
	resp, err := t.base.RoundTrip(copy)
	if err != nil {
		return nil, ErrUnavailable
	}
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		resp.Body.Close()
		return nil, ErrUnavailable
	}
	if resp.ContentLength > 524288 {
		resp.Body.Close()
		return nil, ErrUnavailable
	}
	resp.Body = &boundedBody{ReadCloser: resp.Body, remaining: 524288}
	return resp, nil
}

type boundedBody struct {
	io.ReadCloser
	remaining int64
}

func (b *boundedBody) Read(p []byte) (int, error) {
	if b.remaining <= 0 {
		return 0, ErrUnavailable
	}
	if int64(len(p)) > b.remaining {
		p = p[:b.remaining]
	}
	n, err := b.ReadCloser.Read(p)
	b.remaining -= int64(n)
	return n, err
}
func (r *HTTPRemote) session(ctx context.Context, endpoint, token string) (*mcp.ClientSession, func(), error) {
	httpClient, cleanup, err := r.client(endpoint, token)
	if err != nil {
		return nil, nil, err
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "nudgy", Version: "1"}, &mcp.ClientOptions{Capabilities: &mcp.ClientCapabilities{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: endpoint, HTTPClient: httpClient, MaxRetries: -1, DisableStandaloneSSE: true}, nil)
	if err != nil {
		cleanup()
		return nil, nil, ErrUnavailable
	}
	return session, func() { _ = session.Close(); cleanup() }, nil
}

var remoteToolName = regexp.MustCompile(`^[a-zA-Z0-9_.-]{1,128}$`)

func catalog(ctx context.Context, session *mcp.ClientSession) ([]Tool, error) {
	var out []Tool
	seen := map[string]bool{}
	cursors := map[string]bool{}
	cursor := ""
	for page := 0; page < 8; page++ {
		result, err := session.ListTools(ctx, &mcp.ListToolsParams{Cursor: cursor})
		if err != nil || result == nil {
			return nil, ErrUnavailable
		}
		for _, tool := range result.Tools {
			if tool == nil || !remoteToolName.MatchString(tool.Name) || len(tool.Description) > 2048 || seen[tool.Name] || len(out) >= 32 {
				return nil, ErrUnavailable
			}
			seen[tool.Name] = true
			raw, err := json.Marshal(tool)
			if err != nil || len(raw) > 24576 {
				return nil, ErrUnavailable
			}
			schema, ok := tool.InputSchema.(map[string]any)
			if !ok || schema["type"] != "object" || !localReferences(schema) {
				return nil, ErrUnavailable
			}
			if _, err := resolveSchema(schema); err != nil {
				return nil, ErrUnavailable
			}
			sum := sha256.Sum256(raw)
			out = append(out, Tool{Name: tool.Name, Description: tool.Description, Schema: schema, Fingerprint: hex.EncodeToString(sum[:])})
		}
		if result.NextCursor == "" {
			return out, nil
		}
		if cursors[result.NextCursor] {
			return nil, ErrUnavailable
		}
		cursor = result.NextCursor
		cursors[cursor] = true
	}
	return nil, ErrUnavailable
}
func localReferences(v any) bool {
	switch x := v.(type) {
	case map[string]any:
		for k, v := range x {
			if k == "$ref" || k == "$dynamicRef" {
				ref, ok := v.(string)
				if !ok || !strings.HasPrefix(ref, "#") {
					return false
				}
			}
			if !localReferences(v) {
				return false
			}
		}
	case []any:
		for _, v := range x {
			if !localReferences(v) {
				return false
			}
		}
	}
	return true
}

func resolveSchema(schema map[string]any) (*jsonschema.Resolved, error) {
	raw, err := json.Marshal(schema)
	if err != nil {
		return nil, ErrUnavailable
	}
	var parsed jsonschema.Schema
	if json.Unmarshal(raw, &parsed) != nil {
		return nil, ErrUnavailable
	}
	return parsed.Resolve(nil) // No remote schema loader: never fetch $ref URLs.
}
func (r *HTTPRemote) Discover(ctx context.Context, endpoint, token string) ([]Tool, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	session, cleanup, err := r.session(ctx, endpoint, token)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	return catalog(ctx, session)
}
func (r *HTTPRemote) Call(ctx context.Context, endpoint, token string, tool Tool, args json.RawMessage, authorize func(context.Context) error) (string, error) {
	if len(args) > 16000 {
		return "", ErrUnavailable
	}
	var input any
	schema, err := resolveSchema(tool.Schema)
	if err != nil || json.Unmarshal(args, &input) != nil || schema.Validate(input) != nil {
		return "", ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	session, cleanup, err := r.session(ctx, endpoint, token)
	if err != nil {
		return "", err
	}
	defer cleanup()
	tools, err := catalog(ctx, session)
	if err != nil {
		return "", err
	}
	matched := false
	for _, current := range tools {
		if current.Name == tool.Name && current.Fingerprint == tool.Fingerprint {
			matched = true
		}
	}
	if !matched {
		return "", ErrUnavailable
	}
	// Discovery may take time. Recheck revocation after it, immediately before
	// dispatching the actual operation. No database lock is held over HTTP.
	if authorize == nil {
		return "", ErrUnavailable
	}
	if err := authorize(ctx); err != nil {
		return "", err
	}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: tool.Name, Arguments: args})
	if err != nil || result == nil || result.IsError || result.NeedsInput() {
		return "", ErrUnavailable
	}
	// Embedded images/resources and their URLs are never fetched by the host.
	texts := []string{}
	for _, content := range result.Content {
		if text, ok := content.(*mcp.TextContent); ok {
			texts = append(texts, text.Text)
		}
	}
	raw, err := json.Marshal(map[string]any{"source": "untrusted_mcp_tool_result", "text": texts, "structured": result.StructuredContent})
	if err != nil || len(raw) > 65536 {
		return "", ErrUnavailable
	}
	return string(raw), nil
}
