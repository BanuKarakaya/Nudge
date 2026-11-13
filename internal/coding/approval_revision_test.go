package coding

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestApprovalPinsRevisionBeforeUserClick(t *testing.T) {
	s, store, base, i := fixture(t)
	p := &changeProvider{fakeProvider: base, change: ChangeRequest{Number: 12, State: "open", Ready: true, HeadSHA: strings.Repeat("a", 40), SourceBranch: "feature", TargetBranch: "main"}}
	s.provider = p
	s.store = &pinStore{memoryStore: store}
	i.Request = "merge https://github.com/owner/repo/pull/12"
	revision, err := s.PreviewMerge(context.Background(), i, "owner/repo", 12, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if p.merges != 0 || p.creates != 0 {
		t.Fatal("preview mutated provider")
	}
	p.change.HeadSHA = strings.Repeat("b", 40)
	ctx := WithApprovedRevision(context.Background(), revision)
	if _, err = s.MergeChangeRequest(ctx, i, "owner/repo", 12); !errors.Is(err, ErrChangeMoved) {
		t.Fatal("accepted changed source", err)
	}
	p.change.HeadSHA = strings.Repeat("a", 40)
	p.change.TargetBranch = "production"
	if _, err = s.MergeChangeRequest(ctx, i, "owner/repo", 12); !errors.Is(err, ErrChangeMoved) {
		t.Fatal("accepted changed target", err)
	}
	if p.merges != 0 {
		t.Fatal("stale approval reached merge")
	}
	p.change.TargetBranch = "main"
	if _, err = s.MergeChangeRequest(ctx, i, "owner/repo", 12); err != nil || p.merges != 1 {
		t.Fatal("valid approval rejected", err)
	}
}
