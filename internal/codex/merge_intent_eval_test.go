package codex

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"nudge/internal/service"
)

// Opt in explicitly: this sends synthetic conversations to the configured model,
// incurs inference usage, and never supplies repository or merge action tools.
func TestLiveMergeIntentLanguage(t *testing.T) {
	if os.Getenv("NUDGE_MERGE_INTENT_EVAL") != "1" {
		t.Skip("set NUDGE_MERGE_INTENT_EVAL=1, OPENROUTER_API_KEY and CODEX_SIMPLE_MODEL")
	}
	r, err := New(Config{Model: os.Getenv("CODEX_SIMPLE_MODEL"), APIKey: os.Getenv("OPENROUTER_API_KEY"), StateDir: filepath.Join(t.TempDir(), "intent-eval"), Timeout: 40 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	pr := "https://github.com/owner/repo/pull/12"
	failed := []service.ConversationExchange{{Request: pr + " merge et", Reply: "PR #12 merge tamamlanamadı: " + pr + ". Kaynak feature/login, hedef development."}}
	for _, tc := range []struct {
		name, current, action string
		history               []service.ConversationExchange
	}{
		{"demonstrative", "bunu mergele", "merge", failed},
		{"natural_pr", "bu PR ı merge et", "merge", failed},
		{"polite_retry", "mergeleme işlemini tekrar dener misin", "merge", failed},
		{"contextual_retry", "tekrar dene", "merge", failed},
		{"english", "could you merge this PR please?", "merge", failed},
		{"gitlab", "bu MR'ı birleştirir misin", "merge", []service.ConversationExchange{{Reply: "https://gitlab.com/team/sub/project/-/merge_requests/9 açık."}}},
		{"branch_direction", "şu feature/login dalını development'a mergeleyebilir misin", "merge", []service.ConversationExchange{{Reply: "GitHub owner/repo dalları: feature/login, development, main."}}},
		{"changed_target", "bunu bu sefer main'e mergele", "merge", failed},
		{"negation", "bunu merge etme", "none", failed},
		{"review", "merge etmeden önce sadece incele", "none", failed},
		{"status", "merge oldu mu?", "none", failed},
		{"ack", "tamam", "none", failed},
		{"conditional", "testler geçerse bunu mergele", "none", failed},
		{"quoted", "Arkadaşım 'bunu mergele' yazmış, ne demek?", "none", failed},
		{"creation_retry", "tekrar dene", "none", []service.ConversationExchange{{Request: "sadece PR aç", Reply: "PR oluşturulamadı."}}},
		{"ambiguous", "bunu mergele", "clarify", []service.ConversationExchange{{Request: "PRları göster", Reply: pr + " ve https://github.com/owner/repo/pull/13 açık."}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			key := runtimeTestRequest().Key
			key.Thread = tc.name
			got, err := r.ResolveMergeIntent(context.Background(), service.MergeIntentRequest{Key: key, Current: tc.current, Turns: tc.history})
			targetOK := true
			if tc.action == "merge" {
				switch tc.name {
				case "gitlab":
					targetOK = got.Provider == "gitlab" && got.Repository == "team/sub/project" && got.Number == 9
				case "branch_direction", "changed_target":
					target := "development"
					if tc.name == "changed_target" {
						target = "main"
					}
					targetOK = got.Provider == "github" && got.Repository == "owner/repo" && got.Number == 0 && got.SourceBranch == "feature/login" && got.TargetBranch == target
				default:
					targetOK = got.Provider == "github" && got.Repository == "owner/repo" && got.Number == 12
				}
			}
			if err != nil || got.Action != tc.action || !targetOK {
				t.Errorf("action=%s provider=%s number=%d err=%v; want %s", got.Action, got.Provider, got.Number, err, tc.action)
			}
		})
	}
}
