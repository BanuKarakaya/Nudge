package service

import (
	"context"
	"fmt"
	"regexp"
	"strings"
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
	Turns    []ConversationExchange `json:"turns"`
	Articles []ConversationArticle  `json:"previously_recommended_articles"`
}

type ConversationStore interface {
	Load(context.Context, ConversationKey) (ConversationState, error)
	Save(context.Context, ConversationKey, ConversationState) error
}

// Actions are data capabilities. Gemini interprets the whole conversation,
// rather than the handler assigning intent from words in the latest message.
const (
	ConversationReply        = "REPLY"
	ConversationSelect       = "SELECT_PREVIOUS"
	ConversationRSSSummary   = "RSS_SUMMARY"
	ConversationRSSTitles    = "RSS_TITLES"
	ConversationRSSStats     = "RSS_STATS"
	ConversationRSSRecommend = "RSS_RECOMMEND"
	ConversationBookmarks    = "BOOKMARKS"
)

type ConversationDecision struct {
	Action    string `json:"action"`
	Reply     string `json:"reply"`
	ArticleID string `json:"article_id"`
}

type ConversationAssistant interface {
	Converse(context.Context, ConversationState, string) (ConversationDecision, error)
}

func (d ConversationDecision) Validate(state ConversationState) error {
	switch d.Action {
	case ConversationReply:
		if strings.TrimSpace(d.Reply) == "" {
			return fmt.Errorf("empty conversation reply")
		}
	case ConversationSelect:
		if strings.TrimSpace(d.Reply) == "" {
			return fmt.Errorf("missing selection reason")
		}
		for _, a := range state.Articles {
			if a.ID == d.ArticleID {
				return nil
			}
		}
		return fmt.Errorf("selection is not in the previously recommended articles")
	case ConversationRSSSummary, ConversationRSSTitles, ConversationRSSStats, ConversationRSSRecommend, ConversationBookmarks:
	default:
		return fmt.Errorf("unknown conversation action %q", d.Action)
	}
	return nil
}

func (s ConversationState) RenderSelection(d ConversationDecision) (string, error) {
	if d.Action != ConversationSelect {
		return "", fmt.Errorf("not a selection")
	}
	if err := d.Validate(s); err != nil {
		return "", err
	}
	for _, a := range s.Articles {
		if a.ID == d.ArticleID {
			escape := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
			return fmt.Sprintf("🎯 *O önerilerden bunu seçerdim Beybi*\n\n<%s|%s>\n\n%s", a.URL, a.Title, escape.Replace(d.Reply)), nil
		}
	}
	return "", fmt.Errorf("article missing")
}

var conversationArticleLink = regexp.MustCompile(`<((?:https?://)[^<>|\s]+)\|([^<>]+)>`)

// Parse only the actual delivered summary, not the full Feedbin inventory.
func RecommendedArticles(reply string) []ConversationArticle {
	matches := conversationArticleLink.FindAllStringSubmatchIndex(reply, -1)
	var articles []ConversationArticle
	seen := make(map[string]bool)
	for i, match := range matches {
		url := reply[match[2]:match[3]]
		if seen[url] {
			continue
		}
		seen[url] = true
		end := len(reply)
		if i+1 < len(matches) {
			end = matches[i+1][0]
		}
		articles = append(articles, ConversationArticle{
			ID:    fmt.Sprintf("PREVIOUS-%d", len(articles)+1),
			Title: reply[match[4]:match[5]], URL: url,
			Description: limitConversationText(strings.TrimSpace(reply[match[1]:end]), 1800),
		})
	}
	return articles
}

func (s *ConversationState) Remember(request, reply, action string) {
	s.Turns = append(s.Turns, ConversationExchange{limitConversationText(request, 3000), limitConversationText(reply, 10000)})
	if len(s.Turns) > 12 {
		s.Turns = s.Turns[len(s.Turns)-12:]
	}
	if action == ConversationRSSSummary || action == ConversationRSSRecommend {
		s.Articles = RecommendedArticles(reply)
	}
}

func limitConversationText(text string, limit int) string {
	runes := []rune(text)
	if len(runes) > limit {
		return string(runes[:limit]) + "\n[Geçmiş mesaj kısaltıldı.]"
	}
	return text
}
