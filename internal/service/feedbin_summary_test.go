package service

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

type sequenceSummarizer struct {
	calls   int
	outputs []string
}

func (s *sequenceSummarizer) Summarize(context.Context, string) (string, error) {
	s.calls++
	if s.calls > len(s.outputs) {
		return "", fmt.Errorf("unexpected extra call")
	}
	return s.outputs[s.calls-1], nil
}

func TestSummaryRetriesInvalidFormatAndNeverReturnsTitles(t *testing.T) {
	input := "[RSS-1]\nBaşlık: Example\nURL: https://example.com/article\n"
	valid := `{"articles":[{"id":"RSS-1","topic":"Konu.","importance":"Önemli."}]}`
	for _, success := range []bool{true, false} {
		second := "invalid again"
		if success {
			second = valid
		}
		ai := &sequenceSummarizer{outputs: []string{"invalid", second}}
		output, err := summarizeFeedbinInput(context.Background(), ai, input)
		if ai.calls != 2 {
			t.Fatalf("expected retry; calls=%d", ai.calls)
		}
		if success && (err != nil || !strings.Contains(output, "Neden önemli:")) {
			t.Fatalf("summary missing: %s %v", output, err)
		}
		if !success && (err == nil || output != "") {
			t.Fatalf("failure must not produce title fallback: %s %v", output, err)
		}
	}
}

func TestRenderFeedbinSummaryRejectsIncompleteOutput(t *testing.T) {
	input := "[RSS-1]\nBaşlık: Example\nURL: https://example.com/article\n"
	cases := []string{
		`{"articles":[{"id":"RSS-1","topic":"Konu.","importance":"Yarım`,
		`{"articles":[{"id":"RSS-1","topic":"Konu.","importance":"Yarım cümle"}]}`,
		`{"articles":[{"id":"RSS-99","topic":"Konu.","importance":"Önemli."}]}`,
		"Konu: Test.\nNeden önemli: Test.\n[RSS-1",
	}
	for _, output := range cases {
		if _, ok := RenderFeedbinAISummary(output, input); ok {
			t.Errorf("accepted incomplete/invalid summary: %s", output)
		}
	}
	output, ok := RenderFeedbinAISummary(`{"articles":[{"id":"RSS-1","topic":"Konu.","importance":"Önemli."}]}`, input)
	if !ok || !strings.Contains(output, "<https://example.com/article|Example>") || strings.Contains(output, "RSS-1") {
		t.Fatalf("invalid rendering: %s", output)
	}
}

func TestFormatFeedbinCategoryStatsShowsEachFolderAndTotal(t *testing.T) {
	groups := feedbinGroups{
		"AI":      {{ID: 1, Title: "one"}, {ID: 2, Title: "two"}},
		"Backend": {{ID: 3, Title: "three"}},
		"Diğer":   {{ID: 4, Title: "four"}, {ID: 5, Title: "five"}, {ID: 6, Title: "six"}},
	}
	output := formatFeedbinCategoryStats(groups)
	for _, expected := range []string{
		"*AI*: 2 okunmamış RSS",
		"*Backend*: 1 okunmamış RSS",
		"*Diğer*: 3 okunmamış RSS",
		"*Toplam benzersiz*: 6 okunmamış RSS",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("missing %q in %s", expected, output)
		}
	}
}
