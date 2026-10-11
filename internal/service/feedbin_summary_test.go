package service

import (
	"strings"
	"testing"
)

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
