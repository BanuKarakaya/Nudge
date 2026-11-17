package analytics

import (
	"context"
	"encoding/json"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// ModelCall is separate from public operational events. Contents are encrypted
// at rest and available only to the requesting member, never workspace peers.
type ModelCall struct {
	ID         string    `json:"id"`
	Provider   string    `json:"provider"`
	Model      string    `json:"model"`
	Status     int       `json:"status"`
	ErrorCode  string    `json:"error_code,omitempty"`
	StartedAt  time.Time `json:"started_at"`
	DurationMS int64     `json:"duration_ms"`
	Usage      *AIUsage  `json:"usage,omitempty"`
	Request    string    `json:"request,omitempty"`
	Response   string    `json:"response,omitempty"`
	Truncated  bool      `json:"truncated,omitempty"`
}

type modelReporterKey struct{}

func WithModelReporter(ctx context.Context, report func(ModelCall)) context.Context {
	return context.WithValue(ctx, modelReporterKey{}, report)
}
func HasModelReporter(ctx context.Context) bool {
	_, ok := ctx.Value(modelReporterKey{}).(func(ModelCall))
	return ok
}
func ReportModel(ctx context.Context, call ModelCall) {
	if f, ok := ctx.Value(modelReporterKey{}).(func(ModelCall)); ok {
		f(call)
	}
}

var secretPattern = regexp.MustCompile(`(?i)(bearer\s+[^\s"<>]+|(?:sk-|xox[baprs]-|gh[pousr]_|github_pat_)[a-zA-Z0-9_-]{8,}|(?:api[_-]?key|access[_-]?token|refresh[_-]?token|password|client[_-]?secret|authorization)\s*[=:]\s*[^\s,"<>]+)`)
var urlPattern = regexp.MustCompile(`https?://[^\s"<>]+`)

// RedactText removes recognizable credentials and signed/query-bearing links.
// It is defense in depth; content access remains owner-only and bounded.
func RedactText(s string) string {
	s = secretPattern.ReplaceAllString(s, "[REDACTED]")
	return urlPattern.ReplaceAllStringFunc(s, func(raw string) string {
		u, err := url.Parse(raw)
		if err != nil {
			return "[REDACTED URL]"
		}
		u.User = nil
		if u.RawQuery != "" {
			u.RawQuery = "redacted"
		}
		u.Fragment = ""
		return u.String()
	})
}

func RedactJSON(raw []byte, secrets ...string) string {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return "[İçerik JSON olarak okunamadı]"
	}
	var walk func(any) any
	walk = func(v any) any {
		switch x := v.(type) {
		case map[string]any:
			for k, v := range x {
				lower := strings.ToLower(k)
				if strings.Contains(lower, "secret") || strings.Contains(lower, "password") || strings.Contains(lower, "authorization") || strings.Contains(lower, "api_key") || lower == "access_token" || lower == "refresh_token" {
					x[k] = "[REDACTED]"
				} else {
					x[k] = walk(v)
				}
			}
			return x
		case []any:
			for i, v := range x {
				x[i] = walk(v)
			}
			return x
		case string:
			for _, secret := range secrets {
				if secret != "" {
					x = strings.ReplaceAll(x, secret, "[REDACTED]")
				}
			}
			// Tool results and function arguments often contain JSON encoded as text.
			trimmed := strings.TrimSpace(x)
			if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
				var nested any
				if json.Unmarshal([]byte(trimmed), &nested) == nil {
					clean, _ := json.Marshal(walk(nested))
					return string(clean)
				}
			}
			return RedactText(x)
		}
		return v
	}
	clean, _ := json.MarshalIndent(walk(value), "", "  ")
	return string(clean)
}
