package analytics

import (
	"strings"
	"testing"
)

func TestTraceRedactionIncludesNestedToolJSON(t *testing.T) {
	raw := []byte(`{"authorization":"header-secret","input":[{"type":"function_call_output","output":"{\"password\":\"nested-secret\",\"total\":42}"},{"text":"Bearer bearer-secret https://user:pass@example.com/callback?token=signed-secret"}],"usage":{"input_tokens":123}}`)
	result := RedactJSON(raw)
	for _, secret := range []string{"header-secret", "nested-secret", "bearer-secret", "signed-secret", "user:pass"} {
		if strings.Contains(result, secret) {
			t.Fatal("trace leaked credential", secret)
		}
	}
	if !strings.Contains(result, "123") || !strings.Contains(result, "42") {
		t.Fatal("redaction destroyed ordinary data")
	}
}
