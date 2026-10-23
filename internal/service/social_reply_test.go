package service

import "testing"

func TestImmediateSocialReplyMatchesWholeMessage(t *testing.T) {
	for _, request := range []string{"teşekkürler", "TEŞEKKÜRLER! 😊", "  teşekkür   ederim  ", "tesekkurler 🙏", "nasılsın?"} {
		if reply, ok := ImmediateSocialReply(request); !ok || reply == "" {
			t.Errorf("missing reply for %q", request)
		}
	}
	for _, request := range []string{"teşekkürler, rss titlelarımı da ver", "nasılsın bir de rss sayım kaç", "sana teşekkürler dedim iki saattir cevap veremedin", "teşekkürler ne demek", ""} {
		if _, ok := ImmediateSocialReply(request); ok {
			t.Errorf("substantive request intercepted: %q", request)
		}
	}
}
