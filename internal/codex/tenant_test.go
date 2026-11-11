package codex

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"nudge/internal/ai"
	"nudge/internal/coding"
	"nudge/internal/service"
	"nudge/internal/workspace"
)

type tenantFixture struct {
	members map[int64]workspace.Member
	configs map[string]workspace.AICredentials
	reads   int
	err     error
}

func (f *tenantFixture) Member(_ context.Context, id int64) (workspace.Member, error) {
	m, ok := f.members[id]
	if !ok {
		return m, workspace.ErrDenied
	}
	return m, nil
}
func (f *tenantFixture) AISettings(ctx context.Context, id int64) (workspace.AISettings, error) {
	c, e := f.AICredentials(ctx, id)
	return c.AISettings, e
}
func (f *tenantFixture) AICredentials(ctx context.Context, id int64) (workspace.AICredentials, error) {
	f.reads++
	if f.err != nil {
		return workspace.AICredentials{}, f.err
	}
	m, e := f.Member(ctx, id)
	if e != nil {
		return workspace.AICredentials{}, e
	}
	c, ok := f.configs[m.Team]
	if !ok {
		return c, workspace.ErrAINotConfigured
	}
	return c, nil
}
func (f *tenantFixture) SaveAI(context.Context, int64, workspace.AICredentials, string) error {
	return errors.New("not used")
}
func tenantRuntimeFixture(t *testing.T, mode string) (*TenantRuntime, *tenantFixture) {
	t.Helper()
	r := helperRuntime(t, mode)
	f := &tenantFixture{members: map[int64]workspace.Member{1: {UserID: 1, Team: "team", SlackUser: "user"}, 2: {UserID: 2, Team: "other", SlackUser: "other-user"}}, configs: map[string]workspace.AICredentials{
		"team":  {AISettings: workspace.AISettings{Provider: "openrouter", Model: "vendor/one", HasKey: true, Revision: "rev-one"}, APIKey: "team-one-key"},
		"other": {AISettings: workspace.AISettings{Provider: "openai", Model: "gpt-test", HasKey: true, Revision: "rev-two"}, APIKey: "team-two-key"},
	}}
	runtime, e := NewTenantRuntime(f, f, r.config.Binary, r.config.StateDir)
	if e != nil {
		t.Fatal(e)
	}
	return runtime, f
}
func observeTenant(t *testing.T, r *TenantRuntime, req service.AgentRequest) (service.AgentResult, helperObservation) {
	t.Helper()
	result, e := r.Run(context.Background(), req)
	if e != nil {
		t.Fatal(e)
	}
	var got helperObservation
	if json.Unmarshal([]byte(result.Text), &got) != nil {
		t.Fatal("invalid helper output")
	}
	return result, got
}
func TestTenantRuntimeCredentialsIsolationRotationAndNoFallback(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "operator-key")
	t.Setenv("OPENAI_API_KEY", "operator-openai-key")
	r, f := tenantRuntimeFixture(t, "observe")
	req := runtimeTestRequest()
	req.UserID = 1
	req.ThreadID = "old-thread"
	result, first := observeTenant(t, r, req)
	if first.Env["NUDGY_MODEL_API_KEY"] != "team-one-key" || first.Env["OPENAI_API_KEY"] != "" || string(first.Start["model"]) != `"vendor/one"` || first.Method != "thread/start" || result.AIRevision != "rev-one" {
		t.Fatal("workspace configuration not applied")
	}
	req.ThreadID = "thread-1"
	req.History.AIRevision = result.AIRevision
	_, resumed := observeTenant(t, r, req)
	if resumed.Method != "thread/resume" {
		t.Fatal("same configuration failed to resume")
	}
	c := f.configs["team"]
	c.APIKey = "rotated-key"
	c.Revision = "rotated"
	f.configs["team"] = c
	_, rotated := observeTenant(t, r, req)
	if rotated.Method != "thread/start" || rotated.Env["CODEX_HOME"] == first.Env["CODEX_HOME"] || rotated.Env["NUDGY_MODEL_API_KEY"] != "rotated-key" {
		t.Fatal("rotation reused old credentials/state")
	}
	req.UserID = 2
	req.Key.Team = "other"
	req.Key.User = "other-user"
	_, other := observeTenant(t, r, req)
	if other.Env["NUDGY_MODEL_API_KEY"] != "team-two-key" || other.Env["CODEX_HOME"] == rotated.Env["CODEX_HOME"] {
		t.Fatal("cross-team credential/state reuse")
	}
	req.Key.Team = "team"
	before := f.reads
	if _, e := r.Run(context.Background(), req); !errors.Is(e, workspace.ErrDenied) || f.reads != before {
		t.Fatal("forged identity read credentials")
	}
	req.UserID = 1
	req.Key.User = "user"
	delete(f.configs, "team")
	if _, e := r.Run(context.Background(), req); !errors.Is(e, workspace.ErrAINotConfigured) {
		t.Fatal("missing key fell back")
	}
	f.err = errors.New("db offline")
	if _, e := r.Run(context.Background(), req); e == nil {
		t.Fatal("storage failure fell back")
	}
}
func TestTenantRuntimeScheduledAndSearchCredentials(t *testing.T) {
	r, f := tenantRuntimeFixture(t, "observe")
	req := runtimeTestRequest()
	req.UserID = 2
	req.Key = service.ConversationKey{Team: "scheduled", User: "2", Channel: "feedbin_digest", Thread: "window"}
	_, got := observeTenant(t, r, req)
	if got.Env["NUDGY_MODEL_API_KEY"] != "team-two-key" {
		t.Fatal("scheduled digest not workspace billed")
	}
	if r.WebForUser(context.Background(), 1) != nil {
		t.Fatal("global search fallback")
	}
	c := f.configs["team"]
	c.SearchAPIKey = "team-google-key"
	c.SearchModel = "gemini-test"
	c.HasSearchKey = true
	f.configs["team"] = c
	search, ok := r.WebForUser(context.Background(), 1).(ai.GeminiClient)
	if !ok || search.APIKey != "team-google-key" || search.Model != "gemini-test" {
		t.Fatal("search config missing")
	}
	if r.WebForUser(context.Background(), 2) != nil {
		t.Fatal("search key crossed workspace")
	}
}
func TestTenantMergeResolverUsesWorkspaceAndRejectsMissingKey(t *testing.T) {
	r, f := tenantRuntimeFixture(t, "merge-intent")
	req := service.MergeIntentRequest{UserID: 1, Key: runtimeTestRequest().Key, Current: "bunu mergele", Turns: []service.ConversationExchange{{Reply: "https://github.com/owner/repo/pull/12"}}}
	// Resolver must not deadlock behind its own parent agent slot.
	for i := 0; i < cap(r.slots); i++ {
		r.slots <- struct{}{}
	}
	result, e := r.ResolveMergeIntent(context.Background(), req)
	if e != nil || result.Action != "merge" {
		t.Fatal("workspace resolver failed", e)
	}
	delete(f.configs, "team")
	if _, e = r.ResolveMergeIntent(context.Background(), req); !errors.Is(e, coding.ErrMergeIntentUnavailable) {
		t.Fatal("merge resolver used fallback")
	}
}
