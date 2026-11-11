package workspace

import (
	"context"
	"errors"
	"regexp"
)

var ErrAINotConfigured = errors.New("çalışma alanının AI ayarları hazır değil; yönetici dashboard’dan API anahtarını ve modelini seçmeli")
var ErrAIConflict = errors.New("AI ayarları başka bir oturumda değişti; sayfayı yenileyin")

// AISettings is safe to render. Credentials never belong in a page or API response.
type AISettings struct {
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
	case "openrouter":
		return "https://openrouter.ai/api/v1"
	case "openai":
		return "https://api.openai.com/v1"
	}
	return ""
}

var modelID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._:/@+-]{0,199}$`)

func ValidModel(s string) bool { return modelID.MatchString(s) }
