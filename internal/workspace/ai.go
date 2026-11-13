package workspace

import (
	"context"
	"errors"
	"net/url"
	"os"
	"regexp"
	"strings"
)

var ErrAINotConfigured = errors.New("çalışma alanının AI ayarları hazır değil; yönetici dashboard’dan API anahtarını ve modelini seçmeli")
var ErrAIConflict = errors.New("AI ayarları başka bir oturumda değişti; sayfayı yenileyin")

// AISettings is safe to render. Credentials never belong in a page or API response.
type AISettings struct {
	ToolModels   map[string]string
	Provider     string
	Model        string
	ComplexModel string
	SearchModel  string
	HasKey       bool
	HasSearchKey bool
	Revision     string
}

func (s AISettings) Ready() bool { return s.HasKey && s.Model != "" }

type AICredentials struct {
	AISettings
	APIKey       string `json:"-"`
	SearchAPIKey string `json:"-"`
}
type AIStore interface {
	AISettings(context.Context, int64) (AISettings, error)
	AICredentials(context.Context, int64) (AICredentials, error)
	SaveAI(context.Context, int64, AICredentials, string) error
}

// Fixed destinations prevent a supplied provider URL from receiving workspace keys.
func ProviderURL(provider string) string {
	switch provider {
	case "custom":
		raw := strings.TrimRight(strings.TrimSpace(os.Getenv("NUDGY_CUSTOM_AI_BASE_URL")), "/")
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return ""
		}
		local := u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1"
		if u.Scheme != "https" && !(u.Scheme == "http" && local) {
			return ""
		}
		return raw
	case "openrouter":
		return "https://openrouter.ai/api/v1"
	case "openai":
		return "https://api.openai.com/v1"
	}
	return ""
}

var modelID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._:/@+-]{0,199}$`)

func ValidModel(s string) bool { return modelID.MatchString(s) }

var toolName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,99}$`)

func ValidToolModels(routes map[string]string) bool {
	if len(routes) > 100 {
		return false
	}
	for tool, model := range routes {
		if !toolName.MatchString(tool) || !ValidModel(model) {
			return false
		}
	}
	return true
}
