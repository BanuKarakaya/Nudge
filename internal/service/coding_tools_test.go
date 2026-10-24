package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"nudge/internal/coding"
)

type codingTestStore struct {
	coding.Store
	task coding.Task
}

func (*codingTestStore) SlackTeam(_ context.Context, id int64) (string, error) {
	if id == 7 {
		return "T", nil
	}
	return "", coding.ErrDenied
}

func (*codingTestStore) WithConnection(_ context.Context, id int64, fn func(*coding.Connection) error) error {
	if id != 7 {
		return coding.ErrConnect
	}
	return fn(&coding.Connection{UserID: id, Team: "T", Credentials: coding.Credentials{Access: "ghu_private"}})
}
func (s *codingTestStore) CreateDraft(_ context.Context, t coding.Task) (coding.Task, error) {
	s.task = t
	return t, nil
}

type codingTestProvider struct{ coding.Provider }

func (codingTestProvider) Repositories(context.Context, string) ([]coding.Repository, error) {
	return []coding.Repository{{ID: 10, FullName: "owner/repo", DefaultBranch: "master", InstallationID: 20}}, nil
}

func TestCodingToolsRejectIdentityInjectionAndExposeNoConnectionLink(t *testing.T) {
	store := &codingTestStore{}
	s, err := coding.New(store, codingTestProvider{})
	if err != nil {
		t.Fatal(err)
	}
	base := NewAgentTools(nil, nil, nil, 7, time.Now(), time.UTC)
	calls := 0
	tools := NewCodingTools(base, s, coding.Identity{UserID: 7, Team: "T", Channel: "C", Thread: "1", Event: "E", Request: "original signed Slack request"}, func(context.Context) error { calls++; return nil })
	for _, args := range []string{`{"user_id":8}`, `{"team":"OTHER"}`, `null`, `{} {}`, `{"repository":"owner/repo","repository":"owner/other"}`, `{"repository":null}`} {
		if _, err := tools.Call(context.Background(), "repository_task_prepare", json.RawMessage(args)); err == nil {
			t.Fatalf("accepted %s", args)
		}
	}
	for range 2 {
		result, err := tools.Call(context.Background(), "github_connect", json.RawMessage(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(result, "https://") || strings.Contains(result, "ghu_") {
			t.Fatal("private capability entered model context")
		}
	}
	if calls != 1 {
		t.Fatal("duplicated private connection link in one turn")
	}
	result, err := tools.Call(context.Background(), "repository_task_prepare", json.RawMessage(`{"repository":"owner/repo"}`))
	if err != nil {
		t.Fatal(err)
	}
	if store.task.Identity.UserID != 7 || store.task.Instruction != "original signed Slack request" || store.task.Status != "draft" {
		t.Fatal("identity/instruction was model-selected")
	}
	if !strings.Contains(result, `"execution_available":false`) || strings.Contains(result, "ghu_private") {
		t.Fatal("misleading execution claim or secret leak")
	}
	if _, err := tools.Call(context.Background(), "rss_inventory", json.RawMessage(`{"scope":"all_unread"}`)); err == nil {
		t.Fatal("missing RSS connection should still fail")
	}
}

func TestCodingToolsRejectMismatchedWorkspace(t *testing.T) {
	s, _ := coding.New(&codingTestStore{}, codingTestProvider{})
	tools := NewCodingTools(NewAgentTools(nil, nil, nil, 7, time.Now(), nil), s, coding.Identity{UserID: 7, Team: "OTHER"}, func(context.Context) error { t.Fatal("sent link to another workspace"); return nil })
	for _, name := range []string{"github_connect", "repositories_list", "repository_task_prepare", "repository_task_cancel"} {
		if _, err := tools.Call(context.Background(), name, json.RawMessage(`{}`)); err == nil {
			t.Fatalf("allowed %s", name)
		}
	}
}
