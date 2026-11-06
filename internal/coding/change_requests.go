package coding

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var ErrAPIScope = errors.New("GitLab bağlantısını api izniyle yeniden yetkilendir; gitlab_connect aracını kullan")
var ErrMergeRequest = errors.New("merge için açık komut gerekli: merge <PR/MR bağlantısı> veya <PR/MR bağlantısı> merge et")
var ErrChangeMoved = errors.New("PR/MR kaynağı veya hedefi değişti; durumu kontrol edip yeni bir merge isteği gönder")
var ErrMergeBlocked = errors.New("PR/MR birleştirilemiyor: izinleri, çakışmaları, zorunlu incelemeleri ve CI durumunu kontrol et")

type ChangeRequest struct {
	Number           int    `json:"number"`
	URL              string `json:"url"`
	State            string `json:"state"`
	SourceBranch     string `json:"source_branch"`
	TargetBranch     string `json:"target_branch"`
	HeadSHA          string `json:"head_sha"`
	MergeSHA         string `json:"merge_sha,omitempty"`
	Draft            bool   `json:"draft"`
	Ready            bool   `json:"ready"`
	ReadinessPending bool   `json:"readiness_pending,omitempty"`
}

type ChangeRequestProvider interface {
	FindChange(context.Context, string, Repository, string, string) (*ChangeRequest, error)
	CreateChange(context.Context, string, Repository, string, string, string, string) (ChangeRequest, error)
	GetChange(context.Context, string, Repository, int) (ChangeRequest, error)
	MergeChange(context.Context, string, Repository, ChangeRequest) (ChangeRequest, error)
}

// A merge request pins the provider-observed revision before any remote write.
// Retrying the same signed Slack event cannot authorize newer commits.
type MergePin struct {
	SourceBranch string `json:"source_branch,omitempty"`
	Mode         string `json:"mode,omitempty"`
	AccountID    int64  `json:"account_id"`
	RepositoryID int64  `json:"repository_id"`
	Number       int    `json:"number"`
	HeadSHA      string `json:"head_sha"`
	TargetBranch string `json:"target_branch"`
}
type MergeStore interface {
	PinMerge(context.Context, Identity, MergePin) (MergePin, error)
}

func (s *Service) ChangeRequestsAvailable() bool {
	_, ok := s.provider.(ChangeRequestProvider)
	return ok
}

func (s *Service) requireChangeScope(ctx context.Context, i Identity) error {
	if s.Kind() != "gitlab" {
		return nil
	}
	return s.store.WithConnection(ctx, i.UserID, func(c *Connection) error {
		if c.Team != i.Team {
			return ErrDenied
		}
		if !strings.Contains(" "+c.Credentials.Scopes+" ", " api ") {
			return ErrAPIScope
		}
		return nil
	})
}

func (s *Service) changeAccess(ctx context.Context, i Identity, name string) (ChangeRequestProvider, Repository, Account, string, error) {
	p, ok := s.provider.(ChangeRequestProvider)
	if !ok || !s.validName(name) {
		return nil, Repository{}, Account{}, "", ErrArguments
	}
	account, token, err := s.verifiedConnection(ctx, i)
	if err != nil {
		return nil, Repository{}, Account{}, "", err
	}
	if err = s.requireChangeScope(ctx, i); err != nil {
		return nil, Repository{}, Account{}, "", err
	}
	repos, err := s.provider.Repositories(ctx, token)
	if err != nil {
		return nil, Repository{}, Account{}, "", err
	}
	repos, err = s.filterSelected(ctx, i.UserID, repos)
	if err != nil {
		return nil, Repository{}, Account{}, "", err
	}
	for _, r := range repos {
		if r.Valid() && r.Kind() == s.Kind() && r.FullName == name {
			return p, r, account, token, nil
		}
	}
	return nil, Repository{}, Account{}, "", ErrDenied
}

func (s *Service) CreateChangeRequest(ctx context.Context, i Identity, id, title, body string, targets ...string) (ChangeRequest, error) {
	if !s.EditingAvailable() || !taskID.MatchString(id) || strings.TrimSpace(title) == "" || len(title) > 240 || len(body) > 30000 {
		return ChangeRequest{}, ErrArguments
	}
	if err := s.AuthorizeIdentity(ctx, i); err != nil {
		return ChangeRequest{}, err
	}
	var result ChangeRequest
	err := s.edits.WithEdit(ctx, i.UserID, id, func(e *Edit) error {
		if !sameScope(i, e.Task.Identity) {
			return ErrDenied
		}
		if e.Task.Status != "pushed" || !gitObjectID.MatchString(e.Commit) || !boundReview(*e) {
			return ErrArguments
		}
		token, err := s.editAccess(ctx, i, *e)
		if err != nil {
			return err
		}
		if err := s.requireChangeScope(ctx, i); err != nil {
			return err
		}
		p, ok := s.provider.(ChangeRequestProvider)
		if !ok {
			return ErrUnavailable
		}
		target := e.Task.Repository.DefaultBranch
		if len(targets) > 0 && targets[0] != "" {
			target = targets[0]
		}
		if !ValidBranch(target) || target == e.Review.Branch {
			return ErrArguments
		}
		if target != e.Task.Repository.DefaultBranch {
			bp, ok := s.provider.(BranchProvider)
			if !ok {
				return ErrUnavailable
			}
			if _, err := bp.GetBranch(ctx, token, e.Task.Repository, target); err != nil {
				return err
			}
		}
		if e.ChangeRequest != nil && e.ChangeRequest.TargetBranch == target {
			result, err = p.GetChange(ctx, token, e.Task.Repository, e.ChangeRequest.Number)
		} else {
			var found *ChangeRequest
			found, err = p.FindChange(ctx, token, e.Task.Repository, e.Review.Branch, target)
			if err == nil && found != nil {
				result = *found
			} else if err == nil {
				result, err = p.CreateChange(ctx, token, e.Task.Repository, e.Review.Branch, target, title, body+"\n\nNudgy: otomatik test çalıştırılmadı.")
			}
		}
		if err != nil {
			return err
		}
		if result.HeadSHA != e.Commit || result.SourceBranch != e.Review.Branch || result.TargetBranch != target {
			return ErrChangeMoved
		}
		e.ChangeRequest = &result
		return nil
	})
	return result, err
}

func (s *Service) ChangeRequestStatus(ctx context.Context, i Identity, name string, number int) (ChangeRequest, error) {
	if number <= 0 {
		return ChangeRequest{}, ErrArguments
	}
	p, r, _, token, err := s.changeAccess(ctx, i, name)
	if err != nil {
		return ChangeRequest{}, err
	}
	return p.GetChange(ctx, token, r, number)
}

var slackChangeLink = regexp.MustCompile(`<((?:https://github\.com/|https://gitlab\.com/)[^<>|\s]+)(?:\|[^<>]*)?>`)

// Fail closed on free-form prose, negations, quotes, questions and additional
// targets. No model-supplied boolean or extracted quote can grant merge authority.
func ExplicitMerge(request string, r Repository, number int) bool {
	if number <= 0 || (r.Kind() == "github" && !ValidRepository(r.FullName)) || (r.Kind() == "gitlab" && !ValidGitLabRepository(r.FullName)) {
		return false
	}
	target := r.Origin() + "/" + r.FullName + "/pull/" + strconv.Itoa(number)
	if r.Kind() == "gitlab" {
		target = r.Origin() + "/" + r.FullName + "/-/merge_requests/" + strconv.Itoa(number)
	}
	normalized := slackChangeLink.ReplaceAllString(normalizeMergeText(request), "$1")
	for _, command := range []string{"merge " + target, target + " merge et", target + " birleştir", "birleştir " + target} {
		if normalized == command {
			return true
		}
	}
	return false
}

func (s *Service) MergeChangeRequest(ctx context.Context, i Identity, name string, number int) (ChangeRequest, error) {
	// Validate intent before making even a provider read or credential refresh.
	candidate := Repository{Provider: s.Kind(), FullName: name}
	if i.Event == "" || i.Channel == "" || i.Thread == "" || !ExplicitMerge(i.Request, candidate, number) {
		return ChangeRequest{}, ErrMergeRequest
	}
	p, r, account, token, err := s.changeAccess(ctx, i, name)
	if err != nil {
		return ChangeRequest{}, err
	}
	current, err := p.GetChange(ctx, token, r, number)
	if err != nil {
		return ChangeRequest{}, err
	}
	if current.State == "merged" {
		return current, nil
	}
	if current.State != "open" || current.Draft {
		return ChangeRequest{}, ErrMergeBlocked
	}
	store, ok := s.store.(MergeStore)
	if !ok {
		return ChangeRequest{}, ErrUnavailable
	}
	pin := MergePin{AccountID: account.ID, RepositoryID: r.ID, Number: number, HeadSHA: current.HeadSHA, TargetBranch: current.TargetBranch}
	saved, err := store.PinMerge(ctx, i, pin)
	if err != nil {
		return ChangeRequest{}, err
	}
	if saved != pin {
		return ChangeRequest{}, ErrChangeMoved
	}
	if !current.Ready {
		return ChangeRequest{}, ErrMergeBlocked
	}
	result, err := p.MergeChange(ctx, token, r, current)
	if err != nil {
		return ChangeRequest{}, err
	}
	if result.State != "merged" {
		return result, fmt.Errorf("merge sonucu henüz doğrulanmadı; PR/MR durumunu sorgula")
	}
	return result, nil
}
