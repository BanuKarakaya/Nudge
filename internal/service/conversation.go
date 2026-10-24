package service

import (
	"context"
	"time"
)

type ConversationKey struct{ Team, Channel, Thread, User string }

type ConversationExchange struct {
	Request string `json:"request"`
	Reply   string `json:"reply"`
}

type ConversationArticle struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	URL         string `json:"url"`
	Description string `json:"description"`
}

type ConversationState struct {
	AgentThreadID    string                 `json:"agent_thread_id,omitempty"`
	AgentToolVersion string                 `json:"agent_tool_version,omitempty"`
	AgentTokenUsage  *AgentTokenUsage       `json:"agent_token_usage,omitempty"`
	Turns            []ConversationExchange `json:"turns"`
	Articles         []ConversationArticle  `json:"previously_recommended_articles"`
	RSS              *RSSSummaryProgress    `json:"rss_progress,omitempty"`
}

// RSSSummaryProgress preserves pagination data in histories saved before AgentRuntime.
type RSSSummaryProgress struct {
	Scope     string    `json:"scope"`
	Since     time.Time `json:"since"`
	ShownKeys []string  `json:"shown_keys"`
	Total     int       `json:"total"`
}

type ConversationStore interface {
	Load(context.Context, ConversationKey) (ConversationState, error)
	Save(context.Context, ConversationKey, ConversationState) error
}

// Remember records delivered exchanges without changing persisted agent or legacy state.
// The third argument is retained for compatibility with existing callers.
func (s *ConversationState) Remember(request, reply, _ string) {
	s.Turns = append(s.Turns, ConversationExchange{limitConversationText(request, 3000), limitConversationText(reply, 10000)})
	if len(s.Turns) > 12 {
		s.Turns = s.Turns[len(s.Turns)-12:]
	}
}

func limitConversationText(text string, limit int) string {
	runes := []rune(text)
	if len(runes) > limit {
		return string(runes[:limit]) + "\n[Geçmiş mesaj kısaltıldı.]"
	}
	return text
}
