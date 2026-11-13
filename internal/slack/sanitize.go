package slack

import (
	"net/url"
	"regexp"
	"strings"
)

var slackMarkup = regexp.MustCompile(`<([^<>]*)>`)

// SanitizeGeneratedText never creates mentions from untrusted model output.
// Preserve useful links, but always expose the actual destination instead of
// allowing the model to disguise it behind a different label.
func SanitizeGeneratedText(text string) string {
	return slackMarkup.ReplaceAllStringFunc(text, func(markup string) string {
		body := markup[1 : len(markup)-1]
		target, _, _ := strings.Cut(body, "|")
		parsed, err := url.Parse(target)
		if err == nil && (parsed.Scheme == "https" || parsed.Scheme == "http") && parsed.Host != "" && parsed.User == nil && !strings.ContainsAny(target, "\r\n\t ") {
			return "<" + target + ">"
		}
		return "&lt;" + strings.ReplaceAll(strings.ReplaceAll(body, "&", "&amp;"), ">", "&gt;") + "&gt;"
	})
}
