package slack

import (
	"strings"
	"testing"
)

func TestGeneratedTextCannotMentionOrHideLinkTargets(t *testing.T) {
	got := SanitizeGeneratedText("<!channel> <@U123> <#C123|general> <https://evil.test|https://trusted.test> <https://ok.test/a>")
	for _, bad := range []string{"<!", "<@", "<#", "trusted.test"} {
		if strings.Contains(got, bad) {
			t.Fatal(got)
		}
	}
	if !strings.Contains(got, "<https://evil.test>") || !strings.Contains(got, "<https://ok.test/a>") {
		t.Fatal(got)
	}
}
