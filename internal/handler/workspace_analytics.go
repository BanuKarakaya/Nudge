package handler

import (
	"context"
	_ "embed"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"nudge/internal/repository"
)

//go:embed workspace_analytics.html
var workspaceAnalyticsHTML string

func (h *WorkspaceHandler) WithAnalytics(r repository.WorkspaceAnalyticsRepository) { h.analytics = r }

func (h *WorkspaceHandler) analyticsPage(w http.ResponseWriter, r *http.Request) {
	m, csrf, ok := h.session(w, r)
	if !ok {
		return
	}
	section := "usage"
	title := "Kullanım"
	description := "AI kullanımını, modellerini ve yanıt sürelerini takip et."
	if r.URL.Path == "/dashboard/operations" {
		section = "operations"
		title = "İşlemlerim"
		description = "Nudgy ile yaptığın işlemler ve gerçekleşen araç adımları."
	}
	data := workspacePage{Member: m, CSRF: csrf, Section: section, Title: title, Description: description}
	q := r.URL.Query()
	data.Range = q.Get("range")
	if data.Range == "" {
		data.Range = "7d"
	}
	durations := map[string]time.Duration{"24h": 24 * time.Hour, "7d": 7 * 24 * time.Hour, "30d": 30 * 24 * time.Hour, "90d": 90 * 24 * time.Hour}
	duration, valid := durations[data.Range]
	if !valid && data.Range != "all" {
		data.Error = "Geçersiz tarih aralığı."
		h.page(w, 400, data)
		return
	}
	data.Scope = q.Get("scope")
	if data.Scope == "" {
		data.Scope = "personal"
	}
	if data.Scope != "personal" && data.Scope != "workspace" {
		data.Error = "Geçersiz kullanım kapsamı."
		h.page(w, 400, data)
		return
	}
	if data.Scope == "workspace" && (!m.Manager || section != "usage") {
		data.Error = "Çalışma alanı toplamları yalnızca yöneticinin Kullanım ekranında gösterilir."
		h.page(w, 403, data)
		return
	}
	data.Status = q.Get("status")
	data.Model = strings.TrimSpace(q.Get("model"))
	statuses := map[string]bool{"": true, "success": true, "issues": true, "processing": true, "failed": true, "degraded": true}
	offset, err := strconv.Atoi(q.Get("offset"))
	if q.Get("offset") == "" {
		offset = 0
		err = nil
	}
	if err != nil || offset < 0 || offset > 1000000 || offset%25 != 0 || !statuses[data.Status] || len(data.Model) > 200 {
		data.Error = "Filtreler veya sayfa bilgisi geçersiz."
		h.page(w, 400, data)
		return
	}
	if section == "usage" {
		offset = 0
		data.Status = ""
	}
	to := time.Now()
	from := to.Add(-duration)
	if data.Range == "all" {
		from = time.Unix(0, 0)
	}
	if h.analytics == nil {
		data.Error = "Kullanım kayıtları şu an alınamıyor."
		h.page(w, 503, data)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	data.Stats, err = h.analytics.WorkspaceDashboard(ctx, m.UserID, data.Scope == "workspace", repository.DashboardFilters{From: from, To: to, Model: data.Model, Status: data.Status, Limit: 25, Offset: offset})
	if err != nil {
		data.Error = "Kayıtlar şu an alınamıyor. Yenileyerek tekrar dene."
		h.page(w, 503, data)
		return
	}
	if data.Stats.TotalMessages > 0 {
		data.ErrorRate = 100 * float64(data.Stats.ErrorCount) / float64(data.Stats.TotalMessages)
	}
	data.MaxDailyTokens = 1
	for _, day := range data.Stats.Daily {
		if day.Tokens > data.MaxDailyTokens {
			data.MaxDailyTokens = day.Tokens
		}
	}
	// Only query/filter values we explicitly support survive pagination.
	link := func(n int) string {
		values := url.Values{"range": {data.Range}, "status": {data.Status}, "model": {data.Model}, "offset": {strconv.Itoa(n)}}
		return "/dashboard/operations?" + values.Encode()
	}
	data.PageNumber = offset/25 + 1
	if offset > 0 {
		data.Previous = link(offset - 25)
	}
	if int64(offset+len(data.Stats.Recent)) < data.Stats.TotalMessages {
		data.Next = link(offset + 25)
	}
	h.page(w, 200, data)
}

func formatCount(n int64) string {
	s := strconv.FormatInt(n, 10)
	start := 0
	if n < 0 {
		start = 1
	}
	for i := len(s) - 3; i > start; i -= 3 {
		s = s[:i] + "." + s[i:]
	}
	return s
}
func formatMillis(v any) string {
	var ms float64
	switch n := v.(type) {
	case int64:
		ms = float64(n)
	case float64:
		ms = n
	}
	if ms < 1000 {
		return fmt.Sprintf("%.0f ms", ms)
	}
	return fmt.Sprintf("%.1f sn", ms/1000)
}
func formatActivityTime(t time.Time) string {
	return t.In(time.FixedZone("Europe/Istanbul", 3*3600)).Format("02.01.2006 15:04")
}
func activityStatus(s string) string {
	switch s {
	case "success":
		return "Başarılı"
	case "failed":
		return "Başarısız"
	case "degraded":
		return "Kısmen tamamlandı"
	case "processing":
		return "İşleniyor"
	default:
		return "Bilinmiyor"
	}
}
func activityLabel(s string) string {
	labels := map[string]string{"AGENT": "Nudgy işlemi", "SOCIAL_REPLY": "Sohbet yanıtı", "CONNECTION_LINK": "Hesap bağlantısı", "web_search": "Web araması", "rss_articles": "RSS içeriklerini getir", "answer": "Yanıt", "chat": "Yanıt", "coding": "Kod işlemi", "bookmark": "Bağlantı kaydet", "reply": "Yanıt", "message": "Mesaj", "unknown": "Nudgy isteği"}
	if v, ok := labels[s]; ok {
		return v
	}
	if s == "" {
		return "Nudgy isteği"
	}
	return strings.ReplaceAll(s, "_", " ")
}
