package postgres

import (
	"context"
	"github.com/jackc/pgx/v5/pgxpool"
	"nudge/internal/coding"
	"testing"
)

func testBranchRulesDB(t *testing.T, db *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	s, err := NewBranchRuleStore(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = NewBranchRuleStore(ctx, db); err != nil {
		t.Fatal(err)
	}
	i := coding.Identity{UserID: 1, Team: "T"}
	check := func(want string) {
		t.Helper()
		got, err := s.BranchTemplate(ctx, i)
		if err != nil || got != want {
			t.Fatalf("template %q want %q err %v", got, want, err)
		}
	}
	check(coding.DefaultBranchTemplate)
	personal := coding.BranchRule{Team: "T", User: "U1", Template: "personal/{slug}"}
	if err = s.SaveRule(ctx, personal); err != nil {
		t.Fatal(err)
	}
	check(personal.Template)
	i.UserID = 2
	check(coding.DefaultBranchTemplate)
	i.UserID = 1
	company := coding.BranchRule{Team: "T", Template: "company/{task_id}"}
	if err = s.SaveRule(ctx, company); err != nil {
		t.Fatal(err)
	}
	check(company.Template)
	s2, err := NewBranchRuleStore(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s2.BranchTemplate(ctx, i); err != nil || got != company.Template {
		t.Fatal("restart lost policy", err)
	}
	if err = s.SaveRule(ctx, coding.BranchRule{Team: "OTHER", User: "U1", Template: "a"}); err == nil {
		t.Fatal("foreign workspace accepted")
	}
	if err = s.SaveRule(ctx, coding.BranchRule{Team: "T", User: "UNKNOWN", Template: "a"}); err == nil {
		t.Fatal("unknown member accepted")
	}
	if _, err = s.BranchTemplate(ctx, coding.Identity{Team: "OTHER", UserID: 1}); err == nil {
		t.Fatal("foreign identity accepted")
	}
	company.Template = ""
	if err = s.SaveRule(ctx, company); err != nil {
		t.Fatal(err)
	}
	check(personal.Template)
	settings, err := s.Settings(ctx)
	if err != nil || len(settings.Rules) != 1 || len(settings.Scopes) != 2 {
		t.Fatalf("settings %+v %v", settings, err)
	}
	personal.Template = ""
	if err = s.SaveRule(ctx, personal); err != nil {
		t.Fatal(err)
	}
	check(coding.DefaultBranchTemplate)
}
