package handler

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"net/http"
	"net/url"
	"nudge/internal/repository"
	"nudge/internal/workspace"
	"strconv"
	"strings"
	"time"
)

//go:embed monitor.html
var monitorHTML string

func (h *WorkspaceHandler) WithMonitor(store repository.MonitorRepository) { h.monitor = store }
func (h *WorkspaceHandler) monitorData(w http.ResponseWriter, r *http.Request) {
	privatePageHeaders(w)
	c, err := r.Cookie(workspaceCookie)
	if err != nil || len(c.Value) != 64 {
		writeError(w, 401, "session expired")
		return
	}
	user, err := h.store.SessionUser(r.Context(), workspaceHash(c.Value))
	if err != nil {
		writeError(w, 401, "session expired")
		return
	}
	m, err := h.store.Member(r.Context(), user)
	if err != nil {
		writeError(w, 401, "session expired")
		return
	}
	h.serveMonitor(w, r, m, "", true)
}
func (h *WorkspaceHandler) monitorPage(w http.ResponseWriter, r *http.Request) {
	m, csrf, ok := h.session(w, r)
	if !ok {
		return
	}
	h.serveMonitor(w, r, m, csrf, false)
}
func (h *WorkspaceHandler) serveMonitor(w http.ResponseWriter, r *http.Request, m workspace.Member, csrf string, fragment bool) {
	section := "overview"
	if r.URL.Path == "/dashboard/usage" {
		section = "usage"
	}
	if r.URL.Path == "/dashboard/operations" {
		section = "operations"
	}
	if fragment {
		section = r.URL.Query().Get("section")
	}
	data := workspacePage{Member: m, CSRF: csrf, Section: section, MonitorEnabled: true, Range: r.URL.Query().Get("range"), Scope: r.URL.Query().Get("scope"), Status: r.URL.Query().Get("status"), Model: strings.TrimSpace(r.URL.Query().Get("model")), MonitorUser: r.URL.Query().Get("user")}
	fail := func(code int, message string) {
		if fragment {
			writeError(w, code, message)
		} else {
			data.Error = message
			h.page(w, code, data)
		}
	}
	if section != "overview" && section != "operations" && section != "usage" {
		fail(400, "Geçersiz ekran.")
		return
	}
	if h.monitor == nil {
		fail(503, "İzleme kayıtları kullanılamıyor.")
		return
	}
	if data.Range == "" {
		data.Range = "7d"
	}
	if data.Scope == "" {
		data.Scope = "personal"
		if m.Manager {
			data.Scope = "workspace"
		}
	}
	if data.Scope != "personal" && data.Scope != "workspace" {
		fail(400, "Geçersiz kapsam.")
		return
	}
	if data.Scope == "workspace" && !m.Manager {
		fail(403, "Ekip kayıtları için yönetici erişimi gerekli.")
		return
	}
	duration, ok := map[string]time.Duration{"24h": 24 * time.Hour, "7d": 7 * 24 * time.Hour, "30d": 30 * 24 * time.Hour, "90d": 90 * 24 * time.Hour}[data.Range]
	if !ok && data.Range != "all" {
		fail(400, "Geçersiz dönem.")
		return
	}
	statusOK := map[string]bool{"": true, "queued": true, "running": true, "completed": true, "failed": true, "active": true, "unanswered": true, "issues": true}
	offset := 0
	var err error
	if r.URL.Query().Get("offset") != "" {
		offset, err = strconv.Atoi(r.URL.Query().Get("offset"))
	}
	if err != nil {
		fail(400, "Geçersiz sayfa.")
		return
	}
	job := int64(0)
	if v := r.URL.Query().Get("job"); v != "" {
		job, err = strconv.ParseInt(v, 10, 64)
		if job == 0 {
			fail(400, "Geçersiz istek numarası.")
			return
		}
	}
	if err != nil || offset < 0 || offset > 1000000 || offset%25 != 0 || !statusOK[data.Status] || len(data.Model) > 200 || len(data.MonitorUser) > 100 {
		fail(400, "Geçersiz filtre.")
		return
	}
	if data.Scope == "personal" {
		data.MonitorUser = ""
	}
	to := time.Now()
	from := to.Add(-duration)
	if data.Range == "all" {
		from = time.Unix(0, 0)
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if section == "overview" && h.ai != nil {
		data.AI, err = h.ai.AISettings(ctx, m.UserID)
		if err != nil {
			fail(503, "AI ayarları şu an okunamıyor. Yenileyerek tekrar dene.")
			return
		}
		data.SetupNeeded = !data.AI.Ready()
	}
	data.Monitor, err = h.monitor.Monitor(ctx, m.UserID, data.Scope == "workspace", repository.MonitorFilters{From: from, To: to, Status: data.Status, Model: data.Model, User: data.MonitorUser, JobID: job, Offset: offset})
	if err != nil {
		fail(503, "İzleme verileri alınamadı. Son görüntü güncel olmayabilir; yeniden dene.")
		return
	}
	data.MonitorJob = job
	data.PageNumber = offset/25 + 1
	data.Title = map[string]string{"overview": "Genel bakış", "operations": "Canlı işlemler ve loglar", "usage": "Kullanım ve modeller"}[section]
	data.Description = "İsteklerin durumunu, gerçekleşen adımları ve kullanımını canlı takip et."
	data.MaxDailyTokens = 1
	data.MaxDailyRequests = 1
	for _, d := range data.Monitor.Daily {
		data.MaxDailyTokens = max(data.MaxDailyTokens, d.Tokens)
		data.MaxDailyRequests = max(data.MaxDailyRequests, d.Messages)
	}
	if data.Monitor.Requests > 0 {
		data.ErrorRate = 100 * float64(data.Monitor.IssueRequests) / float64(data.Monitor.Requests)
	}
	path := "/dashboard"
	if section != "overview" {
		path += "/" + section
	}
	query := url.Values{"section": {section}, "range": {data.Range}, "scope": {data.Scope}, "status": {data.Status}, "model": {data.Model}, "user": {data.MonitorUser}}
	if job != 0 {
		query.Set("job", strconv.FormatInt(job, 10))
	}
	if offset > 0 {
		query.Set("offset", strconv.Itoa(offset-25))
		data.Previous = path + "?" + query.Encode()
	}
	if int64(offset+len(data.Monitor.Recent)) < data.Monitor.FilteredRequests {
		query.Set("offset", strconv.Itoa(offset+25))
		data.Next = path + "?" + query.Encode()
	}
	query.Set("offset", strconv.Itoa(offset))
	data.MonitorURL = "/dashboard/live/data?" + query.Encode()
	if fragment {
		var b bytes.Buffer
		if workspaceTemplate.ExecuteTemplate(&b, "monitor-fragment", data) != nil {
			writeError(w, 500, "İzleme ekranı hazırlanamadı.")
			return
		}
		writeJSON(w, 200, map[string]string{"html": b.String(), "updated_at": data.Monitor.UpdatedAt.Format(time.RFC3339)})
		return
	}
	h.page(w, 200, data)
}
func monitorStage(stage string) string {
	labels := map[string]string{"queued": "Kuyrukta", "worker_started": "İşleyici başladı", "identity_lookup": "Slack kullanıcısı doğrulanıyor", "installation_lookup": "Çalışma alanı bağlantısı doğrulanıyor", "history_loading": "Thread geçmişi yükleniyor", "agent_starting": "Nudgy hazırlanıyor", "model_starting": "Model bağlantısı açılıyor", "model_running": "Model yanıt üretiyor", "model_handoff": "Model değiştiriliyor", "tool_started": "Araç çalışıyor", "tool_completed": "Araç tamamlandı", "tool_failed": "Araç başarısız", "approval_waiting": "Slack onayı istendi; işlem henüz çalışmadı", "action_starting": "Onaylı işlem çalışıyor", "action_completed": "Onaylı işlem tamamlandı", "action_failed": "Onaylı işlem başarısız", "response_ready": "Yanıt hazır", "delivery_started": "Slack’e gönderiliyor", "delivery_failed": "Slack’e gönderilemedi", "delivery_completed": "Slack yanıtı teslim edildi", "attempt_failed": "Deneme başarısız", "usage": "Model kullanımı kaydedildi", "legacy": "Eski kayıt · canlı log yok"}
	if label, ok := labels[stage]; ok {
		return label
	}
	return "İşlem aşaması bildirilmedi"
}
func monitorError(code string) string {
	labels := map[string]string{"workspace_link_failed": "Dashboard bağlantısı özel mesaja gönderilemedi; Slack bot erişimini kontrol edip yeniden dene.", "rss_connection_required": "Bu kullanıcının Feedbin bağlantısı yok; hesabını bağlaması gerekiyor.", "rss_credentials_unavailable": "Kayıtlı Feedbin hesabı sunucuda okunamadı; veritabanı ve şifreleme ayarlarını kontrol et.", "rss_access_denied": "Feedbin hesap erişimini reddetti (401/403); hesabını kontrol edip bağlantıyı yenile.", "rss_rate_limited": "Feedbin istek sınırına ulaşıldı (429); bir süre sonra yeniden dene.", "rss_unavailable": "RSS servisine erişilemedi veya verisi alınamadı; servis ve ağ durumunu kontrol et.", "workspace_busy": "Çalışma alanının eşzamanlı AI iş sınırına ulaşıldı.", "daily_quota": "Günlük AI token kotası doldu; kota UTC gece yarısında yenilenir.", "token_limit": "Bu çalıştırmanın token sınırına ulaşıldı.", "runtime_start_failed": "Model çalıştırıcısı başlatılamadı; sunucu kurulumunu kontrol et.", "runtime_storage_failed": "Modelin çalışma dizinine erişilemiyor; sunucu disk ve dosya izinlerini kontrol et.", "timeout": "Zaman sınırı aşıldı.", "interrupted": "İşlem kesildi; yeniden başlatma veya iptal olabilir.", "ai_not_configured": "AI ayarları eksik. API anahtarı ve modeli kontrol et.", "access_denied": "Hesap veya çalışma alanı erişimi doğrulanamadı.", "lease_lost": "Bu işin sahipliği sona erdi; eski deneme durduruldu.", "slack_delivery_failed": "Yanıt Slack’e ulaştırılamadı. Bot erişimini, kanal üyeliğini ve Slack bağlantısını kontrol et.", "worker_panic": "İşleyici beklenmedik şekilde durdu.", "runtime_disconnected": "Model çalıştırıcısının bağlantısı yanıt tamamlanmadan kapandı.", "model_failed": "Model yanıt üretimini tamamlayamadı; sağlayıcı erişimini ve kotayı kontrol et.", "processing_failed": "İşlem tamamlanamadı. Kesin neden bildirilmedi; son aşamayı ve yeniden denemeleri incele."}
	if v, ok := labels[code]; ok {
		return v
	}
	if code == "" || code == "none" {
		return ""
	}
	return "Araç işlemi reddedildi veya tamamlanamadı (" + code + "). Bağlantı ve erişim ayarlarını kontrol et."
}
func monitorStatusLabel(s string) string {
	switch s {
	case "queued":
		return "Sırada"
	case "running":
		return "Çalışıyor"
	case "completed":
		return "Tamamlandı"
	case "failed":
		return "Hata"
	}
	return "Bilinmiyor"
}
func monitorTime(t time.Time) string {
	return t.In(time.FixedZone("Europe/Istanbul", 3*3600)).Format("02.01 15:04:05")
}
func monitorRequestLabel(id int64) string {
	if id < 0 {
		return fmt.Sprintf("Eski kayıt #%d", -id)
	}
	return fmt.Sprintf("İstek #%d", id)
}
