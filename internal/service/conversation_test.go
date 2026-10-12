package service

import (
	"fmt"
	"strings"
	"testing"
)

func TestConversationRetainsRecommendationsAcrossSmallTalk(t *testing.T) {
	var state ConversationState
	state.Remember("özet", "<https://example.com/1|First>\nKonu: Bir.", ConversationRSSSummary)
	for i := 0; i < 20; i++ {
		state.Remember(fmt.Sprint(i), "Rica ederim!", ConversationReply)
	}
	if len(state.Turns) != 12 || len(state.Articles) != 1 {
		t.Fatalf("lost recommendations: %+v", state)
	}
	state.Remember("yeni özet", "<https://example.com/2|Second>\nKonu: İki.", ConversationRSSSummary)
	if len(state.Articles) != 1 || state.Articles[0].URL != "https://example.com/2" {
		t.Fatal("old articles not replaced")
	}
	text, err := state.RenderSelection(ConversationDecision{Action: ConversationSelect, ArticleID: "PREVIOUS-1", Reply: "İlgini çekeceğini düşünüyorum."})
	if err != nil || !strings.Contains(text, "<https://example.com/2|Second>") {
		t.Fatalf("invalid selection %s %v", text, err)
	}
}
