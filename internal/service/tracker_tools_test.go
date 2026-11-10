package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"nudge/internal/tracker"
)

type trackerToolStore struct {
	tracker.Store
	connection tracker.Connection
}

func (*trackerToolStore) SlackTeam(context.Context, int64) (string, error) { return "T", nil }
func (s *trackerToolStore) WithConnection(_ context.Context, _ string, _ int64, fn func(*tracker.Connection) error) error {
	return fn(&s.connection)
}

type trackerToolProvider struct {
	tracker.Client
	creates, moves int
	input          tracker.CreateInput
	move           tracker.MoveInput
}

func (*trackerToolProvider) Account(context.Context, string) (tracker.Account, error) {
	return tracker.Account{ID: "account", Name: "Name"}, nil
}
func (*trackerToolProvider) Resources(context.Context, string) ([]tracker.Resource, error) {
	return []tracker.Resource{{ID: "selected"}}, nil
}
func (p *trackerToolProvider) Create(_ context.Context, _ string, _ tracker.Resource, input tracker.CreateInput) (tracker.MutationResult, error) {
	p.creates++
	p.input = input
	return tracker.MutationResult{Status: "created", Item: tracker.Item{ID: "item"}}, nil
}
func (p *trackerToolProvider) Move(_ context.Context, _ string, _ tracker.Resource, input tracker.MoveInput) (tracker.MutationResult, error) {
	p.moves++
	p.move = input
	return tracker.MutationResult{Status: "moved", Item: tracker.Item{ID: "item"}}, nil
}

type trackerBaseTools struct{}

func (trackerBaseTools) Definitions() []ToolDefinition { return nil }
func (trackerBaseTools) Call(context.Context, string, json.RawMessage) (string, error) {
	return "base", nil
}

func TestTrackerWriteToolDispatchAndSchema(t *testing.T) {
	for _, kind := range []string{"jira", "trello"} {
		t.Run(kind, func(t *testing.T) {
			store := &trackerToolStore{connection: tracker.Connection{Identity: tracker.Identity{UserID: 1, Team: "T"}, Provider: kind, Account: tracker.Account{ID: "account"}, Credentials: tracker.Credentials{Access: "secret", Expires: time.Now().Add(time.Hour), Scopes: "write:jira-work write:board:trello"}, Selected: []string{"selected"}}}
			provider := &trackerToolProvider{Client: tracker.Client{Provider: kind}}
			svc := tracker.New(store, provider)
			tools := NewTrackerTools(trackerBaseTools{}, svc, tracker.Identity{UserID: 1, Team: "T"}, nil, "signed-event")
			createName, moveName := kind+"_issue_create", kind+"_issue_move"
			create := `{"resource_id":"selected","issue_type_id":"type","title":"Task","description":"Description","fields_json":"{\"customfield_1\":\"value\"}"}`
			move := `{"resource_id":"selected","item_id":"item","transition_id":"testing","expected_status_id":"doing","fields_json":"{}"}`
			if kind == "trello" {
				createName, moveName = "trello_card_create", "trello_card_move"
				create = `{"resource_id":"selected","list_id":"list","title":"Task","description":"Description"}`
				move = `{"resource_id":"selected","item_id":"item","list_id":"testing","expected_status_id":"doing"}`
			}
			for range 2 {
				result, err := tools.Call(context.Background(), createName, json.RawMessage(create))
				if err != nil || !strings.Contains(result, `"status":"created"`) || strings.Contains(result, "secret") {
					t.Fatal(result, err)
				}
				result, err = tools.Call(context.Background(), moveName, json.RawMessage(move))
				if err != nil || !strings.Contains(result, `"status":"moved"`) {
					t.Fatal(result, err)
				}
			}
			if provider.creates != 1 || provider.moves != 1 || provider.input.Title != "Task" || provider.move.ExpectedStatus != "doing" || provider.move.Destination != "testing" {
				t.Fatal(provider)
			}
			if kind == "jira" && provider.input.Fields["customfield_1"] != "value" {
				t.Fatal(provider.input)
			}
			invalid := strings.TrimSuffix(create, "}") + `,"user_id":2}`
			if _, err := tools.Call(context.Background(), createName, json.RawMessage(invalid)); err == nil {
				t.Fatal("model identity injection")
			}
			noEvent := NewTrackerTools(trackerBaseTools{}, svc, tracker.Identity{UserID: 1, Team: "T"}, nil)
			if _, err := noEvent.Call(context.Background(), createName, json.RawMessage(create)); !errors.Is(err, tracker.ErrArguments) {
				t.Fatal("unbound write", err)
			}
			if got, err := tools.Call(context.Background(), "feed_list", json.RawMessage(`{}`)); err != nil || got != "base" {
				t.Fatal(got, err)
			}
		})
	}
}
func TestTrackerFailureMessagesDoNotSuggestUnsafeRetry(t *testing.T) {
	code, message := AgentToolFailure(tracker.ErrOutcomeUnknown)
	if code != "tracker_outcome_unknown" || !strings.Contains(message, "tekrarlama") {
		t.Fatal(code, message)
	}
	code, _ = AgentToolFailure(tracker.ErrWriteScope)
	if code != "tracker_write_scope_required" {
		t.Fatal(code)
	}
}

type managementToolProvider struct {
	*trackerToolProvider
	changes int
	changed tracker.ChangeInput
}

func (p *managementToolProvider) Lookup(context.Context, string, tracker.Resource, tracker.LookupInput) (any, error) {
	return []tracker.Member{{ID: "banu"}, {ID: "latif"}}, nil
}
func (p *managementToolProvider) Change(_ context.Context, _ string, _ tracker.Resource, in tracker.ChangeInput) (tracker.MutationResult, error) {
	p.changes++
	p.changed = in
	return tracker.MutationResult{Status: "updated", Item: tracker.Item{ID: "card"}}, nil
}
func TestManagementToolsAcceptTwoMembersAndExcludeDelete(t *testing.T) {
	store := &trackerToolStore{connection: tracker.Connection{Identity: tracker.Identity{UserID: 1, Team: "T"}, Provider: "trello", Account: tracker.Account{ID: "account"}, Credentials: tracker.Credentials{Access: "secret", Expires: time.Now().Add(time.Hour), Scopes: "write:board:trello"}, Selected: []string{"selected"}}}
	p := &managementToolProvider{trackerToolProvider: &trackerToolProvider{Client: tracker.Client{Provider: "trello"}}}
	tools := NewTrackerTools(trackerBaseTools{}, tracker.New(store, p), tracker.Identity{UserID: 1, Team: "T"}, nil, "event")
	for range 2 {
		if _, err := tools.Call(context.Background(), "trello_card_assign", json.RawMessage(`{"resource_id":"selected","item_id":"card","mode":"add","member_ids":["banu","latif"]}`)); err != nil {
			t.Fatal(err)
		}
	}
	if p.changes != 1 || p.changed.Mode != "add" || len(p.changed.IDs) != 2 {
		t.Fatal(p.changed, p.changes)
	}
	for _, kind := range []string{"jira", "trello"} {
		for _, d := range trackerManagementDefinitions(kind) {
			if strings.Contains(d.Name, "delete") || strings.Contains(d.Name, "project_update") || strings.Contains(d.Name, "board_update") {
				t.Fatal("unrequested destructive/admin tool", d.Name)
			}
		}
	}
	if _, err := tools.Call(context.Background(), "trello_card_delete", json.RawMessage(`{}`)); !errors.Is(err, tracker.ErrArguments) {
		t.Fatal(err)
	}
	if _, err := tools.Call(context.Background(), "trello_card_assign", json.RawMessage(`{"resource_id":"selected","item_id":"card","mode":"add","member_ids":["banu"],"user_id":2}`)); err == nil {
		t.Fatal("identity injection")
	}
}
