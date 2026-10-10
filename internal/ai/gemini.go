package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
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

func (c GeminiClient) generate(ctx context.Context, prompt string, maxOutputTokens int) (string, error) {
	body, err := json.Marshal(generateRequest{
		Contents:         []content{{Parts: []part{{Text: prompt}}}},
		GenerationConfig: generationConfig{Temperature: 0, MaxOutputTokens: maxOutputTokens},
	})
	if err != nil {
		return "", err
	}
	model := c.Model
	if model == "" {
		model = defaultModel
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
		return "", fmt.Errorf("Gemini returned no text")
	}
	return result.Candidates[0].Content.Parts[0].Text, nil
}

func (c GeminiClient) ClassifyRSSRequest(ctx context.Context, request string) (string, error) {
	if c.APIKey == "" {
		return "", fmt.Errorf("Gemini API key is not configured")
	}
	prompt := `Aşağıdaki Slack isteği RSS başlıklarını mı istiyor, yoksa RSS özeti/analizi mi istiyor?
Yalnızca tek kelime cevap ver: TITLES veya SUMMARY.
Özet, önemli içerikler, analiz, değerlendir, günün özeti gibi istekler SUMMARY'dir.
Başlıklar, liste, neler var, göster gibi istekler TITLES'dır.

İstek: ` + request
	result, err := c.generate(ctx, prompt, 5)
	if err != nil {
		return "", err
	}
	result = strings.ToUpper(strings.TrimSpace(result))
	if result != "TITLES" && result != "SUMMARY" {
		return "", fmt.Errorf("unexpected RSS intent %q", result)
	}
	return result, nil
}

func (c GeminiClient) Respond(ctx context.Context, request string) (string, error) {
	if c.APIKey == "" {
		return "", fmt.Errorf("Gemini API key is not configured")
	}
	prompt := `Sen Nudge'sın: sıcak, kısa ve yardımcı bir Türkçe kişisel asistansın.
Kullanıcının mesajına doğal bir şekilde cevap ver. Kullanıcı RSS veya bookmark istemiyorsa dış servislere eriştiğini iddia etme; yalnızca sohbet et.
Gereksiz uzun açıklama, sistem bilgisi veya yapay zekâ olduğunu belirten ifade kullanma.

Kullanıcı mesajı: ` + request
	return c.generate(ctx, prompt, 500)
}

func (c GeminiClient) Summarize(ctx context.Context, input string) (string, error) {
	if c.APIKey == "" {
		return "", fmt.Errorf("Gemini API key is not configured")
	}
	prompt := `Sen Nudge adlı kişisel RSS asistanısın. Aşağıdaki Feedbin okunmamış RSS listesini Türkçe, kısa ve anlaşılır bir gün sonu analizine dönüştür.

Kurallar:
- Çıktının ilk satırı tam olarak şu olsun: 📰 *Bugünkü RSS Özetin Beybi*
- En az 3 makale varsa 3 ila 5 makale seç; yalnızca tek bir makale önerme.
- Her seçilen makale için önce doğru Slack linkini tek satırda yaz: • <TAM_URL|BAŞLIK>
- Linkteki URL'yi asla değiştirme, kısaltma, URL-encode etme veya başlığın bir parçasını URL'ye taşıma.
- Link satırının hemen altında iki kısa satır yaz: "Konu: ..." ve "Neden önemli: ...".
- Konu ve önem açıklamasını yalnızca verilen içerik özeti ve başlığa dayanarak yaz; bilgi uydurma.
- En fazla 5 önemli gelişmeyi "🔥 Öne çıkanlar" altında yaz.
- Folder bazında tekrar eden temaları "📂 Temalar" altında en fazla 3 maddeyle belirt.
- Seçilmeyen içerikleri "⏭️ Daha sonra bakılabilecekler" altında başlıklarıyla kısaca belirt.
- Çıktı yalnızca Slack Markdown mesajı olsun; "Merhaba", kod bloğu veya ayraç satırı ekleme.
- Başlıklar için Slack'in kalın yazımını kullan; Markdown başlık işaretleri kullanma.

RSS listesi:
` + input

	return c.generate(ctx, prompt, 1800)
}
