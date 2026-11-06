package coding

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func (s *pinStore) GetMergePin(_ context.Context, i Identity) (MergePin, bool, error) {
	p, ok := s.pins[i.Event]
	return p, ok, nil
}

func (m *memoryEdits) ListEditTasks(_ context.Context, i Identity, account int64) ([]Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var tasks []Task
	for _, e := range m.items {
		if sameScope(e.Task.Identity, i) && e.AccountID == account {
			tasks = append(tasks, e.Task)
		}
	}
	return tasks, nil
}

type branchChangeProvider struct {
	*changeProvider
	branches    map[string]string
	readyOnRead bool
}

func (p *branchChangeProvider) GetBranch(_ context.Context, _ string, _ Repository, name string) (Branch, error) {
	sha, ok := p.branches[name]
	if !ok {
		return Branch{}, ErrNotFound
	}
	return Branch{Name: name, SHA: sha}, nil
}
func (p *branchChangeProvider) ListBranches(context.Context, string, Repository, int) ([]Branch, error) {
	return nil, nil
}
func (p *branchChangeProvider) FindChange(_ context.Context, _ string, _ Repository, source, target string) (*ChangeRequest, error) {
	if p.creates == 0 || p.change.SourceBranch != source || p.change.TargetBranch != target {
		return nil, nil
	}
	c := p.change
	return &c, nil
}
func (p *branchChangeProvider) GetChange(ctx context.Context, token string, r Repository, number int) (ChangeRequest, error) {
	if p.readyOnRead {
		p.change.Ready, p.change.ReadinessPending = true, false
	}
	return p.changeProvider.GetChange(ctx, token, r, number)
}

func TestBranchMergeIntentPreservesDirectionAndRejectsUntrustedInstructions(t *testing.T) {
	for _, text := range []string{
		"tamam bence olmuş bu branchi development branch ine mergeleyebilir misin",
		"bu dalı development dalına merge et", "Bu branchi development'a mergele?",
		"bu dalı `development` dalına birleştir",
	} {
		i, ok := parseBranchMerge(text)
		if !ok || !i.Current || i.Target != "development" {
			t.Fatalf("missed request %q: %+v", text, i)
		}
	}
	for _, provider := range []string{"github", "gitlab"} {
		text := provider + " owner/repo Feature/Login -> development merge et"
		i, ok := parseBranchMerge(text)
		if !ok || i.Current || i.Source != "Feature/Login" || i.Target != "development" || i.Provider != provider {
			t.Fatal(i, ok)
		}
	}
	for _, text := range []string{
		"bu dalı development dalına merge etme", "bu dalı development dalına mergeleyebilir miyim",
		"bu dalı development dalına merge etmeli miyiz", "bu dalı development dalına merge etmeden önce incele",
		"testler geçerse bu dalı development dalına merge et", "bu dalı development dalına merge et ve sil",
		"> bu dalı development dalına merge et", "örnek: `bu dalı development dalına merge et`",
		"github owner/repo main -> main merge et", "gitlab owner/repo a -> b PR aç",
		"github owner/repo a -> b merge et\ngithub owner/other a -> b merge et", "merge et",
	} {
		if _, ok := parseBranchMerge(text); ok {
			t.Errorf("accepted %q", text)
		}
	}
}

func TestArbitraryBranchMergePinsAndRecoversWithoutNewPR(t *testing.T) {
	for _, kind := range []string{"github", "gitlab"} {
		t.Run(kind, func(t *testing.T) {
			s, store, base, i := fixture(t)
			if kind == "gitlab" {
				s.kind = kind
				store.connection.Credentials = Credentials{Access: glToken, Scopes: "api", SelectedProjects: "[10]"}
				base.repos[0].Provider = kind
				base.repos[0].InstallationID = 0
			}
			sha := strings.Repeat("a", 40)
			p := &branchChangeProvider{changeProvider: &changeProvider{fakeProvider: base, change: ChangeRequest{Number: 12, State: "open", HeadSHA: sha, Ready: true}}, branches: map[string]string{"release/v2": sha, "development": strings.Repeat("b", 40)}}
			s.provider = p
			s.store = &pinStore{memoryStore: store}
			i.Request = kind + " owner/repo release/v2 -> development merge et"
			ctx := context.Background()
			p.mergeErr = ErrUnavailable
			if _, err := s.MergeBranches(ctx, i, "owner/repo", "release/v2", "development"); !errors.Is(err, ErrUnavailable) {
				t.Fatal(err)
			}
			if p.creates != 1 || p.merges != 1 {
				t.Fatal("did not create and attempt merge")
			}
			p.change.HeadSHA = strings.Repeat("c", 40)
			if _, err := s.MergeBranches(ctx, i, "owner/repo", "release/v2", "development"); !errors.Is(err, ErrChangeMoved) {
				t.Fatal("replay accepted newer revision", err)
			}
			p.change.HeadSHA = sha
			p.mergeErr = nil
			got, err := s.MergeBranches(ctx, i, "owner/repo", "release/v2", "development")
			if err != nil || got.State != "merged" || p.creates != 1 {
				t.Fatal(got, err)
			}
			// Branches can be deleted after a successful merge; replay uses the
			// pinned request instead of opening another PR or reading deleted refs.
			p.branches = nil
			if _, err := s.MergeBranches(ctx, i, "owner/repo", "release/v2", "development"); err != nil || p.merges != 2 || p.creates != 1 {
				t.Fatal("duplicate merge on recovery", err)
			}
		})
	}
}

func TestBranchMergeRejectsWrongIntentPermissionsAndBlockedChecks(t *testing.T) {
	for _, mode := range []string{"wrong target", "wrong source", "wrong provider", "other repo", "question", "workspace", "revoked", "missing target", "blocked", "draft", "pending", "account changed"} {
		t.Run(mode, func(t *testing.T) {
			s, store, base, i := fixture(t)
			sha := strings.Repeat("a", 40)
			p := &branchChangeProvider{changeProvider: &changeProvider{fakeProvider: base, change: ChangeRequest{Number: 12, State: "open", HeadSHA: sha, Ready: true}}, branches: map[string]string{"feature": sha, "development": sha}}
			s.provider = p
			s.store = &pinStore{memoryStore: store}
			i.Request = "github owner/repo feature -> development merge et"
			source, target, name := "feature", "development", "owner/repo"
			switch mode {
			case "wrong target":
				target = "main"
			case "wrong source":
				source = "other"
			case "wrong provider":
				i.Request = "gitlab owner/repo feature -> development merge et"
			case "other repo":
				name = "owner/another"
			case "question":
				i.Request = "bu dalı development dalına merge etmeli miyiz"
			case "workspace":
				i.Team = "OTHER"
			case "revoked":
				base.repos = nil
			case "missing target":
				delete(p.branches, "development")
			case "blocked":
				p.change.Ready = false
			case "draft":
				p.change.Draft = true
			case "pending":
				p.change.Ready = false
				p.change.ReadinessPending = true
				p.readyOnRead = true
			case "account changed":
				store.connection.GitHubUserID = 99
			}
			got, err := s.MergeBranches(context.Background(), i, name, source, target)
			if mode == "pending" {
				if err != nil || got.State != "merged" || p.merges != 1 {
					t.Fatal("pending check not refreshed", got, err)
				}
			} else if err == nil || p.merges != 0 {
				t.Fatal("unexpected merge", got, err)
			}
			if (mode == "blocked" || mode == "draft") && got.Number != 12 {
				t.Fatal("lost PR URL on blocked merge")
			}
		})
	}
}

func TestCurrentTaskCanMergeIntoNonDefaultBranchAndRejectAmbiguity(t *testing.T) {
	for _, mode := range []string{"ok", "explicit source", "explicit wrong source", "explicit disambiguates", "other thread", "wrong source", "ambiguous", "peer ambiguous"} {
		t.Run(mode, func(t *testing.T) {
			s, edits, _, base, store, i, info := editFixture(t)
			if _, err := s.WriteEditFile(context.Background(), i, info.Task.ID, "src/app.go", "after", false); err != nil {
				t.Fatal(err)
			}
			published, err := s.FinishAndPublish(context.Background(), i, info.Task.ID, "summary")
			if err != nil {
				t.Fatal(err)
			}
			p := &branchChangeProvider{changeProvider: &changeProvider{fakeProvider: base, change: ChangeRequest{Number: 12, State: "open", HeadSHA: published.Commit, Ready: true}}, branches: map[string]string{published.Branch: published.Commit, "development": strings.Repeat("b", 40)}}
			s.provider = p
			s.store = &pinStore{memoryStore: store}
			i.Event = "merge-new"
			i.Request = "tamam bence olmuş bu branchi development branch ine mergeleyebilir misin"
			source := published.Branch
			switch mode {
			case "explicit source", "explicit wrong source", "explicit disambiguates":
				i.Request = "`" + published.Branch + "` bu branchi development branchine mergeleyebilir misin"
				if mode == "explicit wrong source" {
					source = "unrelated"
				}
				if mode == "explicit disambiguates" {
					e := edits.items[info.Task.ID]
					e.Task.ID = strings.Repeat("f", 32)
					e.Review.Branch = "other-branch"
					edits.items[e.Task.ID] = e
				}
			case "other thread":
				i.Thread = "other"
			case "wrong source":
				source = "unrelated"
			case "ambiguous":
				e := edits.items[info.Task.ID]
				e.Task.ID = strings.Repeat("f", 32)
				edits.items[e.Task.ID] = e
			case "peer ambiguous":
				peer := *s
				s.WithMergePeer(&peer)
			}
			got, err := s.MergeBranches(context.Background(), i, "owner/repo", source, "development")
			if mode == "ok" || mode == "explicit source" || mode == "explicit disambiguates" {
				if err != nil || got.State != "merged" || got.TargetBranch != "development" {
					t.Fatal(got, err)
				}
			} else if err == nil || p.merges != 0 || p.creates != 0 {
				t.Fatal("ambiguous task mutated provider", got, err)
			}
		})
	}
}

func TestCreateFromArbitraryBranchesAndTaskTargetDoesNotMerge(t *testing.T) {
	s, edits, _, base, _, i, info := editFixture(t)
	if _, err := s.WriteEditFile(context.Background(), i, info.Task.ID, "src/app.go", "after", false); err != nil {
		t.Fatal(err)
	}
	published, err := s.FinishAndPublish(context.Background(), i, info.Task.ID, "summary")
	if err != nil {
		t.Fatal(err)
	}
	p := &branchChangeProvider{changeProvider: &changeProvider{fakeProvider: base, change: ChangeRequest{Number: 12, State: "open", HeadSHA: published.Commit}}, branches: map[string]string{published.Branch: published.Commit, "development": published.Commit, "existing/manual": published.Commit}}
	s.provider = p
	for n := 0; n < 2; n++ {
		got, err := s.CreateChangeRequest(context.Background(), i, info.Task.ID, "title", "body", "development")
		if err != nil || got.TargetBranch != "development" {
			t.Fatal(got, err)
		}
	}
	if p.creates != 1 || edits.items[info.Task.ID].ChangeRequest.TargetBranch != "development" {
		t.Fatal("custom task target not persisted/reused")
	}
	got, err := s.CreateBranchChangeRequest(context.Background(), i, "owner/repo", "existing/manual", "development", "manual branch", "body")
	if err != nil || got.SourceBranch != "existing/manual" || p.creates != 2 || p.merges != 0 {
		t.Fatal(got, err)
	}
}
