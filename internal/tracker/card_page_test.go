package tracker

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"nudge/internal/workspace"
)

type cardPageProvider struct {
	*fakeProvider
	optionsErr, itemsErr error
	listCalls, itemCalls int
	cursor, board        string
	next                 string
}

func (p *cardPageProvider) CreateOptions(_ context.Context, _ string, r Resource, _ string) (CreateOptions, error) {
	p.listCalls++
	p.board = r.ID
	return CreateOptions{Options: []Option{{ID: "doing", Name: "Doing"}, {ID: "todo", Name: "To do"}}}, p.optionsErr
}
func (p *cardPageProvider) Items(_ context.Context, _ string, r Resource, cursor string) (Page, error) {
	p.itemCalls++
	p.cursor = cursor
	p.board = r.ID
	return Page{Items: []Item{{ID: "mine", Title: "My task", StatusID: "doing", MemberIDs: []string{"account"}}, {ID: "other", StatusID: "doing", MemberIDs: []string{"other"}}, {ID: "unassigned", StatusID: "todo"}}, NextCursor: p.next}, p.itemsErr
}
func TestCardPageContainsListAndAssignmentWithoutCardReads(t *testing.T) {
	s, _, base := connectFixture(t, "trello")
	p := &cardPageProvider{fakeProvider: base}
	s.provider = p
	ctx := context.Background()
	id := Identity{1, "T"}
	page, err := s.Cards(ctx, id, "selected", "")
	if err != nil || page.BoardID != "selected" || page.CurrentUserID != "account" || !page.Complete || len(page.Lists) != 2 || len(page.Items) != 3 {
		t.Fatalf("page %+v err %v", page, err)
	}
	if !page.Items[0].AssignedToCurrentUser || page.Items[1].AssignedToCurrentUser || page.Items[2].AssignedToCurrentUser || page.Items[0].ListName != "Doing" || page.Items[2].ListName != "To do" {
		t.Fatal("incorrect assignment/list mapping", page)
	}
	if p.listCalls != 1 || p.itemCalls != 1 || base.reads != 0 {
		t.Fatal("unexpected detail reads")
	}
	raw, _ := json.Marshal(page)
	if strings.Contains(string(raw), "secret") {
		t.Fatal("credentials leaked")
	}
	p.next = "next-page"
	page, err = s.Cards(ctx, id, "selected", "previous-page")
	if err != nil || page.Complete || page.NextCursor != "next-page" || p.cursor != "previous-page" {
		t.Fatal("pagination lost", page, err)
	}
	p.optionsErr = errors.New("unavailable")
	page, err = s.Cards(ctx, id, "selected", "")
	if err == nil || len(page.Items) > 0 || p.itemCalls != 2 {
		t.Fatal("failed list read presented as empty data")
	}
	p.optionsErr = nil
	p.itemsErr = errors.New("unavailable")
	page, err = s.Cards(ctx, id, "selected", "")
	if err == nil || len(page.Lists) > 0 || page.Complete {
		t.Fatal("failed card read presented as complete")
	}
}
func TestCardPagePreservesWorkspaceAndPersonalAccess(t *testing.T) {
	s, _, base := connectFixture(t, "trello")
	p := &cardPageProvider{fakeProvider: base}
	s.provider = p
	ctx := context.Background()
	for _, input := range []struct {
		id               Identity
		resource, cursor string
	}{{Identity{1, "WRONG"}, "selected", ""}, {Identity{2, "T"}, "selected", ""}, {Identity{1, "T"}, "doing", ""}, {Identity{1, "T"}, "other", ""}, {Identity{1, "T"}, "selected", strings.Repeat("a", 4097)}} {
		if _, err := s.Cards(ctx, input.id, input.resource, input.cursor); err == nil {
			t.Fatal("invalid access accepted", input)
		}
	}
	if p.listCalls != 0 || p.itemCalls != 0 {
		t.Fatal("read before authorization")
	}
	policy := &testWorkspacePolicy{scope: workspace.Scope{Enabled: false, Resources: []workspace.Resource{{ID: "selected"}}}}
	s.WithWorkspacePolicy(policy)
	if _, err := s.Cards(ctx, Identity{1, "T"}, "selected", ""); !errors.Is(err, ErrDenied) {
		t.Fatal("disabled workspace bypassed", err)
	}
	if p.listCalls != 0 || p.itemCalls != 0 {
		t.Fatal("disabled workspace read")
	}
}
