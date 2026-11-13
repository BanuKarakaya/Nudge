package codex

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"nudge/internal/coding"
	"nudge/internal/service"
)

func TestMergeIntentTransportIsIsolatedAndDoesNotWaitForAgentSlot(t *testing.T) {
	r := helperRuntime(t, "merge-intent")
	r.config.ComplexModel = "must-not-be-offered"
	// All main slots can be occupied by agents waiting for merge authorization.
	for n := 0; n < cap(r.slots); n++ {
		r.slots <- struct{}{}
	}
	got, err := r.ResolveMergeIntent(context.Background(), service.MergeIntentRequest{
		Key: runtimeTestRequest().Key, Current: "bunu mergele",
		Turns: []service.ConversationExchange{{Request: "https://github.com/owner/repo/pull/12", Reply: "PR açıldı"}},
	})
	if err != nil || got.Action != "merge" || got.Number != 12 || len(r.intentSlots) != 0 {
		t.Fatal(got, err)
	}
	if len(r.slots) != cap(r.slots) {
		t.Fatal("resolver modified main concurrency slots")
	}
}

func TestMergeIntentDecoderRejectsMalformedAndUngroundedTargets(t *testing.T) {
	req := service.MergeIntentRequest{Current: "bunu mergele", Turns: []service.ConversationExchange{{Request: "<https://github.com/owner/repo/pull/12|PR #12>, feature/Login → development"}}}
	for _, tc := range []struct {
		text string
		want string
	}{
		{`{"action":"merge","provider":"github","repository":"owner/repo","number":12}`, "merge"},
		{`{"action":"merge","provider":"github","repository":"owner/repo","number":1}`, "clarify"},
		{`{"action":"merge","provider":"gitlab","repository":"owner/repo","number":12}`, "clarify"},
		{`{"action":"merge","provider":"github","repository":"owner/other","number":12}`, "clarify"},
		{`{"action":"merge","provider":"github","repository":"owner/repo","source_branch":"feature/Login","target_branch":"development"}`, "merge"},
		{`{"action":"merge","provider":"github","repository":"owner/repo","source_branch":"Login","target_branch":"development"}`, "clarify"},
		{`{"action":"merge","provider":"github","repository":"owner/repo","source_branch":"feature/Login","target_branch":"main"}`, "clarify"},
		{`{"action":"none"}`, "none"},
		{`{"action":"clarify"}`, "clarify"},
		{`{"action":"merge","provider":"github","repository":"owner/repo","number":12,"target_branch":"development"}`, "error"},
		{`{"action":"merge","provider":"github","repository":"owner/repo","number":12,"authorized":true}`, "error"},
		{"```json\n{\"action\":\"none\"}\n```", "error"},
		{`{"action":"none"} {"action":"merge"}`, "error"},
		{`{"action":"none","number":12}`, "error"},
		{`{"action":"merge"}`, "error"},
	} {
		got, err := decodeMergeIntent(tc.text, req)
		if tc.want == "error" {
			if !errors.Is(err, coding.ErrMergeIntentUnavailable) {
				t.Error(tc.text, got, err)
			}
		} else if err != nil || got.Action != tc.want {
			t.Error(tc.text, got, err)
		}
	}
	intent := coding.MergeIntent{Action: "merge", Provider: "gitlab", Repository: "team/sub/project", Number: 9}
	encoded, _ := json.Marshal(intent)
	req = service.MergeIntentRequest{Current: "şunu birleştirebilir misin: https://gitlab.com/team/sub/project/-/merge_requests/9"}
	if got, err := decodeMergeIntent(string(encoded), req); err != nil || got != intent {
		t.Fatal("GitLab subgroup target lost", got, err)
	}
}

func TestAssistantTextAndUnrelatedPRNumbersCannotAuthorizeMerge(t *testing.T) {
	intent := coding.MergeIntent{Action: "merge", Provider: "github", Repository: "owner/repo", Number: 12}
	for _, req := range []service.MergeIntentRequest{
		{Current: "bunu mergele", Turns: []service.ConversationExchange{{Reply: "https://github.com/owner/repo/pull/12"}}},
		{Current: "GitHub owner/repo merge et", Turns: []service.ConversationExchange{{Request: "other/repo PR #12"}}},
	} {
		if groundedMergeTarget(intent, req) {
			t.Fatal("untrusted/unrelated target accepted")
		}
	}
}
