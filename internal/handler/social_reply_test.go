package handler

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"nudge/internal/service"
)

type unavailableSocialHistory struct{ saved bool }

func (*unavailableSocialHistory) Load(context.Context, service.ConversationKey) (service.ConversationState, error) {
	return service.ConversationState{}, errors.New("history unavailable")
}
func (s *unavailableSocialHistory) Save(context.Context, service.ConversationKey, service.ConversationState) error {
	s.saved = true
	return nil
}

func TestThanksDeliveredWithoutAIOrAvailableHistory(t *testing.T) {
	store := &unavailableSocialHistory{}
	sender := &conversationSender{}
	h := &SlackEventHandler{installations: conversationInstallations{}, history: store, client: sender, timezone: time.UTC,
		dialogue: dialogueFunc(func(context.Context, service.ConversationState, string) (service.ConversationDecision, error) {
			t.Fatal("thanks must not depend on AI availability")
			return service.ConversationDecision{}, nil
		}),
	}
	event := slackEventEnvelope{TeamID: "T"}
	event.Event.User, event.Event.Channel, event.Event.ThreadTS, event.Event.Text = "U", "C", "1.0", "<@B123> teşekkürler"
	h.replyWithSummary(event)
	if len(sender.messages) != 1 || !strings.Contains(sender.messages[0], "Rica ederim") {
		t.Fatalf("thanks not delivered: %v", sender.messages)
	}
	if store.saved {
		t.Fatal("failed history load must not overwrite existing conversation")
	}
}
