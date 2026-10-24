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
	"time"

	"nudge/internal/analytics"
)

const defaultModel = "gemini-2.5-flash"

// GeminiClient implements the optional Google Search tool.
type GeminiClient struct {
	APIKey     string
	Model      string
	HTTPClient *http.Client
}

type generateRequest struct {
	Contents         []content        `json:"contents"`
	GenerationConfig generationConfig `json:"generationConfig"`
	Tools            []geminiTool     `json:"tools,omitempty"`
}

type geminiTool struct {
	GoogleSearch *struct{} `json:"google_search,omitempty"`
}

type content struct {
	Parts []part `json:"parts"`
}

type part struct {
	Text    string `json:"text"`
	Thought bool   `json:"thought,omitempty"`
}

type generationConfig struct {
	Temperature     float32 `json:"temperature"`
	MaxOutputTokens int     `json:"maxOutputTokens"`
}

type generateResponse struct {
	UsageMetadata struct {
		PromptTokenCount     int `json:"promptTokenCount"`
		CandidatesTokenCount int `json:"candidatesTokenCount"`
		TotalTokenCount      int `json:"totalTokenCount"`
	} `json:"usageMetadata"`
	Candidates []struct {
		Content           content           `json:"content"`
		FinishReason      string            `json:"finishReason"`
		GroundingMetadata groundingMetadata `json:"groundingMetadata"`
	} `json:"candidates"`
}

func (c GeminiClient) callGenerate(ctx context.Context, payload generateRequest) (generateResponse, error) {
	var result generateResponse
	body, err := json.Marshal(payload)
	if err != nil {
		return result, err
	}
	model := c.Model
	if model == "" {
		model = defaultModel
	}
	endpoint := "https://generativelanguage.googleapis.com/v1beta/models/" + url.PathEscape(model) + ":generateContent"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return result, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", c.APIKey)
	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	started := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return result, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Avoid logging secrets, user input, or reflected prompts from error bodies.
		return result, fmt.Errorf("Gemini API returned HTTP %d", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&result); err != nil {
		return result, err
	}
	if tracker := analytics.UsageTrackerFromContext(ctx); tracker != nil {
		modelName := c.Model
		if modelName == "" {
			modelName = defaultModel
		}
		tracker.Add(analytics.AIUsage{
			Provider: "Gemini", Model: modelName,
			PromptTokens:     result.UsageMetadata.PromptTokenCount,
			CompletionTokens: result.UsageMetadata.CandidatesTokenCount,
			TotalTokens:      result.UsageMetadata.TotalTokenCount,
			LatencyMS:        time.Since(started).Milliseconds(),
		})
	}
	return result, nil
}

func (result generateResponse) text() (string, error) {
	if len(result.Candidates) == 0 || len(result.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("Gemini returned no text")
	}
	if result.Candidates[0].FinishReason != "STOP" {
		return "", fmt.Errorf("Gemini incomplete response: %s", result.Candidates[0].FinishReason)
	}
	var output strings.Builder
	for _, part := range result.Candidates[0].Content.Parts {
		if !part.Thought {
			output.WriteString(part.Text)
		}
	}
	if strings.TrimSpace(output.String()) == "" {
		return "", fmt.Errorf("Gemini returned empty text")
	}
	return output.String(), nil
}
