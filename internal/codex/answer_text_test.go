package codex

import "testing"

func TestProviderTextEnvelopeIsRenderedWithoutJSONWrapper(t *testing.T) {
	raw := `[{"text":"*Şirket içi uygulama*\nEntegrasyonlar → Özel bağlantıları yönet.","type":"text"}]`
	result, err, _ := consumePackets(t, nil, completedPacket(t, "thread-1", "turn-1", "completed", agentItem{Type: "agentMessage", Phase: "final_answer", Text: raw}))
	if err != nil || result.Text != "*Şirket içi uygulama*\nEntegrasyonlar → Özel bağlantıları yönet." {
		t.Fatal("content envelope reached Slack", result.Text, err)
	}
	for _, text := range []string{
		`[{"title":"An actual JSON result"}]`,
		"```json\n" + raw + "\n```",
		`[{"type":"text","text":"example","extra":true}]`,
		`[{"type":"text","text":"example"},{"type":"image","url":"https://example.test"}]`,
		`[{"type":"text","text":""}]`,
		`[{"type":"text","text":"example"}] trailing`,
	} {
		if answerText(text) != text {
			t.Fatal("ordinary or mixed content rewritten", text)
		}
	}
}
