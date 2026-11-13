package coding

import (
	"context"
	"encoding/json"
)

type approvalRevisionKey struct{}
type approvalRevision struct{ Head, Source, Target string }

// WithApprovedRevision carries host-persisted review data, never model arguments.
func WithApprovedRevision(ctx context.Context, revision string) context.Context {
	return context.WithValue(ctx, approvalRevisionKey{}, revision)
}
func checkApprovedRevision(ctx context.Context, head, source, target string) error {
	raw, _ := ctx.Value(approvalRevisionKey{}).(string)
	if raw == "" {
		return nil
	}
	var expected approvalRevision
	if json.Unmarshal([]byte(raw), &expected) != nil || expected != (approvalRevision{head, source, target}) {
		return ErrChangeMoved
	}
	return nil
}

// PreviewMerge reads and freezes the exact source commit and destination before
// displaying the human approval card. It performs no provider write.
func (s *Service) PreviewMerge(ctx context.Context, i Identity, name string, number int, source, target string) (string, error) {
	p, r, _, token, err := s.changeAccess(ctx, i, name)
	if err != nil {
		return "", err
	}
	var revision approvalRevision
	if number > 0 {
		current, err := p.GetChange(ctx, token, r, number)
		if err != nil {
			return "", err
		}
		revision = approvalRevision{current.HeadSHA, current.SourceBranch, current.TargetBranch}
	} else {
		branch, err := branchPair(ctx, p, token, r, source, target)
		if err != nil {
			return "", err
		}
		revision = approvalRevision{branch.SHA, source, target}
	}
	if !gitObjectID.MatchString(revision.Head) || !ValidBranch(revision.Source) || !ValidBranch(revision.Target) {
		return "", ErrArguments
	}
	raw, err := json.Marshal(revision)
	return string(raw), err
}
