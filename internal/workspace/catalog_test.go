package workspace

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestCatalogVerifiesKeyFiltersModelsAndHidesProviderErrors(t *testing.T) {
	calls := 0
	c := ProviderCatalog{Client: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Host != "openrouter.ai" || r.Header.Get("Authorization") != "Bearer test-secret" {
			t.Fatal("wrong credential destination")
		}
		body := `{"data":{"label":"my-key"}}`
		if r.URL.Path == "/api/v1/models" {
			body = `{"data":[{"id":"vendor/text","name":"Text","supported_parameters":["tools"],"architecture":{"output_modalities":["text"]}},{"id":"vendor/image","supported_parameters":["tools"],"architecture":{"output_modalities":["image"]}},{"id":"vendor/plain","architecture":{"output_modalities":["text"]}}]}`
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}}
	models, e := c.Models(context.Background(), "openrouter", "test-secret")
	if e != nil || len(models) != 1 || models[0].ID != "vendor/text" || calls != 2 {
		t.Fatal("unexpected catalog", models, e)
	}
	if _, e = c.Models(context.Background(), "https://evil.example", "test-secret"); e == nil || calls != 2 {
		t.Fatal("untrusted provider contacted")
	}
	c.Client.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 401, Body: io.NopCloser(strings.NewReader("test-secret"))}, nil
	})
	if _, e = c.Models(context.Background(), "openai", "test-secret"); e == nil || strings.Contains(e.Error(), "test-secret") {
		t.Fatal("provider error not redacted")
	}
}
func TestCatalogDoesNotFollowCredentialRedirect(t *testing.T) {
	calls := 0
	c := ProviderCatalog{Client: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{"https://untrusted.example/"}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	})}}
	if _, e := c.Models(context.Background(), "openai", "secret"); e == nil || calls != 1 {
		t.Fatal("credential redirect followed")
	}
}
