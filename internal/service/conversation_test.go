package service

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestConversationHistoryCompatibility(t *testing.T) {
	const legacy = `{"turns":[{"request":"özet","reply":"Önceki yanıt."}],"previously_recommended_articles":[{"id":"PREVIOUS-1","title":"First","url":"https://example.com/1","description":"Konu: Bir."}],"rss_progress":{"scope":"all_unread","since":"2026-10-01T20:00:00Z","shown_keys":["url:https://example.com/1"],"total":3}}`
	for _, withAgent := range []bool{false, true} {
		t.Run(fmt.Sprintf("agent=%v", withAgent), func(t *testing.T) {
			var state ConversationState
			if err := json.Unmarshal([]byte(legacy), &state); err != nil {
				t.Fatal(err)
			}
			if state.AgentThreadID != "" || state.AgentTokenUsage != nil || len(state.Turns) != 1 || len(state.Articles) != 1 || state.RSS == nil || state.RSS.Total != 3 {
				t.Fatalf("legacy history not loaded: %+v", state)
			}
			if withAgent {
				state.AgentThreadID = "thread-123"
				state.AgentTokenUsage = &AgentTokenUsage{Input: 10, Output: 5, Total: 15}
			}
			before, err := json.Marshal(state)
			if err != nil {
				t.Fatal(err)
			}
			var expected ConversationState
			if err := json.Unmarshal(before, &expected); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 20; i++ {
				state.Remember(fmt.Sprint(i), "Agent reply <https://example.com/new|New>", "AGENT")
			}
			if len(state.Turns) != 12 || state.Turns[0].Request != "8" || state.Turns[11].Request != "19" {
				t.Fatalf("wrong retained exchanges: %+v", state.Turns)
			}
			expected.Turns = state.Turns
			encoded, err := json.Marshal(state)
			if err != nil {
				t.Fatal(err)
			}
			var restored ConversationState
			if err := json.Unmarshal(encoded, &restored); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(restored, expected) {
				t.Fatalf("persisted state changed: got %+v, want %+v", restored, expected)
			}
		})
	}
}

func TestConversationRememberBoundsUnicodeMessages(t *testing.T) {
	var state ConversationState
	state.Remember(strings.Repeat("ş", 3001), strings.Repeat("📰", 10001), "AGENT")
	turn := state.Turns[0]
	const suffix = "\n[Geçmiş mesaj kısaltıldı.]"
	if turn.Request != strings.Repeat("ş", 3000)+suffix || turn.Reply != strings.Repeat("📰", 10000)+suffix || !utf8.ValidString(turn.Request) || !utf8.ValidString(turn.Reply) {
		t.Fatal("history messages were not safely bounded")
	}
	state.Remember("Kısa istek", "Kısa yanıt", "AGENT")
	if state.Turns[1] != (ConversationExchange{Request: "Kısa istek", Reply: "Kısa yanıt"}) {
		t.Fatal("short messages were altered")
	}
}
