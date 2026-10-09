package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

const defaultModel = "gemini-2.5-flash"

type GeminiClient struct {
	APIKey     string
	Model      string
	HTTPClient *http.Client
}

type generateRequest struct {
	Contents         []content        `json:"contents"`
	GenerationConfig generationConfig `json:"generationConfig"`
}

type content struct {
	Parts []part `json:"parts"`
}

type part struct {
	Text string `json:"text"`
}

type generationConfig struct {
	Temperature     float32 `json:"temperature"`
	MaxOutputTokens int     `json:"maxOutputTokens"`
}

type generateResponse struct {
	Candidates []struct {
		Content content `json:"content"`
	} `json:"candidates"`
}

func (c GeminiClient) Summarize(ctx context.Context, input string) (string, error) {
	if c.APIKey == "" {
		return "", fmt.Errorf("Gemini API key is not configured")
	}
	model := c.Model
	if model == "" {
		model = defaultModel
	}
	prompt := `Sen Nudge adlı kişisel RSS asistanısın. Aşağıdaki Feedbin okunmamış RSS listesini Türkçe, kısa ve anlaşılır bir gün sonu analizine dönüştür.

Kurallar:
- Sadece verilen başlıkları kullan; verilen listede olmayan bilgi uydurma.
- En fazla 5 önemli gelişmeyi "🔥 Öne çıkanlar" altında yaz.
- Folder bazında en fazla 2 kısa tema/çıkarım yaz.
- Önemsiz veya tekrar eden içerikleri "⏭️ Daha sonra bakılabilecekler" altında kısaca belirt.
- Her maddeyi mümkünse başlığa Slack linki vererek yaz.
- Çıktı yalnızca Slack Markdown mesajı olsun; giriş/çıkış açıklaması ekleme.

RSS listesi:
` + input

	body, err := json.Marshal(generateRequest{
		Contents:         []content{{Parts: []part{{Text: prompt}}}},
		GenerationConfig: generationConfig{Temperature: 0.2, MaxOutputTokens: 1800},
	})
	if err != nil {
		return "", err
	}
	endpoint := "https://generativelanguage.googleapis.com/v1beta/models/" + url.PathEscape(model) + ":generateContent?key=" + url.QueryEscape(c.APIKey)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errorBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("Gemini API returned HTTP %d: %s", resp.StatusCode, string(errorBody))
	}
	var result generateResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	if len(result.Candidates) == 0 || len(result.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("Gemini returned no summary")
	}
	return result.Candidates[0].Content.Parts[0].Text, nil
}
