package coding

import (
	"context"
	"strings"
	"testing"
)

func TestSlackCopiedMergeCommandPreservesExplicitIntent(t *testing.T) {
	r := Repository{ID: 10, InstallationID: 2, FullName: "BanuKarakaya/toDoList"}
	link := "https://github.com/BanuKarakaya/toDoList/pull/3"
	for _, request := range []string{
		"`<" + link + "> merge et`", // Exact Slack event.text from the failed request.
		"`" + link + "` merge et", "`merge " + link + "`",
		"<" + link + "|PR #3> merge et", "`&lt;" + link + "&gt; merge et`",
	} {
		if !ExplicitMerge(request, r, 3) {
			t.Errorf("formatted command rejected: %q", request)
		}
		if ExplicitMerge(request, r, 4) {
			t.Fatal("formatting changed PR authority")
		}
	}
	for _, request := range []string{
		"> `" + link + " merge et`", "örnek: `" + link + " merge et`",
		"\"" + link + " merge et\"", "```" + link + " merge et```",
		"`" + link + " merge et", "`" + link + " merge et` yapma",
		"`" + link + " merge et`\nbaşka işlem", "`" + link + " merge et` değil",
		"<https://github.com/other/repo/pull/3|" + link + "> merge et",
	} {
		if ExplicitMerge(request, r, 3) {
			t.Errorf("non-command accepted: %q", request)
		}
	}
}

func TestFormattedURLMergeReachesProviderAndRemainsPinned(t *testing.T) {
	s, store, base, i := fixture(t)
	p := &changeProvider{fakeProvider: base, change: ChangeRequest{Number: 12, State: "open", Ready: true, HeadSHA: strings.Repeat("a", 40), TargetBranch: "development"}}
	s.provider = p
	s.store = &pinStore{memoryStore: store}
	i.Request = "`<https://github.com/owner/repo/pull/12> merge et`"
	result, err := s.MergeChangeRequest(context.Background(), i, "owner/repo", 12)
	if err != nil || result.State != "merged" || p.merges != 1 {
		t.Fatal(result, err)
	}
	if _, err := s.MergeChangeRequest(context.Background(), i, "owner/repo", 12); err != nil || p.merges != 1 {
		t.Fatal("replayed formatted merge", err)
	}
}

func TestSlackExplicitSourceBranchRequest(t *testing.T) {
	source := "nudgy/banukarakaya/githubdaki-todolist-projemde-yeni-bi-ekran-olust-0cce518bcd560ba0492da8cc81bff7d8"
	request := "`" + source + "` bu branchi development branchine mergeleyebilir misin"
	i, ok := parseBranchMerge(request)
	if !ok || !i.Current || i.Source != source || i.Target != "development" {
		t.Fatal(i, ok)
	}
	polite, ok := parseBranchMerge("Lütfen bu dalı development dalına merge et")
	if !ok || !polite.Current || polite.Source != "" {
		t.Fatal("courtesy interpreted as source", polite, ok)
	}
	for _, prefix := range []string{"sakın", "asla", "örneğin", "example", ">", "&gt;"} {
		if _, ok := parseBranchMerge(prefix + " bu dalı development dalına merge et"); ok {
			t.Fatal("non-command prefix accepted", prefix)
		}
	}
	for _, request := range []string{"örnek: " + request, request + " ama yapma", "> " + request, strings.Replace(request, "mergeleyebilir misin", "mergeleme", 1)} {
		if _, ok := parseBranchMerge(request); ok {
			t.Fatal("non-command authorized", request)
		}
	}
}
