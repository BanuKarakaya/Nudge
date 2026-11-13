package codex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"path/filepath"
	"strconv"
	"time"

	"nudge/internal/ai"
	"nudge/internal/analytics"
	"nudge/internal/coding"
	"nudge/internal/service"
	"nudge/internal/workspace"
)

type tenantMembers interface {
	Member(context.Context, int64) (workspace.Member, error)
}

// TenantRuntime resolves identity and credentials for EVERY invocation (including
// scheduled digests and merge-intent checks). It never reads global model keys.
type TenantRuntime struct {
	store              workspace.AIStore
	members            tenantMembers
	base               Config
	slots, intentSlots chan struct{}
}

func NewTenantRuntime(store workspace.AIStore, members tenantMembers, binary, stateDir string) (*TenantRuntime, error) {
	if store == nil || members == nil || !filepath.IsAbs(stateDir) {
		return nil, errors.New("workspace AI runtime configuration is invalid")
	}
	if err := removeLegacyState(stateDir); err != nil {
		return nil, err
	}
	return &TenantRuntime{store: store, members: members, base: Config{Binary: binary, StateDir: stateDir}, slots: make(chan struct{}, 4), intentSlots: make(chan struct{}, 4)}, nil
}
func (t *TenantRuntime) configured(ctx context.Context, user int64, key service.ConversationKey) (*Runtime, workspace.AICredentials, error) {
	m, err := t.members.Member(ctx, user)
	if err != nil || m.Team != key.Team || m.SlackUser != key.User {
		return nil, workspace.AICredentials{}, workspace.ErrDenied
	}
	c, err := t.store.AICredentials(ctx, user)
	if err != nil {
		return nil, c, err
	}
	if !c.Ready() || c.APIKey == "" {
		return nil, c, workspace.ErrAINotConfigured
	}
	base := workspace.ProviderURL(c.Provider)
	if base == "" {
		return nil, c, workspace.ErrAINotConfigured
	}
	cfg := t.base
	cfg.ToolModels = c.ToolModels
	cfg.Model = c.Model
	cfg.ComplexModel = c.ComplexModel
	if cfg.ComplexModel == cfg.Model {
		cfg.ComplexModel = ""
	}
	cfg.APIKey = c.APIKey
	cfg.BaseURL = base
	cfg.ModelCatalogURL = base + "/models"
	// Changing credentials/provider starts a separate native state directory. Only
	// explicitly delivered, user-bound history is carried across that boundary.
	sum := sha256.Sum256([]byte(m.Team + ":" + c.Revision))
	cfg.StateDir = filepath.Join(cfg.StateDir, "workspaces", hex.EncodeToString(sum[:]), "users", nativeUserDir(user))
	r, err := New(cfg)
	if err != nil {
		return nil, c, errors.New("workspace AI runtime could not start")
	}
	r.slots = t.slots
	r.intentSlots = t.intentSlots
	return r, c, nil
}
func (t *TenantRuntime) Run(ctx context.Context, req service.AgentRequest) (service.AgentResult, error) {
	if req.Key.Team == "scheduled" && req.Key.Channel == "feedbin_digest" && req.Key.User == strconv.FormatInt(req.UserID, 10) {
		m, err := t.members.Member(ctx, req.UserID)
		if err != nil {
			return service.AgentResult{}, workspace.ErrDenied
		}
		req.Key.Team = m.Team
		req.Key.User = m.SlackUser
	}
	r, c, err := t.configured(ctx, req.UserID, req.Key)
	if err != nil {
		return service.AgentResult{}, err
	}
	if budgets, ok := t.store.(interface {
		BeginBudget(context.Context, int64) (string, error)
		FinishBudget(context.Context, string, int) error
	}); ok {
		id, err := budgets.BeginBudget(ctx, req.UserID)
		if err != nil {
			return service.AgentResult{}, err
		}
		usage := analytics.UsageTrackerFromContext(ctx)
		if usage == nil {
			usage = analytics.NewUsageTracker()
			ctx = analytics.WithUsageTracker(ctx, usage)
		}
		before := len(usage.Snapshot())
		defer func() {
			tokens := 0
			for _, u := range usage.Snapshot()[before:] {
				tokens += u.TotalTokens
			}
			finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			_ = budgets.FinishBudget(finish, id, tokens)
		}()
	}
	if req.History.AIRevision != c.Revision {
		req.ThreadID = ""
		req.History.AgentThreadID = ""
		req.History.AgentTokenUsage = nil
	}
	result, err := r.Run(ctx, req)
	result.AIRevision = c.Revision
	return result, err
}
func (t *TenantRuntime) ResolveMergeIntent(ctx context.Context, req service.MergeIntentRequest) (coding.MergeIntent, error) {
	r, _, err := t.configured(ctx, req.UserID, req.Key)
	if err != nil {
		return coding.MergeIntent{}, coding.ErrMergeIntentUnavailable
	}
	return r.ResolveMergeIntent(ctx, req)
}
func (t *TenantRuntime) WebForUser(ctx context.Context, user int64) service.WebAssistant {
	c, err := t.store.AICredentials(ctx, user)
	if err != nil || !c.HasSearchKey || c.SearchAPIKey == "" || c.SearchModel == "" {
		return nil
	}
	return ai.GeminiClient{APIKey: c.SearchAPIKey, Model: c.SearchModel, HTTPClient: &http.Client{Timeout: 60 * time.Second}}
}
