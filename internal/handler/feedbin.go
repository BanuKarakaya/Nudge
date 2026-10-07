package handler

import (
	"html/template"
	"net/http"
	"strconv"
	"strings"

	"nudge/internal/service"
)

type FeedbinHandler struct {
	service *service.FeedbinService
}

func NewFeedbinHandler(feedbinService *service.FeedbinService) *FeedbinHandler {
	return &FeedbinHandler{service: feedbinService}
}

func (h *FeedbinHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /feedbin/install", h.install)
	mux.HandleFunc("POST /feedbin/connect", h.connect)
}

func (h *FeedbinHandler) install(w http.ResponseWriter, r *http.Request) {
	userID, err := strconv.ParseInt(r.URL.Query().Get("user_id"), 10, 64)
	if err != nil || userID <= 0 {
		writeError(w, http.StatusBadRequest, "user_id must be a positive integer")
		return
	}
	writeFeedbinForm(w, userID, "")
}

func (h *FeedbinHandler) connect(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeError(w, http.StatusBadRequest, "invalid form")
		return
	}
	userID, err := strconv.ParseInt(r.FormValue("user_id"), 10, 64)
	if err != nil || userID <= 0 {
		writeError(w, http.StatusBadRequest, "user_id must be a positive integer")
		return
	}
	email := strings.TrimSpace(r.FormValue("email"))
	password := r.FormValue("password")
	if email == "" || password == "" {
		writeFeedbinForm(w, userID, "Email ve şifre alanlarını doldurmalısın.")
		return
	}
	if err := h.service.Connect(r.Context(), userID, email, password); err != nil {
		writeFeedbinForm(w, userID, "Feedbin bağlantısı kurulamadı. Bilgilerini kontrol edip tekrar dene.")
		return
	}
	writeFeedbinSuccess(w)
}

func writeFeedbinForm(w http.ResponseWriter, userID int64, errorMessage string) {
	const page = `<!doctype html>
<html lang="tr"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Feedbin’i Nudge’a bağla</title>
<style>
:root{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;color:#29211f}body{margin:0;min-height:100vh;display:grid;place-items:center;background:#fff7f5}.card{width:min(460px,calc(100% - 40px));box-sizing:border-box;padding:36px;background:#fff;border:1px solid #f4d8d2;border-radius:22px;box-shadow:0 18px 48px rgba(222,61,40,.12)}.brand{color:#de3d28;font-size:21px;font-weight:750;margin-bottom:24px}.icon{display:grid;place-items:center;width:58px;height:58px;margin-bottom:18px;border-radius:15px;background:#fff1ee;color:#de3d28;font-size:28px}h1{margin:0 0 10px;font-size:25px}p{color:#756967;line-height:1.5}.hint{font-size:13px;color:#a18d88}label{display:block;margin:18px 0 7px;font-size:14px;font-weight:650}input{box-sizing:border-box;width:100%;padding:13px 14px;border:1px solid #e2cbc6;border-radius:10px;font:inherit}button{width:100%;margin-top:24px;padding:13px;border:0;border-radius:10px;background:#de3d28;color:#fff;font:inherit;font-weight:700;cursor:pointer}.error{padding:11px 13px;border-radius:10px;background:#fff0ee;color:#a32b20;font-size:14px}</style></head>
<body><main class="card"><div class="brand">🔖 Nudge</div><div class="icon">📰</div><h1>Feedbin’i bağla</h1><p>Okunmamış RSS’lerini folder’larına göre gruplayıp her akşam sana gönderebilmemiz için Feedbin hesabını bağla.</p>{{if .Error}}<div class="error">{{.Error}}</div>{{end}}<form method="post" action="/feedbin/connect"><input type="hidden" name="user_id" value="{{.UserID}}"><label for="email">Feedbin email</label><input id="email" name="email" type="email" autocomplete="username" required><label for="password">Feedbin şifresi</label><input id="password" name="password" type="password" autocomplete="current-password" required><p class="hint">Mümkünse Feedbin’in uygulama şifresini kullan. Bu bilgiler yalnızca RSS’lerini okumak için kullanılır.</p><button type="submit">Feedbin’i bağla</button></form></main></body></html>`
	tmpl := template.Must(template.New("feedbin-form").Parse(page))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = tmpl.Execute(w, struct {
		UserID int64
		Error  string
	}{userID, errorMessage})
}

func writeFeedbinSuccess(w http.ResponseWriter) {
	const page = `<!doctype html><html lang="tr"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Feedbin bağlandı</title><style>body{margin:0;min-height:100vh;display:grid;place-items:center;background:#fff7f5;font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;color:#29211f}.card{width:min(460px,calc(100% - 40px));padding:42px 34px;text-align:center;background:#fff;border:1px solid #f4d8d2;border-radius:22px;box-shadow:0 18px 48px rgba(222,61,40,.12)}.icon{font-size:48px;margin-bottom:18px}h1{font-size:25px;margin:0 0 12px}p{color:#756967;line-height:1.55}</style></head><body><main class="card"><div class="icon">✅</div><h1>Feedbin başarıyla bağlandı</h1><p>Nudge artık okunmamış RSS’lerini folder’larına göre takip edebilir.</p><p>Bu pencereyi kapatabilirsin.</p></main></body></html>`
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(page))
}
