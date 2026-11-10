package handler

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"time"

	"nudge/internal/repository"
	"nudge/internal/secure"
	"nudge/internal/service"
	"nudge/internal/tracker"
)

//go:embed tracker_page.html
var trackerPageHTML string
var trackerPageTemplate = template.Must(template.New("tracker").Parse(trackerPageHTML))

type trackerPageData struct {
	Name, Kind, Title, Description, State, Account string
	Selecting, Success                             bool
	Resources                                      []tracker.Resource
}
type TrackerHandler struct {
	workspace     *WorkspaceHandler
	service       *tracker.Service
	installations repository.InstallationRepository
	client        codingMessenger
	links         secure.LinkSigner
	publicURL     string
}

func NewTrackerHandler(s *tracker.Service, installations repository.InstallationRepository, client codingMessenger, links secure.LinkSigner, base string) *TrackerHandler {
	return &TrackerHandler{service: s, installations: installations, client: client, links: links, publicURL: base}
}
func (h *TrackerHandler) WithWorkspacePortal(p *WorkspaceHandler) *TrackerHandler {
	h.workspace = p
	return h
}

func (h *TrackerHandler) page(w http.ResponseWriter, status int, data trackerPageData) {
	privatePageHeaders(w)
	if data.Selecting {
		// no-referrer makes browsers send Origin: null on HTML form POSTs.
		// Preserve the origin for CSRF checks without exposing callback paths or codes.
		w.Header().Set("Referrer-Policy", "strict-origin")
	}
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; img-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
	data.Name = h.service.Name()
	data.Kind = h.service.Kind()
	var body bytes.Buffer
	if trackerPageTemplate.Execute(&body, data) != nil {
		http.Error(w, "Sayfa gösterilemiyor", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(body.Bytes())
}
func (h *TrackerHandler) failure(w http.ResponseWriter, status int) {
	h.page(w, status, trackerPageData{Title: "Bağlantıyı yeniden başlatalım.", Description: "İzin verilmemiş, bağlantının süresi dolmuş veya erişimin değişmiş olabilir. Slack’te Nudgy’den yeni bir bağlantı iste. Önceki çalışan bağlantın korunur."})
}
func (h *TrackerHandler) RegisterRoutes(public, admin *http.ServeMux) {
	base := "/" + h.service.Kind()
	public.HandleFunc("GET "+base+"/install", h.install)
	public.HandleFunc("GET "+base+"/oauth/callback", h.callback)
	public.HandleFunc("POST "+base+"/select", h.selectResources)
	admin.HandleFunc("GET /api/v1/integrations"+base+"/connection", func(w http.ResponseWriter, r *http.Request) {
		privatePageHeaders(w)
		id, err := userIDFromHeader(r)
		if err != nil {
			writeError(w, 400, "invalid user")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
		defer cancel()
		installation, err := h.installations.GetByUserID(ctx, id)
		if err != nil {
			writeError(w, 404, "user not found")
			return
		}
		access, err := h.service.Access(ctx, tracker.Identity{UserID: id, Team: installation.TeamID})
		if err != nil {
			writeError(w, 403, "connection could not be verified")
			return
		}
		writeJSON(w, 200, access)
	})
	admin.HandleFunc("DELETE /api/v1/integrations"+base+"/connection", func(w http.ResponseWriter, r *http.Request) {
		id, err := userIDFromHeader(r)
		if err != nil {
			writeError(w, 400, "invalid user")
			return
		}
		if err := h.service.Disconnect(r.Context(), id); err != nil {
			writeError(w, 503, "disconnect failed")
			return
		}
		w.WriteHeader(204)
	})
}
func (h *TrackerHandler) install(w http.ResponseWriter, r *http.Request) {
	privatePageHeaders(w)
	id, err := strconv.ParseInt(r.URL.Query().Get("user_id"), 10, 64)
	if err != nil || !h.links.Verify(r.URL.Query().Get("token"), id, h.service.Kind(), time.Now()) {
		h.failure(w, 401)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	state, redirect, err := h.service.Begin(ctx, id)
	if err != nil {
		h.failure(w, 503)
		return
	}
	setOAuthCookie(w, r, "nudgy_"+h.service.Kind()+"_oauth", state, "/"+h.service.Kind()+"/oauth/callback")
	http.Redirect(w, r, redirect, http.StatusFound)
}
func (h *TrackerHandler) callback(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	if !consumeOAuthCookie(w, r, "nudgy_"+h.service.Kind()+"_oauth", state, "/"+h.service.Kind()+"/oauth/callback") || r.URL.Query().Get("error") != "" {
		h.failure(w, 400)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	next, access, err := h.service.Selection(ctx, state, r.URL.Query().Get("code"))
	if err != nil {
		h.failure(w, 400)
		return
	}
	if next == "" && h.workspace != nil {
		http.Redirect(w, r, "/workspace?done=verified", http.StatusSeeOther)
		return
	}
	setOAuthCookie(w, r, "nudgy_"+h.service.Kind()+"_selection", next, "/"+h.service.Kind()+"/select")
	h.page(w, 200, trackerPageData{Selecting: true, Title: "Nudgy hangi alanlarda çalışsın?", Description: "Nudgy’nin erişebileceği alanları seç. Seçimini kaydettiğinde bağlantın tamamlanır.", State: next, Account: access.Account.Name, Resources: access.Resources})
}
func (h *TrackerHandler) selectResources(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" || (r.Header.Get("Origin") != "" && r.Header.Get("Origin") != h.publicURL) {
		h.page(w, http.StatusForbidden, trackerPageData{Title: "Bağlantı formu doğrulanamadı.", Description: "Form gönderimi güvenlik kontrolünden geçemedi. Slack’te Nudgy’den yeni bir bağlantı iste ve izin verme ile seçim adımlarını aynı tarayıcıda tamamla."})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 65536)
	if r.ParseForm() != nil {
		h.failure(w, 400)
		return
	}
	state := r.PostForm.Get("state")
	if !consumeOAuthCookie(w, r, "nudgy_"+h.service.Kind()+"_selection", state, "/"+h.service.Kind()+"/select") {
		h.failure(w, 400)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	account, err := h.service.Complete(ctx, state, r.PostForm["resource"])
	if err != nil {
		h.failure(w, 400)
		return
	}
	h.page(w, 200, trackerPageData{Success: true, Title: h.service.Name() + " hesabın bağlandı.", Description: "Seçtiğin alanlardaki işleri artık Slack’ten takip edebilirsin. İlk sorunu sor, birlikte bakalım.", Account: account.Name})
}
func (h *TrackerHandler) Tools(base service.AgentTools, id tracker.Identity, requestID ...string) service.AgentTools {
	return service.NewTrackerTools(base, h.service, id, func(ctx context.Context) error {
		if h.workspace != nil {
			return h.workspace.SendLink(ctx, id.UserID, id.Team)
		}
		if err := h.service.Authorize(ctx, id); err != nil {
			return err
		}
		installation, err := h.installations.GetByUserID(ctx, id.UserID)
		if err != nil || installation.TeamID != id.Team {
			return tracker.ErrDenied
		}
		link, err := h.links.URL(h.publicURL, id.UserID, h.service.Kind(), time.Now())
		if err != nil {
			return err
		}
		channel, err := h.client.OpenDirectMessage(ctx, installation.BotToken, installation.SlackUserID)
		if err != nil {
			return err
		}
		return h.client.PostMessage(ctx, installation.BotToken, channel, fmt.Sprintf("🔗 *Nudgy · %s bağlantısı*\n<%s|%s hesabını bağla>\nHesabına izin verdikten sonra Nudgy’nin erişebileceği alanları seç. Bu özel bağlantı 20 dakika geçerlidir.", h.service.Name(), link, h.service.Name()))
	}, requestID...)
}
func (h *SlackEventHandler) WithTracker(handler *TrackerHandler) *SlackEventHandler {
	h.trackers = append(h.trackers, handler)
	return h
}
