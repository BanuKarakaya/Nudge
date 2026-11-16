package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestWorkspaceLinkToolBindsRecipientAndDeduplicatesDelivery(t *testing.T) {
	calls := 0
	base := NewAgentTools(nil, nil, nil, 1, time.Now(), time.UTC)
	tools := WithWorkspaceTools(base, func(context.Context) error { calls++; return nil })
	for _, raw := range []string{`{"user_id":2}`, `{"team":"OTHER"}`, `{"url":"https://attacker.test"}`, `null`, `[]`, `""`, `{}`} {
		_, err := tools.Call(context.Background(), "workspace_connect", json.RawMessage(raw))
		if (err == nil) != (raw == `{}`) {
			t.Fatal("invalid argument acceptance", raw, err)
		}
	}
	var wg sync.WaitGroup
	for n := 0; n < 8; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, err := tools.Call(context.Background(), "workspace_connect", json.RawMessage(`{}`))
			if err != nil || !strings.Contains(out, `"connection_link_sent_privately":true`) || strings.Contains(out, "token=") {
				t.Error("incorrect private delivery result", out, err)
			}
		}()
	}
	wg.Wait()
	if calls != 1 {
		t.Fatal("duplicate DM", calls)
	}
	if ToolMutates(tools, "workspace_connect") {
		t.Fatal("panel link incorrectly requires provider write approval")
	}
	if len(WithWorkspaceTools(base, nil).Definitions()) != len(base.Definitions()) {
		t.Fatal("disabled portal exposed tool")
	}
}
func TestWorkspaceLinkToolDoesNotClaimFailedDelivery(t *testing.T) {
	tools := WithWorkspaceTools(NewAgentTools(nil, nil, nil, 1, time.Now(), time.UTC), func(context.Context) error { return errors.New("PRIVATE_TOKEN") })
	out, err := tools.Call(context.Background(), "workspace_connect", json.RawMessage(`{}`))
	code, msg := AgentToolFailure(err)
	if out != "" || !errors.Is(err, ErrWorkspaceLinkDelivery) || code != "workspace_link_failed" || strings.Contains(msg+err.Error(), "PRIVATE") {
		t.Fatal("unsafe delivery failure", out, err, code)
	}
}
