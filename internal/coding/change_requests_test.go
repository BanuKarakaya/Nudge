package coding

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type changeProvider struct {
	*fakeProvider
	change                 ChangeRequest
	creates, merges, reads int
	mergeErr               error
}

func (p *changeProvider) FindChange(context.Context, string, Repository, string, string) (*ChangeRequest, error) {
	if p.creates == 0 {
		return nil, nil
	}
	v := p.change
	return &v, nil
}
func (p *changeProvider) CreateChange(_ context.Context, _ string, r Repository, source, target, title, body string) (ChangeRequest, error) {
	p.creates++
	p.change.SourceBranch = source
	p.change.TargetBranch = target
	return p.change, nil
}
func (p *changeProvider) GetChange(context.Context, string, Repository, int) (ChangeRequest, error) {
	p.reads++
	return p.change, nil
}
func (p *changeProvider) MergeChange(context.Context, string, Repository, ChangeRequest) (ChangeRequest, error) {
	p.merges++
	if p.mergeErr != nil {
		return ChangeRequest{}, p.mergeErr
	}
	p.change.State = "merged"
	return p.change, nil
}

type pinStore struct {
	*memoryStore
	pins map[string]MergePin
}

func (s *pinStore) PinMerge(_ context.Context, i Identity, p MergePin) (MergePin, error) {
	if s.pins == nil {
		s.pins = map[string]MergePin{}
	}
	if saved, ok := s.pins[i.Event]; ok {
		return saved, nil
	}
	s.pins[i.Event] = p
	return p, nil
}

func TestMergeRequiresExactCurrentRequestAndPinsRevision(t *testing.T) {
	s, store, base, i := fixture(t)
	p := &changeProvider{fakeProvider: base, change: ChangeRequest{Number: 12, URL: "https://github.com/owner/repo/pull/12", State: "open", Ready: true, HeadSHA: strings.Repeat("a", 40), TargetBranch: "main"}}
	s.provider = p
	s.store = &pinStore{memoryStore: store}
	ctx := context.Background()
	for _, request := range []string{"PR aç", "kodu düzelt", "merge et", "do not merge https://github.com/owner/repo/pull/12", "merge https://github.com/owner/repo/pull/13", "merge https://gitlab.com/owner/repo/-/merge_requests/12", "`merge https://github.com/owner/repo/pull/12`", "> merge https://github.com/owner/repo/pull/12", "merge https://github.com/owner/repo/pull/12?x=1", "merge https://github.com/owner/repo/pull/12\nmerge https://github.com/owner/another/pull/1"} {
		i.Request = request
		if _, err := s.MergeChangeRequest(ctx, i, "owner/repo", 12); !errors.Is(err, ErrMergeRequest) {
			t.Fatalf("accepted %q: %v", request, err)
		}
	}
	if p.reads != 0 || p.merges != 0 {
		t.Fatal("denied intent reached provider")
	}
	i.Request = "merge https://github.com/owner/repo/pull/12"
	p.mergeErr = ErrUnavailable
	if _, err := s.MergeChangeRequest(ctx, i, "owner/repo", 12); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	p.change.HeadSHA = strings.Repeat("b", 40)
	if _, err := s.MergeChangeRequest(ctx, i, "owner/repo", 12); !errors.Is(err, ErrChangeMoved) {
		t.Fatal("replay authorized changed commit", err)
	}
	if p.merges != 1 {
		t.Fatal("stale request merged new revision")
	}
	i.Event = "fresh-explicit-request"
	p.mergeErr = nil
	result, err := s.MergeChangeRequest(ctx, i, "owner/repo", 12)
	if err != nil || result.State != "merged" || p.merges != 2 {
		t.Fatal("explicit merge failed", result, err)
	}
	if _, err := s.MergeChangeRequest(ctx, i, "owner/repo", 12); err != nil || p.merges != 2 {
		t.Fatal("replayed merge", err)
	}
}

func TestMergeChecksCurrentRepositoryAndWorkspacePermissions(t *testing.T) {
	for _, mode := range []string{"workspace", "revoked repo", "draft", "closed", "blocked", "missing pin store"} {
		t.Run(mode, func(t *testing.T) {
			s, store, base, i := fixture(t)
			p := &changeProvider{fakeProvider: base, change: ChangeRequest{Number: 1, State: "open", Ready: true, HeadSHA: strings.Repeat("a", 40), TargetBranch: "main"}}
			s.provider = p
			s.store = &pinStore{memoryStore: store}
			i.Request = "merge https://github.com/owner/repo/pull/1"
			switch mode {
			case "workspace":
				i.Team = "other"
			case "revoked repo":
				base.repos = nil
			case "draft":
				p.change.Draft = true
			case "closed":
				p.change.State = "closed"
			case "blocked":
				p.change.Ready = false
			case "missing pin store":
				s.store = store
			}
			if _, err := s.MergeChangeRequest(context.Background(), i, "owner/repo", 1); err == nil {
				t.Fatal("unauthorized merge")
			}
			if p.merges != 0 {
				t.Fatal("write reached provider")
			}
		})
	}
}

func TestExplicitMergeParsesOnlyCanonicalTargets(t *testing.T) {
	for _, r := range []Repository{{ID: 10, InstallationID: 2, FullName: "owner/repo"}, {Provider: "gitlab", ID: 10, FullName: "team/sub/app"}} {
		link := r.Origin() + "/" + r.FullName + "/pull/12"
		if r.Kind() == "gitlab" {
			link = r.Origin() + "/" + r.FullName + "/-/merge_requests/12"
		}
		for _, request := range []string{"merge " + link, link + " merge et", link + " birleştir", "birleştir " + link, "merge <" + link + "|PR/MR>"} {
			if !ExplicitMerge(request, r, 12) {
				t.Fatal("valid explicit command rejected", request)
			}
		}
		if ExplicitMerge("merge "+link, r, 13) {
			t.Fatal("different target authorized")
		}
	}
}

func TestCreatePRRequiresPublishedOwnedTaskAndRecoversLostResponse(t *testing.T) {
	s, edits, _, base, _, i, info := editFixture(t)
	p := &changeProvider{fakeProvider: base, change: ChangeRequest{Number: 4, State: "open", HeadSHA: strings.Repeat("3", 40)}}
	s.provider = p
	ctx := context.Background()
	if _, err := s.CreateChangeRequest(ctx, i, info.Task.ID, "title", "body"); err == nil {
		t.Fatal("created PR from unpublished task")
	}
	if _, err := s.WriteEditFile(ctx, i, info.Task.ID, "src/app.go", "after", false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FinishAndPublish(ctx, i, info.Task.ID, "summary"); err != nil {
		t.Fatal(err)
	}
	for j := 0; j < 2; j++ {
		result, err := s.CreateChangeRequest(ctx, i, info.Task.ID, "title", "body")
		if err != nil || result.Number != 4 {
			t.Fatal(result, err)
		}
	}
	if p.creates != 1 || p.merges != 0 {
		t.Fatal("duplicate PR or implicit merge")
	}
	// Simulate remote success followed by loss of the local transaction.
	saved := edits.items[info.Task.ID]
	saved.ChangeRequest = nil
	edits.items[info.Task.ID] = saved
	if _, err := s.CreateChangeRequest(ctx, i, info.Task.ID, "title", "body"); err != nil || p.creates != 1 {
		t.Fatal("lost-response recovery duplicated PR", err)
	}
	other := i
	other.Thread = "another-thread"
	if _, err := s.CreateChangeRequest(ctx, other, info.Task.ID, "title", "body"); err == nil {
		t.Fatal("cross-thread publication")
	}
	p.change.HeadSHA = strings.Repeat("9", 40)
	if _, err := s.CreateChangeRequest(ctx, i, info.Task.ID, "title", "body"); !errors.Is(err, ErrChangeMoved) {
		t.Fatal("changed source accepted", err)
	}
}

func TestLegacyGitLabScopeKeepsEditingButRequiresReauthorizationForMR(t *testing.T) {
	s, store, _, _, _, _ := gitLabFixture(t)
	connectGitLabFixture(t, s, store)
	store.connection.Credentials.Scopes = "read_user read_api write_repository"
	i := Identity{UserID: 1, Team: "T", Channel: "C", Thread: "1", Event: "E", Request: "update code"}
	if _, err := s.Repositories(context.Background(), i); err != nil {
		t.Fatal("legacy read broke", err)
	}
	if _, err := s.BeginEdit(context.Background(), i, "team/subgroup/app"); err != nil {
		t.Fatal("legacy editing broke", err)
	}
	if _, err := s.ChangeRequestStatus(context.Background(), i, "team/subgroup/app", 1); !errors.Is(err, ErrAPIScope) {
		t.Fatal("missing api scope not explained", err)
	}
}
