package service

import (
	"strings"
	"unicode"
)

// Match the entire message. A thanks followed by a question must still reach
// the assistant with its conversation context.
func ImmediateSocialReply(request string) (string, bool) {
	text := strings.ToLower(strings.TrimSpace(request))
	text = strings.TrimRightFunc(text, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsPunct(r) || r == '😊' || r == '🙏'
	})
	text = strings.Join(strings.Fields(text), " ")
	switch text {
	case "teşekkürler", "tesekkurler", "teşekkür ederim", "tesekkur ederim", "çok teşekkürler", "cok tesekkurler", "çok teşekkür ederim", "sağ ol", "sağol", "sag ol", "sagol":
		return "Rica ederim! 😊", true
	case "selam", "merhaba":
		return "Selam! Nasıl yardımcı olabilirim?", true
	case "nasılsın", "nasilsin", "selam nasılsın", "merhaba nasılsın":
		return "İyiyim, teşekkür ederim! Sen nasılsın?", true
	case "thanks", "thank you":
		return "You're welcome! 😊", true
	}
	return "", false
}
