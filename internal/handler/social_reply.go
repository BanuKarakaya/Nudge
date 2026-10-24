package handler

import "strings"

// Match the entire message: a greeting followed by a task must reach the agent.
// Do not strip quotes, Slack markup, or arbitrary punctuation from user content.
func localSocialReply(message string) string {
	text := strings.ToLower(message)
	text = strings.NewReplacer("ı", "i", "ş", "s", "ğ", "g", "ü", "u", "ö", "o", "ç", "c").Replace(text)
	text = strings.Join(strings.Fields(text), " ")
	text = strings.TrimRight(text, " .!?")
	switch text {
	case "tesekkurler", "tesekkur ederim", "cok tesekkurler", "cok tesekkur ederim", "tesekkur ediyorum", "sag ol", "sagol", "sag olun", "eyvallah", "iyiyim tesekkurler", "iyiyim tesekkur ederim":
		return "Rica ederim! 😊"
	case "nasilsin", "nasilsiniz", "selam nasilsin", "merhaba nasilsin", "selam, nasilsin", "merhaba, nasilsin", "nasilsin nudgy", "nasilsin nudge":
		return "İyiyim, teşekkür ederim! Sen nasılsın?"
	case "selam", "merhaba", "selamlar", "gunaydin", "iyi aksamlar", "iyi gunler":
		return "Selam! Nasıl yardımcı olabilirim?"
	case "thanks", "thank you", "thanks a lot":
		return "You're welcome! 😊"
	case "hi", "hello", "hey":
		return "Hi! How can I help?"
	case "how are you", "hi how are you", "hello how are you":
		return "I'm doing well, thanks! How are you?"
	default:
		return ""
	}
}
