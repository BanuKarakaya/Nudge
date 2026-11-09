package coding

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// The interpreter is stubbed here: these tests verify the authorization boundary,
// not the language model's accuracy. No provider network calls are made.
func TestSemanticMergeBindsBothProvidersAndKeepsRevisionGuard(t *testing.T) {
	for _, kind := range []string{"github", "gitlab"} {
		for _, request := range []string{"bunu mergele", "bu PR ı merge et", "mergeleme işlemini tekrar dener misin", "could you merge this please?"} {
			t.Run(kind+"/"+request, func(t *testing.T) {
				s, store, base, i := fixture(t)
				if kind == "gitlab" {
					s.kind = kind
					store.connection.Credentials = Credentials{Access: glToken, Scopes: "api", SelectedProjects: "[10]"}
					base.repos[0].Provider, base.repos[0].InstallationID = kind, 0
				}
				p := &changeProvider{fakeProvider: base, change: ChangeRequest{Number: 12, State: "open", Ready: true, HeadSHA: strings.Repeat("a", 40), TargetBranch: "development"}}
				s.provider, s.store = p, &pinStore{memoryStore: store}
				i.Request = request
				ctx := WithMergeIntentResolver(context.Background(), func(_ context.Context, got Identity) (MergeIntent, error) {
					if got != i {
						t.Fatal("lost signed identity")
					}
					return MergeIntent{Action: "merge", Provider: kind, Repository: "owner/repo", Number: 12}, nil
				})
				if _, err := s.MergeChangeRequest(ctx, i, "owner/repo", 13); !errors.Is(err, ErrMergeRequest) || p.reads != 0 {
					t.Fatal("agent substituted PR", err)
				}
				p.mergeErr = ErrUnavailable
				if _, err := s.MergeChangeRequest(ctx, i, "owner/repo", 12); !errors.Is(err, ErrUnavailable) || p.merges != 1 {
					t.Fatal("natural request did not reach merge", err)
				}
				p.change.HeadSHA = strings.Repeat("b", 40)
				if _, err := s.MergeChangeRequest(ctx, i, "owner/repo", 12); !errors.Is(err, ErrChangeMoved) || p.merges != 1 {
					t.Fatal("retry merged changed revision", err)
				}
			})
		}
	}
}

func TestSemanticDecisionCannotFallBackToCommandOrDifferentTarget(t *testing.T) {
	for _, tc := range []struct {
		intent MergeIntent
		err    error
	}{
		{MergeIntent{Action: "none"}, nil},
		{MergeIntent{Action: "clarify"}, nil},
		{MergeIntent{Action: "merge", Provider: "gitlab", Repository: "owner/repo", Number: 12}, nil},
		{MergeIntent{Action: "merge", Provider: "github", Repository: "owner/other", Number: 12}, nil},
		{MergeIntent{Action: "merge", Provider: "github", Repository: "owner/repo", Number: 12, TargetBranch: "development"}, nil},
		{MergeIntent{}, errors.New("resolver offline")},
	} {
		s, store, base, i := fixture(t)
		p := &changeProvider{fakeProvider: base}
		s.provider, s.store = p, &pinStore{memoryStore: store}
		i.Request = "merge https://github.com/owner/repo/pull/12"
		ctx := WithMergeIntentResolver(context.Background(), func(context.Context, Identity) (MergeIntent, error) { return tc.intent, tc.err })
		if _, err := s.MergeChangeRequest(ctx, i, "owner/repo", 12); err == nil || p.reads != 0 || p.merges != 0 {
			t.Fatal("rejected semantic decision bypassed via legacy grammar", tc.intent, err)
		}
	}
}

func TestSemanticBranchMergePreservesDirectionAndCreatesOnce(t *testing.T) {
	for _, kind := range []string{"github", "gitlab"} {
		t.Run(kind, func(t *testing.T) {
			s, store, base, i := fixture(t)
			if kind == "gitlab" {
				s.kind = kind
				store.connection.Credentials = Credentials{Access: glToken, Scopes: "api", SelectedProjects: "[10]"}
				base.repos[0].Provider, base.repos[0].InstallationID = kind, 0
			}
			sha := strings.Repeat("a", 40)
			p := &branchChangeProvider{changeProvider: &changeProvider{fakeProvider: base, change: ChangeRequest{Number: 12, State: "open", Ready: true, HeadSHA: sha}}, branches: map[string]string{"feature/login": sha, "development": strings.Repeat("b", 40)}}
			s.provider, s.store = p, &pinStore{memoryStore: store}
			i.Request = "bu sefer development'a alabilir misin bu değişikliği?"
			ctx := WithMergeIntentResolver(context.Background(), func(context.Context, Identity) (MergeIntent, error) {
				return MergeIntent{Action: "merge", Provider: kind, Repository: "owner/repo", SourceBranch: "feature/login", TargetBranch: "development"}, nil
			})
			for _, pair := range [][2]string{{"development", "feature/login"}, {"feature/login", "main"}} {
				if _, err := s.MergeBranches(ctx, i, "owner/repo", pair[0], pair[1]); !errors.Is(err, ErrBranchMerge) || p.creates != 0 {
					t.Fatal("agent changed direction", err)
				}
			}
			for n := 0; n < 2; n++ {
				got, err := s.MergeBranches(ctx, i, "owner/repo", "feature/login", "development")
				if err != nil || got.State != "merged" || p.creates != 1 || p.merges != 1 {
					t.Fatal("merge/replay failed", got, err)
				}
			}
		})
	}
}
