package coding

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type testBranchRules struct {
	template string
	err      error
}

func (r *testBranchRules) BranchTemplate(context.Context, Identity) (string, error) {
	return r.template, r.err
}

func TestBranchTemplates(t *testing.T) {
	repo := Repository{FullName: "group/sub/Mobile_App", DefaultBranch: "main"}
	got, err := RenderBranch("feature/{user}/{repo}/{date}/{slug}-{task_id}", strings.Repeat("a", 32), "Giriş ekranını güncelle!", "Banu.Test", repo, time.Date(2026, 11, 6, 3, 0, 0, 0, time.UTC))
	want := "feature/banu-test/mobile-app/20261106/giris-ekranini-guncelle-" + strings.Repeat("a", 32)
	if err != nil || got != want {
		t.Fatalf("%q %v", got, err)
	}
	for _, template := range []string{"main", "HEAD", "-x", "refs/heads/foo", "a//b", "a/../b", "foo.lock", "a/.hidden", "a/", "a.", "a b", "x@{y}", "{unknown}", "a\\b", "a\nb", "feature/{slug}/..", strings.Repeat("a", 161)} {
		if _, err := ValidateBranchTemplate(template); err == nil {
			t.Errorf("accepted %q", template)
		}
	}
	if _, err := ValidateBranchTemplate("feature/{slug}"); err != nil {
		t.Fatal("task ID is optional", err)
	}
	r := reviewFixture()
	before, _ := r.Digest()
	r.Branch = "company/change-123"
	after, err := r.Digest()
	if err != nil || after == before {
		t.Fatal("branch not bound to digest", err)
	}
}
func TestBranchRulePinnedThroughRetryAndPublish(t *testing.T) {
	s, _, p, i := fixture(t)
	edits := &memoryEdits{}
	git := &recordingGit{}
	s.EnableEditing(edits, git)
	rule := &testBranchRules{template: "feature/{repo}/{task_id}"}
	s.WithBranchRules(rule)
	info, err := s.BeginEdit(context.Background(), i, p.repos[0].FullName)
	if err != nil || info.Branch != "feature/repo/"+info.Task.ID {
		t.Fatalf("%+v %v", info, err)
	}
	rule.template = "changed/{task_id}"
	retry, err := s.BeginEdit(context.Background(), i, p.repos[0].FullName)
	if err != nil || retry.Branch != info.Branch {
		t.Fatalf("retry renamed branch: %+v %v", retry, err)
	}
	if _, err = s.WriteEditFile(context.Background(), i, info.Task.ID, "README.md", "new", false); err != nil {
		t.Fatal(err)
	}
	published, err := s.FinishAndPublish(context.Background(), i, info.Task.ID, "Updated readme")
	if err != nil || published.Branch != info.Branch || git.review.Branch != info.Branch {
		t.Fatalf("publish renamed branch: %+v %v", published, err)
	}
}
func TestBranchRuleFailureStopsClone(t *testing.T) {
	for _, r := range []*testBranchRules{{err: errors.New("database offline")}, {template: "master"}, {template: "{invalid}"}} {
		s, _, _, i := fixture(t)
		git := &recordingGit{}
		s.EnableEditing(&memoryEdits{}, git)
		s.WithBranchRules(r)
		if _, err := s.BeginEdit(context.Background(), i, "owner/repo"); err == nil || git.clones != 0 {
			t.Fatal("invalid/unavailable policy proceeded", err)
		}
	}
}
