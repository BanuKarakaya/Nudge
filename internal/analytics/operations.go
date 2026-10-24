package analytics

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// Operation is an observed tool result, never a model's claim that it acted.
// Details must be safe application-generated summaries, not raw credentials,
// arguments, provider errors or article bodies. Future write adapters can attach
// a reviewed, secret-redacted diff after a confirmed write.
type Operation struct {
	Tool       string    `json:"tool"`
	Title      string    `json:"title"`
	Status     string    `json:"status"`
	Model      string    `json:"model"`
	StartedAt  time.Time `json:"started_at"`
	DurationMS int64     `json:"duration_ms"`
	Detail     string    `json:"detail"`
	Path       string    `json:"path,omitempty"`
	Diff       string    `json:"diff,omitempty"`
}

type OperationTracker struct {
	mu    sync.Mutex
	items []Operation
}

func (t *OperationTracker) Add(op Operation) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	// Two model stages can each execute up to 256 calls.
	if len(t.items) >= 512 {
		return
	}
	op.Tool = bounded(op.Tool, 100)
	op.Title = bounded(op.Title, 200)
	op.Model = bounded(op.Model, 200)
	op.Detail = bounded(op.Detail, 2000)
	op.Path = bounded(op.Path, 500)
	op.Diff = bounded(op.Diff, 16000)
	t.items = append(t.items, op)
}

func bounded(s string, limit int) string {
	r := []rune(s)
	if len(r) > limit {
		return string(r[:limit]) + "… [kısaltıldı]"
	}
	return s
}

func (t *OperationTracker) Snapshot() []Operation {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]Operation(nil), t.items...)
}

type operationContextKey struct{}

func WithOperationTracker(ctx context.Context, tracker *OperationTracker) context.Context {
	return context.WithValue(ctx, operationContextKey{}, tracker)
}
func OperationsFromContext(ctx context.Context) *OperationTracker {
	t, _ := ctx.Value(operationContextKey{}).(*OperationTracker)
	return t
}

// RecordTool deliberately extracts only typed numeric metadata from tool output.
// Unknown tools get no output capture until their logging policy is reviewed.
func RecordTool(ctx context.Context, tool, model string, started time.Time, success bool, output string) {
	t := OperationsFromContext(ctx)
	if t == nil {
		return
	}
	title := map[string]string{
		"rss_inventory":   "RSS envanteri sorgulandı",
		"rss_articles":    "RSS başlıkları / içerikleri getirildi",
		"bookmarks_list":  "Kaydedilen bağlantılar listelendi",
		"web_search":      "Web araması yapıldı",
		"handoff_to_luna": "Model devri istendi",
	}[tool]
	if title == "" {
		title = "Araç çağrısı"
	}
	op := Operation{Tool: tool, Title: title, Model: model, StartedAt: started, DurationMS: time.Since(started).Milliseconds(), Status: "success", Detail: "Araç çağrısı tamamlandı. Ham çıktı güvenlik nedeniyle kaydedilmez."}
	if !success {
		op.Status, op.Title = "failed", "Araç çağrısı başarısız / reddedildi"
		op.Detail = "İşlem tamamlanamadı. Bağlantı, araç parametreleri veya zaman aşımını kontrol et. Ham hata ve kimlik bilgileri kaydedilmez."
	} else if tool == "handoff_to_luna" {
		op.Detail = "Devir isteği kabul edildi; bu kayıt sonraki modelin yanıtı tamamladığı anlamına gelmez."
	} else if tool == "rss_inventory" || tool == "rss_articles" || tool == "bookmarks_list" {
		var result struct {
			Total      *int              `json:"total"`
			Offset     int               `json:"offset"`
			NextOffset *int              `json:"next_offset"`
			Articles   []json.RawMessage `json:"articles"`
			Bookmarks  []json.RawMessage `json:"bookmarks"`
		}
		if json.Unmarshal([]byte(output), &result) == nil && result.Total != nil {
			op.Detail = fmt.Sprintf("Eşleşen toplam kayıt: %d.", *result.Total)
			if tool != "rss_inventory" {
				count := len(result.Articles)
				if tool == "bookmarks_list" {
					count = len(result.Bookmarks)
				}
				op.Detail += fmt.Sprintf(" Bu çağrıda getirilen: %d. Başlangıç: %d.", count, result.Offset)
				if result.NextOffset != nil {
					op.Detail += fmt.Sprintf(" Sonraki sayfa: %d.", *result.NextOffset)
				}
			}
		}
	}
	t.Add(op)
}
