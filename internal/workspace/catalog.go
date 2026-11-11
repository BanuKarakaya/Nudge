package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

type Model struct{ ID, Name string }
type Catalog interface {
	Models(context.Context, string, string) ([]Model, error)
	VerifySearch(context.Context, string, string) error
}
type ProviderCatalog struct{ Client *http.Client }

func (c ProviderCatalog) read(ctx context.Context, endpoint, key string, google bool, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return errors.New("sağlayıcı isteği oluşturulamadı")
	}
	if google {
		req.Header.Set("x-goog-api-key", key)
	} else {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	client := http.Client{Timeout: 15 * time.Second}
	if c.Client != nil {
		client = *c.Client
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, err := client.Do(req)
	if err != nil {
		return errors.New("sağlayıcıya ulaşılamadı; tekrar dene")
	}
	defer res.Body.Close()
	if res.StatusCode == 401 || res.StatusCode == 403 {
		return errors.New("API anahtarı veya sağlayıcı izinleri doğrulanamadı")
	}
	if res.StatusCode != 200 {
		return errors.New("sağlayıcı isteği tamamlanamadı; kota ve servis durumunu kontrol et")
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(out); err != nil {
		return errors.New("sağlayıcının yanıtı okunamadı")
	}
	return nil
}
func (c ProviderCatalog) Models(ctx context.Context, provider, key string) ([]Model, error) {
	base := ProviderURL(provider)
	if base == "" || key == "" || len(key) > 4096 || strings.ContainsAny(key, "\r\n\x00") {
		return nil, errors.New("geçerli sağlayıcı ve API anahtarı gerekli")
	}
	if provider == "openrouter" {
		var verification struct {
			Data json.RawMessage `json:"data"`
		}
		if err := c.read(ctx, base+"/key", key, false, &verification); err != nil {
			return nil, err
		}
		if len(verification.Data) == 0 || string(verification.Data) == "null" {
			return nil, errors.New("API anahtarı doğrulanamadı")
		}
	}
	var data struct {
		Data []struct {
			ID           string   `json:"id"`
			Name         string   `json:"name"`
			Parameters   []string `json:"supported_parameters"`
			Architecture struct {
				Outputs []string `json:"output_modalities"`
			} `json:"architecture"`
		} `json:"data"`
	}
	if err := c.read(ctx, base+"/models", key, false, &data); err != nil {
		return nil, err
	}
	out := []Model{}
	seen := map[string]bool{}
	for _, m := range data.Data {
		if !ValidModel(m.ID) || seen[m.ID] {
			continue
		}
		if provider == "openrouter" {
			tools, text := false, false
			for _, p := range m.Parameters {
				tools = tools || p == "tools"
			}
			for _, p := range m.Architecture.Outputs {
				text = text || p == "text"
			}
			if !tools || !text {
				continue
			}
		}
		if provider == "openai" {
			if !(strings.HasPrefix(m.ID, "gpt-") || strings.HasPrefix(m.ID, "o1") || strings.HasPrefix(m.ID, "o3") || strings.HasPrefix(m.ID, "o4")) {
				continue
			}
			if strings.Contains(m.ID, "realtime") || strings.Contains(m.ID, "audio") || strings.Contains(m.ID, "image") || strings.Contains(m.ID, "transcribe") {
				continue
			}
		}
		if m.Name == "" {
			m.Name = m.ID
		}
		out = append(out, Model{m.ID, m.Name})
		seen[m.ID] = true
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
func (c ProviderCatalog) VerifySearch(ctx context.Context, key, model string) error {
	if key == "" || len(key) > 4096 || strings.ContainsAny(key, "\r\n\x00") || !ValidModel(model) {
		return errors.New("Google API anahtarı ve model kimliği gerekli")
	}
	var data struct {
		Name    string   `json:"name"`
		Methods []string `json:"supportedGenerationMethods"`
	}
	if err := c.read(ctx, "https://generativelanguage.googleapis.com/v1beta/models/"+url.PathEscape(model), key, true, &data); err != nil {
		return err
	}
	for _, method := range data.Methods {
		if method == "generateContent" {
			return nil
		}
	}
	return errors.New("seçilen Google modeli metin üretimini desteklemiyor")
}
