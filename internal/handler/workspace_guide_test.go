package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"nudge/internal/coding"
	"nudge/internal/service"
)

func TestWorkspaceGuideUsesActualRoleAndInstallation(t *testing.T) {
	h, _, _ := workspaceFixture(t)
	for _, id := range []int64{1, 2} {
		g, err := h.Guide(context.Background(), id, "T")
		if err != nil || g.Manager != (id == 1) {
			t.Fatal("incorrect role", err)
		}
		for _, f := range g.Features {
			if f.Topic == "raindrop" && f.Available {
				t.Fatal("unconfigured provider advertised")
			}
			if f.Topic == "github" && !f.Available {
				t.Fatal("configured provider hidden")
			}
			if f.Topic == "reminders" && !f.ManagerOnly {
				t.Fatal("workspace setting described as personal")
			}
		}
	}
	if _, err := h.Guide(context.Background(), 1, "OTHER"); err == nil {
		t.Fatal("cross-workspace role read")
	}
	if _, err := h.Guide(context.Background(), 999, "T"); err == nil {
		t.Fatal("unknown user")
	}
}

// This verifies handler plumbing, not language comprehension. Real model
// acceptance cases are documented separately in docs/natural-language-flows.md.
func TestToolCatalogChangesRefreshExistingConversation(t *testing.T) {
	store := make(conversationTestStore)
	sender := &conversationSender{}
	revision := "a"
	calls := 0
	h := NewSlackEventHandler("", conversationInstallations{}, nil, nil, agentFunc(func(_ context.Context, r service.AgentRequest) (service.AgentResult, error) {
		calls++
		wantResume := calls == 2 || calls == 5
		if (r.ThreadID != "") != wantResume {
			t.Fatal("stale catalog resumed or stable catalog reset", calls, r.ThreadID)
		}
		if len(r.History.Turns) != calls-1 {
			t.Fatal("delivered conversation lost", calls)
		}
		return service.AgentResult{ThreadID: fmt.Sprint("native-", calls), Text: "done"}, nil
	}), nil, store, sender, time.UTC)
	h.WithToolModule(func(coding.Identity) []service.RegisteredTool {
		if revision == "" {
			return nil
		}
		return []service.RegisteredTool{{Definition: service.ToolDefinition{Name: "ext_company_lookup", Description: revision, InputSchema: map[string]any{"type": "object"}}, ReadOnly: true, Run: func(context.Context, json.RawMessage) (string, error) {
			return "", errors.New("must not execute during catalog refresh")
		}}}
	})
	for i, rev := range []string{"a", "a", "b", "", ""} {
		revision = rev
		e := conversationEvent("şirket uygulamasında neler yapabilirsin?")
		e.Event.TS = fmt.Sprintf("%d.0", i+1)
		e.Event.ThreadTS = "1.0"
		h.replyWithSummary(e)
	}
	if calls != 5 || len(sender.messages) != 5 {
		t.Fatal("flow did not complete")
	}
	for _, msg := range sender.messages {
		if strings.Contains(msg, "token=") {
			t.Fatal("private link exposed")
		}
	}
}
