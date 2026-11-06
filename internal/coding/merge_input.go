package coding

import (
	"regexp"
	"strings"
)

var mergeInlineCode = regexp.MustCompile("`([^`\\r\\n]+)`")

// Slack preserves inline-code markers in event.text (including around a whole
// copied command). Strip presentation only; callers still match the entire
// current signed message against an anchored, action-specific grammar. Fences,
// blockquotes, example prose, conditions and additional commands are not consent.
func normalizeMergeText(request string) string {
	text := strings.TrimSpace(request)
	if strings.ContainsAny(text, "\r\n") || strings.Contains(text, "``") || strings.Count(text, "`")%2 != 0 {
		return ""
	}
	text = mergeInlineCode.ReplaceAllString(text, "$1")
	text = strings.NewReplacer("&lt;", "<", "&gt;", ">", "&amp;", "&").Replace(text)
	if strings.HasPrefix(strings.TrimSpace(text), ">") {
		return ""
	}
	return strings.Join(strings.Fields(text), " ")
}
