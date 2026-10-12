package handler

import (
	"context"
	"encoding/json"
	"errors"
	"nudge/internal/domain"
	"nudge/internal/repository"
	"nudge/internal/service"
	"strings"
	"testing"
	"time"
)

type conversationTestStore map[service.ConversationKey][]byte

func (s conversationTestStore) Load(_ context.Context, k service.ConversationKey) (service.ConversationState, error) {
	var state service.ConversationState
	if raw, ok := s[k]; ok {
		err := json.Unmarshal(raw, &state)
		return state, err
	}
	return state, nil
}
func (s conversationTestStore) Save(_ context.Context, k service.ConversationKey, state service.ConversationState) error {
	raw, err := json.Marshal(state)
	s[k] = raw
	return err
}

type dialogueFunc func(context.Context, service.ConversationState, string) (service.ConversationDecision, error)

type webFunc func(context.Context, string) (string, error)

func (f webFunc) SearchWeb(ctx context.Context, request string) (string, error) {
	return f(ctx, request)
}

func (f dialogueFunc) Converse(c context.Context, s service.ConversationState, r string) (service.ConversationDecision, error) {
	return f(c, s, r)
}

type conversationInstallations struct {
	repository.InstallationRepository
}

func (conversationInstallations) GetUserIDBySlackUserID(context.Context, string, string) (int64, error) {
	return 1, nil
}
func (conversationInstallations) GetByUserID(context.Context, int64) (domain.SlackInstallation, error) {
	return domain.SlackInstallation{BotToken: "test"}, nil
}

type conversationSender struct {
	messages []string
	fail     bool
}

func (s *conversationSender) PostMessageInThread(_ context.Context, _, _, _, text string) error {
	if s.fail {
		return errors.New("send failed")
	}
	s.messages = append(s.messages, text)
	return nil
}

func TestFollowUpUsesDeliveredSummaryAfterThanksAndHandlerRestart(t *testing.T) {
	store := make(conversationTestStore)
	key := service.ConversationKey{Team: "T", Channel: "C", Thread: "1.0", User: "U"}
	state := service.ConversationState{}
	state.Remember("rss özetimi ver", "<https://example.com/one|First>\nKonu: Bir.\n<https://example.com/two|Second>\nKonu: İki.\n<https://example.com/three|Third>\nKonu: Üç.", service.ConversationRSSSummary)
	if err := store.Save(context.Background(), key, state); err != nil {
		t.Fatal(err)
	}
	sender := &conversationSender{}
	makeHandler := func(model service.ConversationAssistant) *SlackEventHandler {
		return &SlackEventHandler{installations: conversationInstallations{}, history: store, dialogue: model, client: sender, timezone: time.UTC}
	}
	event := slackEventEnvelope{TeamID: "T"}
	event.Event.User = "U"
	event.Event.Channel = "C"
	event.Event.ThreadTS = "1.0"
	event.Event.Text = "Teşekkür ederim"
	h := makeHandler(dialogueFunc(func(_ context.Context, s service.ConversationState, _ string) (service.ConversationDecision, error) {
		return service.ConversationDecision{Action: service.ConversationReply, Reply: "Rica ederim!"}, nil
	}))
	h.replyWithSummary(event)
	requests := []string{
		"peki bu bana verdiğin rss özetinden sadece 1 tane rss okuyacak zamanım var desem hangi rss i seçerdim",
		"Bunlardan birini seçsen?",
	}
	for _, request := range requests {
		event.Event.Text = request
		h = makeHandler(dialogueFunc(func(_ context.Context, s service.ConversationState, r string) (service.ConversationDecision, error) {
			if r != request || len(s.Articles) != 3 || len(s.Turns) < 2 {
				t.Fatalf("context missing: %+v", s)
			}
			return service.ConversationDecision{Action: service.ConversationSelect, ArticleID: s.Articles[1].ID, Reply: "İkinci makale ihtiyacına daha uygun."}, nil
		}))
		// Feedbin is deliberately nil: a follow-up must not fetch a new inventory.
		h.replyWithSummary(event)
		last := sender.messages[len(sender.messages)-1]
		if strings.Count(last, "https://") != 1 || !strings.Contains(last, "https://example.com/two") || strings.Contains(last, "First") {
			t.Fatalf("wrong selection: %s", last)
		}
	}
	for _, change := range []string{"user", "team", "channel", "thread"} {
		other := event
		switch change {
		case "user":
			other.Event.User = "U2"
		case "team":
			other.TeamID = "T2"
		case "channel":
			other.Event.Channel = "C2"
		case "thread":
			other.Event.ThreadTS = "2.0"
		}
		h = makeHandler(dialogueFunc(func(_ context.Context, s service.ConversationState, _ string) (service.ConversationDecision, error) {
			if len(s.Articles) != 0 || len(s.Turns) != 0 {
				t.Fatalf("history leaked across %s", change)
			}
			return service.ConversationDecision{Action: service.ConversationReply, Reply: "Önceki özeti burada göremiyorum."}, nil
		}))
		h.replyWithSummary(other)
	}
}

func TestUnknownArticleDoesNotBecomeANewRSSRequest(t *testing.T) {
	h := &SlackEventHandler{dialogue: dialogueFunc(func(context.Context, service.ConversationState, string) (service.ConversationDecision, error) {
		return service.ConversationDecision{Action: service.ConversationSelect, ArticleID: "PREVIOUS-999", Reply: "Bunu seçtim."}, nil
	})}
	message, _, err := h.conversationReply(context.Background(), service.ConversationState{}, "birini seç", 1, time.Now())
	if err == nil || message == "" {
		t.Fatalf("expected explicit failure, got %s %v", message, err)
	}
}

func TestUndeliveredAnswerIsNotRemembered(t *testing.T) {
	store := make(conversationTestStore)
	sender := &conversationSender{fail: true}
	h := &SlackEventHandler{
		installations: conversationInstallations{}, history: store, client: sender,
		dialogue: dialogueFunc(func(context.Context, service.ConversationState, string) (service.ConversationDecision, error) {
			return service.ConversationDecision{Action: service.ConversationReply, Reply: "Merhaba!"}, nil
		}),
	}
	event := slackEventEnvelope{TeamID: "T"}
	event.Event.Channel, event.Event.User, event.Event.TS = "C", "U", "1.0"
	event.Event.Text = "selam"
	h.replyWithSummary(event)
	if len(store) != 0 {
		t.Fatal("undelivered answer was saved as conversation history")
	}
}

func TestWebRequestDoesNotFetchRSSAndIsRememberedForFollowUp(t *testing.T) {
	store := make(conversationTestStore)
	sender := &conversationSender{}
	h := &SlackEventHandler{installations: conversationInstallations{}, history: store, client: sender}
	h.dialogue = dialogueFunc(func(_ context.Context, s service.ConversationState, _ string) (service.ConversationDecision, error) {
		return service.ConversationDecision{Action: service.ConversationWebSearch, SearchRequest: "AI agent mimarileri hakkında bir makale öner."}, nil
	})
	calls := 0
	h.web = webFunc(func(_ context.Context, request string) (string, error) {
		calls++
		if request != "AI agent mimarileri hakkında bir makale öner." {
			t.Fatalf("wrong request: %s", request)
		}
		return "AI agent mimarileri.\n<https://example.com/agents|Agent makalesi>", nil
	})
	event := slackEventEnvelope{TeamID: "T"}
	event.Event.Channel, event.Event.User, event.Event.TS = "C", "U", "1.0"
	event.Event.Text = "RSSlerimden değil internetten AI agent makalesi öner"
	// Neither a Feedbin nor a Raindrop connection exists in this test.
	h.replyWithSummary(event)
	event.Event.ThreadTS = "1.0"
	event.Event.Text = "Bu makale neden önemli?"
	h.dialogue = dialogueFunc(func(_ context.Context, s service.ConversationState, _ string) (service.ConversationDecision, error) {
		if len(s.Turns) != 1 || !strings.Contains(s.Turns[0].Reply, "example.com/agents") {
			t.Fatalf("missing web context: %+v", s)
		}
		return service.ConversationDecision{Action: service.ConversationReply, Reply: "Agent mimarilerini karşılaştırdığı için."}, nil
	})
	h.replyWithSummary(event)
	if calls != 1 || len(sender.messages) != 2 || !strings.Contains(sender.messages[1], "karşılaştırdığı") {
		t.Fatalf("unexpected calls/messages: %d %+v", calls, sender.messages)
	}
}

func TestFailedWebSearchDoesNotFallBackToRSS(t *testing.T) {
	h := &SlackEventHandler{
		dialogue: dialogueFunc(func(context.Context, service.ConversationState, string) (service.ConversationDecision, error) {
			return service.ConversationDecision{Action: service.ConversationWebSearch, SearchRequest: "AI agents"}, nil
		}),
		web: webFunc(func(context.Context, string) (string, error) { return "", errors.New("quota") }),
	}
	message, action, err := h.conversationReply(context.Background(), service.ConversationState{}, "ara", 1, time.Now())
	if err == nil || action != service.ConversationWebSearch || !strings.Contains(message, "İnternet aramasını") {
		t.Fatalf("wrong failure: %s %s %v", action, message, err)
	}
}
