package analytics

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestOperationMetadataIsBoundedAndSafe(t *testing.T) {
	tracker := &OperationTracker{}
	ctx := WithOperationTracker(context.Background(), tracker)
	RecordTool(ctx, "rss_articles", "model", time.Now(), true, `{"total":120,"offset":50,"next_offset":100,"articles":[{"title":"private-title","content":"secret-body","url":"https://example.com?token=secret"}],"token":"secret-key"}`)
	RecordTool(ctx, "web_search", "model", time.Now(), false, "secret-error")
	RecordTool(ctx, "unknown", "model", time.Now(), true, "secret-output")
	ops := tracker.Snapshot()
	if len(ops) != 3 || !strings.Contains(ops[0].Detail, "getirilen: 1") || !strings.Contains(ops[0].Detail, "toplam kayıt: 120") || ops[1].Status != "failed" {
		t.Fatalf("unexpected trace: %+v", ops)
	}
	raw, _ := json.Marshal(ops)
	if strings.Contains(string(raw), "secret") || strings.Contains(string(raw), "private-title") {
		t.Fatal("raw data leaked")
	}
	ops[0].Detail = "modified"
	if tracker.Snapshot()[0].Detail == "modified" {
		t.Fatal("snapshot aliases stored data")
	}
	tracker.Add(Operation{Detail: strings.Repeat("ı", 9000), Diff: strings.Repeat("+", 20000)})
	last := tracker.Snapshot()[3]
	if len([]rune(last.Detail)) > 2020 || len(last.Diff) > 16030 {
		t.Fatal("trace not bounded")
	}
}

func TestOperationTrackerConcurrencyAndIsolation(t *testing.T) {
	tracker := &OperationTracker{}
	var wg sync.WaitGroup
	for i := 0; i < 600; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); tracker.Add(Operation{Tool: "tool"}) }()
	}
	wg.Wait()
	if len(tracker.Snapshot()) != 512 {
		t.Fatal("missing bound")
	}
	other := &OperationTracker{}
	if len(other.Snapshot()) != 0 {
		t.Fatal("cross-request leak")
	}
	RecordTool(context.Background(), "rss_inventory", "m", time.Now(), true, `{"total":1}`)
}
