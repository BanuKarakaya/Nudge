package coding

import (
	"context"
	"errors"
	"strings"
	"time"
)

type Branch struct {
	Name string `json:"name"`
	SHA  string `json:"sha"`
}

type BranchProvider interface {
	GetBranch(context.Context, string, Repository, string) (Branch, error)
	ListBranches(context.Context, string, Repository, int) ([]Branch, error)
}

func (s *Service) BranchesAvailable() bool {
	_, ok := s.provider.(BranchProvider)
	return ok && s.ChangeRequestsAvailable()
}

// Branch refs are data, never shell arguments. Allow Unicode and slash names,
// while rejecting git revision expressions and invalid refs.
func ValidBranch(name string) bool {
	if name == "" || len(name) > 1024 || name == "HEAD" || name == "@" || strings.HasPrefix(name, "-") || strings.HasPrefix(name, "refs/") || strings.HasSuffix(name, ".") || strings.Contains(name, "..") || strings.Contains(name, "@{") {
		return false
	}
	for _, c := range name {
		if c <= ' ' || c == 127 || strings.ContainsRune("~^:?*[\\", c) {
			return false
		}
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".lock") {
			return false
		}
	}
	return true
}

func (s *Service) Branches(ctx context.Context, i Identity, name string, page int) ([]Branch, error) {
	if page < 1 || page > 1000 {
		return nil, ErrArguments
	}
	_, r, _, token, err := s.changeAccess(ctx, i, name)
	if err != nil {
		return nil, err
	}
	p, ok := s.provider.(BranchProvider)
	if !ok {
		return nil, ErrUnavailable
	}
	return p.ListBranches(ctx, token, r, page)
}

func branchPair(ctx context.Context, p ChangeRequestProvider, token string, r Repository, source, target string) (Branch, error) {
	if !ValidBranch(source) || !ValidBranch(target) || source == target {
		return Branch{}, ErrArguments
	}
	bp, ok := p.(BranchProvider)
	if !ok {
		return Branch{}, ErrUnavailable
	}
	branch, err := bp.GetBranch(ctx, token, r, source)
	if err != nil {
		return Branch{}, err
	}
	_, err = bp.GetBranch(ctx, token, r, target)
	return branch, err
}

// Reuse an open request or a completed request for the same revision. A branch
// reused for new commits after an old merge can have a new PR/MR.
func ensureBranchChange(ctx context.Context, p ChangeRequestProvider, token string, r Repository, source Branch, target, title, body string) (ChangeRequest, error) {
	found, err := p.FindChange(ctx, token, r, source.Name, target)
	if err != nil {
		return ChangeRequest{}, err
	}
	var result ChangeRequest
	if found != nil && (found.State == "open" || found.HeadSHA == source.SHA) {
		result = *found
	} else {
		result, err = p.CreateChange(ctx, token, r, source.Name, target, title, body+"\n\nNudgy: otomatik test çalıştırılmadı.")
	}
	if err != nil {
		return ChangeRequest{}, err
	}
	if result.HeadSHA != source.SHA || result.SourceBranch != source.Name || result.TargetBranch != target {
		return ChangeRequest{}, ErrChangeMoved
	}
	return result, nil
}

func (s *Service) CreateBranchChangeRequest(ctx context.Context, i Identity, name, source, target, title, body string) (ChangeRequest, error) {
	if strings.TrimSpace(title) == "" || len(title) > 240 || len(body) > 30000 {
		return ChangeRequest{}, ErrArguments
	}
	p, r, _, token, err := s.changeAccess(ctx, i, name)
	if err != nil {
		return ChangeRequest{}, err
	}
	branch, err := branchPair(ctx, p, token, r, source, target)
	if err != nil {
		return ChangeRequest{}, err
	}
	return ensureBranchChange(ctx, p, token, r, branch, target, title, body)
}

type mergePinReader interface {
	GetMergePin(context.Context, Identity) (MergePin, bool, error)
}

var ErrBranchMerge = errors.New("kaynak/hedef belirsiz veya mevcut kullanıcı isteği bu dallar için merge yetkisi vermiyor")

// The host resolver (or legacy parser and saved task) determines the merge pair.
// The persisted pin binds retries to one request/revision even if branches move.
func (s *Service) MergeBranches(ctx context.Context, i Identity, name, source, target string) (ChangeRequest, error) {
	intent, ok := parseBranchMerge(i.Request)
	resolved, semantic, err := semanticMergeIntent(ctx, i)
	if err != nil {
		return ChangeRequest{}, err
	}
	if semantic {
		ok = resolved.Action == "merge" && resolved.Number == 0
		intent = branchMergeIntent{Provider: resolved.Provider, Repository: resolved.Repository, Source: resolved.SourceBranch, Target: resolved.TargetBranch}
	}
	if !ok || i.Event == "" || i.Channel == "" || i.Thread == "" || intent.Target != target {
		return ChangeRequest{}, ErrBranchMerge
	}
	if intent.Source != "" && intent.Source != source {
		return ChangeRequest{}, ErrBranchMerge
	}
	if !intent.Current && (intent.Provider != s.Kind() || intent.Repository != name || intent.Source != source) {
		return ChangeRequest{}, ErrBranchMerge
	}
	p, r, account, token, err := s.changeAccess(ctx, i, name)
	if err != nil {
		return ChangeRequest{}, err
	}
	store, ok := s.store.(MergeStore)
	reader, canRead := s.store.(mergePinReader)
	if !ok || !canRead {
		return ChangeRequest{}, ErrUnavailable
	}
	saved, exists, err := reader.GetMergePin(ctx, i)
	if err != nil {
		return ChangeRequest{}, err
	}
	var current ChangeRequest
	if exists {
		if saved.Mode != "branches" || saved.AccountID != account.ID || saved.RepositoryID != r.ID || saved.SourceBranch != source || saved.TargetBranch != target {
			return ChangeRequest{}, ErrChangeMoved
		}
		current, err = p.GetChange(ctx, token, r, saved.Number)
		if err != nil {
			return ChangeRequest{}, err
		}
		if current.HeadSHA != saved.HeadSHA || current.SourceBranch != source || current.TargetBranch != target {
			return ChangeRequest{}, ErrChangeMoved
		}
	} else {
		if intent.Current {
			// A demonstrative request must resolve to exactly one published task
			// in this user's thread, not a guessed branch or another user's work.
			tasks, err := s.EditTasks(ctx, i)
			if err != nil {
				return ChangeRequest{}, err
			}
			// A bounded list must not hide older candidates. With an explicit
			// source, filter below; without one require a single task in the thread.
			if len(tasks) == 0 || len(tasks) >= 20 || (intent.Source == "" && len(tasks) != 1) {
				return ChangeRequest{}, ErrBranchMerge
			}
			if s.mergePeer != nil {
				peerTasks, err := s.mergePeer.EditTasks(ctx, i)
				if err != nil && !errors.Is(err, ErrConnect) {
					return ChangeRequest{}, err
				}
				if len(peerTasks) != 0 {
					return ChangeRequest{}, ErrBranchMerge
				}
			}
			var match *Edit
			for _, task := range tasks {
				if task.Status != "pushed" {
					continue
				}
				e, err := s.edits.GetEdit(ctx, i.UserID, task.ID)
				if err != nil {
					return ChangeRequest{}, err
				}
				if intent.Source != "" && e.Review.Branch != intent.Source {
					continue
				}
				if !sameScope(i, e.Task.Identity) || e.AccountID != account.ID || !boundReview(e) {
					return ChangeRequest{}, ErrDenied
				}
				if match != nil {
					return ChangeRequest{}, ErrBranchMerge
				}
				match = &e
			}
			if match == nil || match.Task.Repository.ID != r.ID || match.Task.Repository.FullName != name || match.Review.Branch != source {
				return ChangeRequest{}, ErrBranchMerge
			}
		}
		branch, err := branchPair(ctx, p, token, r, source, target)
		if err != nil {
			return ChangeRequest{}, err
		}
		current, err = ensureBranchChange(ctx, p, token, r, branch, target, "Merge "+source+" into "+target, "Kullanıcının açık dal birleştirme isteği.")
		if err != nil {
			return ChangeRequest{}, err
		}
		pin := MergePin{Mode: "branches", AccountID: account.ID, RepositoryID: r.ID, Number: current.Number, SourceBranch: source, HeadSHA: current.HeadSHA, TargetBranch: target}
		saved, err = store.PinMerge(ctx, i, pin)
		if err != nil {
			return ChangeRequest{}, err
		}
		if saved != pin {
			return ChangeRequest{}, ErrChangeMoved
		}
	}
	// Providers may calculate mergeability asynchronously after PR/MR creation.
	// A short synchronous recheck is bounded; nothing is scheduled for later.
	for attempt := 0; current.State == "open" && current.ReadinessPending && attempt < 3; attempt++ {
		timer := time.NewTimer(time.Duration(attempt+1) * 250 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return current, ctx.Err()
		case <-timer.C:
		}
		current, err = p.GetChange(ctx, token, r, saved.Number)
		if err != nil {
			return ChangeRequest{}, err
		}
		if current.HeadSHA != saved.HeadSHA || current.SourceBranch != source || current.TargetBranch != target {
			return ChangeRequest{}, ErrChangeMoved
		}
	}
	if current.State == "merged" {
		return current, nil
	}
	// Creation may start asynchronous mergeability checks. Return the actual
	// request alongside the error so the user can see what was created.
	if current.State != "open" || current.Draft || !current.Ready {
		return current, ErrMergeBlocked
	}
	result, err := p.MergeChange(ctx, token, r, current)
	if err != nil {
		return current, err
	}
	if result.State != "merged" {
		return result, ErrMergeBlocked
	}
	return result, nil
}
