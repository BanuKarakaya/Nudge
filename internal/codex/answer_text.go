package codex

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
)

// Some compatible providers serialize text content blocks inside agentMessage
// text. Decode only that exact text-only envelope, once. Ordinary JSON, fenced
// examples and mixed/unknown content retain their original representation.
func answerText(text string) string {
	trimmed := strings.TrimSpace(text)
	if len(trimmed) > 140000 || !strings.HasPrefix(trimmed, "[") {
		return text
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	d := json.NewDecoder(bytes.NewBufferString(trimmed))
	d.DisallowUnknownFields()
	if d.Decode(&blocks) != nil || len(blocks) == 0 || len(blocks) > 16 {
		return text
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return text
	}
	parts := make([]string, 0, len(blocks))
	for _, b := range blocks {
		if (b.Type != "text" && b.Type != "output_text") || strings.TrimSpace(b.Text) == "" {
			return text
		}
		parts = append(parts, b.Text)
	}
	return strings.Join(parts, "\n\n")
}
