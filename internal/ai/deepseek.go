package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"nudge/internal/service"
)

// DeepSeekClient is the OpenAI-compatible DeepSeek Chat Completions client.
// deepseek-flash is the API model identifier for the V4.1 Flash family.
type DeepSeekClient struct {
	APIKey     string
	Model      string
	BaseURL    string
	HTTPClient *http.Client
}

type deepSeekMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type deepSeekRequest struct {
	Model          string            `json:"model"`
	Messages       []deepSeekMessage `json:"messages"`
	Temperature    float64           `json:"temperature,omitempty"`
	MaxTokens      int               `json:"max_tokens,omitempty"`
	ResponseFormat *deepSeekJSONMode `json:"response_format,omitempty"`
}

type deepSeekJSONMode struct {
	Type string `json:"type"`
}

type deepSeekResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type deepSeekConversationOutput struct {
	Action        string `json:"action"`
	Reply         string `json:"reply"`
	ArticleID     string `json:"article_id"`
	SearchRequest string `json:"search_request"`
}

func (c DeepSeekClient) Converse(ctx context.Context, state service.ConversationState, request string) (service.ConversationDecision, error) {
	stateJSON, err := json.Marshal(state)
	if err != nil {
		return service.ConversationDecision{}, fmt.Errorf("marshal conversation state: %w", err)
	}

	system := `Sen Nudge adında, Slack içinde çalışan Türkçe bir AI asistansın.
Kullanıcının isteğini konuşma bağlamına göre anla. RSS, bookmark ve Feedbin/Raindrop işlemleri için uygun action değerini seç; sıradan sohbet, açıklama ve selamlaşmalarda REPLY kullan.

Kullanılabilen action değerleri:
- REPLY: genel sohbet, sorular ve açıklamalar
- RSS_SUMMARY: RSS özetini isteyince
- RSS_TITLES: yalnızca RSS başlıklarını isteyince
- RSS_STATS: toplam okunmamış RSS veya okunmama başlangıç tarihini isteyince
- RSS_CATEGORIES: RSS kategorilerini/klasörlerini ve adetlerini isteyince
- RSS_RECOMMEND: RSS'ler arasından öneri veya seçim isteyince
- BOOKMARKS: Raindrop bookmarklarını isteyince
- WEB_SEARCH: internette güncel bir şey arama veya RSS dışında makale isteme
- SELECT_PREVIOUS: önceki RSS/bookmark önerisinden belirli bir seçim isteme

Yanıtını mutlaka geçerli JSON olarak, tam şu şekle yakın üret:
{"action":"REPLY","reply":"...","article_id":0,"search_request":""}
article_id yalnızca SELECT_PREVIOUS için kullanılır. WEB_SEARCH için search_request alanına aranacak konuyu yaz. RSS_SUMMARY, RSS_TITLES, RSS_STATS, RSS_CATEGORIES, RSS_RECOMMEND ve BOOKMARKS için reply boş olabilir.`

	user := fmt.Sprintf("Konuşma durumu JSON: %s\n\nKullanıcının son isteği: %s", string(stateJSON), strings.TrimSpace(request))
	content, err := c.chat(ctx, []deepSeekMessage{
		{Role: "system", Content: system},
		{Role: "user", Content: user},
	}, true)
	if err != nil {
		return service.ConversationDecision{}, err
	}

	var output deepSeekConversationOutput
	if err := json.Unmarshal([]byte(content), &output); err != nil {
		return service.ConversationDecision{}, fmt.Errorf("DeepSeek returned invalid conversation JSON: %w", err)
	}
	action := strings.ToUpper(strings.TrimSpace(output.Action))
	if action == "" {
		return service.ConversationDecision{}, fmt.Errorf("DeepSeek returned an empty conversation action")
	}

	return service.ConversationDecision{
		Action:        action,
		Reply:         strings.TrimSpace(output.Reply),
		ArticleID:     output.ArticleID,
		SearchRequest: strings.TrimSpace(output.SearchRequest),
	}, nil
}

func (c DeepSeekClient) Summarize(ctx context.Context, input string) (string, error) {
	prompt := `Aşağıdaki Feedbin RSS kayıtlarını Türkçe ve anlaşılır bir gün sonu özeti olarak düzenle.
En fazla 5 öne çıkan makale seç. Her biri için başlık, çalışan kaynak linki, “Konu” ve “Neden önemli” alanlarını yaz. Kalan içerikleri tek tek listeleme; gerekirse kısa bir “Diğer” bölümüyle eğilimleri belirt. Yalnızca verilen kayıtları kullan, linkleri değiştirme ve cevabı yarım bırakma. Slack Markdown kullan.

RSS kayıtları:
` + input
	return c.chat(ctx, []deepSeekMessage{
		{Role: "system", Content: "Sen dikkatli bir RSS editörüsün. Uydurma başlık, URL veya bilgi ekleme."},
		{Role: "user", Content: prompt},
	}, false)
}

func (c DeepSeekClient) Recommend(ctx context.Context, input string) (string, error) {
	prompt := `Aşağıdaki RSS kayıtları içinden kullanıcının isteğine en uygun makaleyi seç. Başlık, çalışan kaynak linki, kısa konu açıklaması ve neden bu makaleyi seçtiğini Türkçe olarak belirt. Sadece verilen kayıtları kullan; konu dışı veya rastgele seçim yapma. Cevabı tamamla ve Slack Markdown kullan.

RSS kayıtları ve kullanıcı isteği:
` + input
	return c.chat(ctx, []deepSeekMessage{
		{Role: "system", Content: "Sen kişiselleştirilmiş bir RSS editörüsün. Önerini listedeki içeriklerden kanıta dayalı seç."},
		{Role: "user", Content: prompt},
	}, false)
}

func (c DeepSeekClient) chat(ctx context.Context, messages []deepSeekMessage, jsonMode bool) (string, error) {
	model := c.Model
	if model == "" {
		model = "deepseek-flash"
	}
	// Keep friendly/versioned names working while sending the current public
	// API identifier to DeepSeek.
	if model == "deepseek-v4.1-flash" || model == "deepseek-v4-flash" {
		model = "deepseek-flash"
	}
	baseURL := strings.TrimRight(c.BaseURL, "/")
	if baseURL == "" {
		baseURL = "https://api.deepseek.com"
	}
	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}

	payload := deepSeekRequest{
		Model:       model,
		Messages:    messages,
		Temperature: 0.2,
		MaxTokens:   4000,
	}
	if jsonMode {
		payload.ResponseFormat = &deepSeekJSONMode{Type: "json_object"}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal DeepSeek request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create DeepSeek request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("call DeepSeek: %w", err)
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return "", fmt.Errorf("read DeepSeek response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("DeepSeek returned %s: %s", resp.Status, strings.TrimSpace(string(responseBody)))
	}

	var decoded deepSeekResponse
	if err := json.Unmarshal(responseBody, &decoded); err != nil {
		return "", fmt.Errorf("decode DeepSeek response: %w", err)
	}
	if decoded.Error != nil {
		return "", fmt.Errorf("DeepSeek error: %s", decoded.Error.Message)
	}
	if len(decoded.Choices) == 0 || strings.TrimSpace(decoded.Choices[0].Message.Content) == "" {
		return "", fmt.Errorf("DeepSeek returned an empty response")
	}
	return strings.TrimSpace(decoded.Choices[0].Message.Content), nil
}
