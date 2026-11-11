package handler

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"nudge/internal/coding"
	"nudge/internal/dashboard"
	"nudge/internal/repository"
	"nudge/internal/secure"
	"nudge/internal/tracker"
	"nudge/internal/workspace"
)

//go:embed workspace_page.html
var workspaceHTML string
var workspaceTemplate = template.Must(template.New("workspace").Parse(workspaceHTML))

const workspaceCookie = "nudgy_workspace"

type workspaceProvider struct {
	Kind, Name  string
	Connected   func(context.Context, int64) (bool, error)
	AccountName func(context.Context, int64) (string, error)
	Disconnect  func(context.Context, int64) error
	Resources   func(context.Context, workspace.Member) ([]workspace.Resource, error)
}
type WorkspaceHandler struct {
	store         workspace.Store
	links         secure.LinkSigner
	base          string
	installations repository.InstallationRepository
	client        codingMessenger
	providers     []workspaceProvider
	ai            workspace.AIStore
	catalog       workspace.Catalog
	slackSignIn   bool
}

func NewWorkspaceHandler(store workspace.Store, links secure.LinkSigner, base string, installations repository.InstallationRepository, client codingMessenger) *WorkspaceHandler {
	return &WorkspaceHandler{store: store, links: links, base: strings.TrimRight(base, "/"), installations: installations, client: client}
}

// Validate checks the canonical origin used for CSRF and secure session cookies.
func (h *WorkspaceHandler) Validate() error {
	u, err := url.Parse(h.base)
	if err != nil || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("workspace panel requires a NUDGE_PUBLIC_URL origin")
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1")) {
		return errors.New("workspace panel requires HTTPS outside localhost")
	}
	return nil
}
func (h *WorkspaceHandler) RegisterCoding(s *coding.Service) {
	s.WithWorkspacePolicy(h.store)
	h.providers = append(h.providers, workspaceProvider{Kind: s.Kind(), Name: s.DisplayName(), Connected: s.Connected, AccountName: s.StoredAccountName, Disconnect: s.Disconnect, Resources: func(ctx context.Context, m workspace.Member) ([]workspace.Resource, error) {
		a, err := s.SetupAccess(ctx, coding.Identity{UserID: m.UserID, Team: m.Team})
		if err != nil {
			return nil, err
		}
		out := []workspace.Resource{}
		for _, r := range a.Repositories {
			out = append(out, workspace.Resource{ID: strconv.FormatInt(r.ID, 10), Name: r.FullName})
		}
		return out, nil
	}})
}
func (h *WorkspaceHandler) RegisterTracker(s *tracker.Service) {
	s.WithWorkspacePolicy(h.store)
	h.providers = append(h.providers, workspaceProvider{Kind: s.Kind(), Name: s.Name(), Connected: s.Connected, AccountName: s.StoredAccountName, Disconnect: s.Disconnect, Resources: func(ctx context.Context, m workspace.Member) ([]workspace.Resource, error) {
		a, err := s.SetupAccess(ctx, tracker.Identity{UserID: m.UserID, Team: m.Team})
		if err != nil {
			return nil, err
		}
		out := []workspace.Resource{}
		for _, r := range a.Resources {
			out = append(out, workspace.Resource{ID: r.ID, Name: r.Name})
		}
		return out, nil
	}})
}
func (h *WorkspaceHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /dashboard", h.home)
	mux.HandleFunc("GET /dashboard/integrations", h.home)
	mux.HandleFunc("GET /dashboard/accounts", h.home)
	mux.HandleFunc("GET /dashboard/ai", h.aiPage)
	mux.HandleFunc("POST /dashboard/ai", h.saveAI)
	mux.HandleFunc("GET /dashboard/team", h.teamPage)
	files := http.StripPrefix("/dashboard/", http.FileServer(http.FS(dashboard.Files())))
	for _, name := range []string{"styles.css", "workspace.css", "nudgy-mascot.webp"} {
		mux.Handle("GET /dashboard/"+name, files)
	}
	for _, path := range []string{"/dashboard/{$}", "/dashboard/index.html", "/dashboard/branch-rules.html"} {
		mux.HandleFunc("GET "+path, func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/dashboard", http.StatusSeeOther) })
	}
	mux.HandleFunc("/api/v1/", retiredDashboard)
	mux.HandleFunc("/api/dashboard", retiredDashboard)
	mux.HandleFunc("/api/dashboard/", retiredDashboard)
	for _, path := range []string{"/workspace", "/workspace/"} {
		mux.HandleFunc("GET "+path, func(w http.ResponseWriter, r *http.Request) {
			next := "/dashboard" + strings.TrimPrefix(r.URL.Path, "/workspace")
			if r.URL.RawQuery != "" {
				next += "?" + r.URL.RawQuery
			}
			privatePageHeaders(w)
			http.Redirect(w, r, next, http.StatusTemporaryRedirect)
		})
	}

	mux.HandleFunc("GET /dashboard/install", h.loginPage)
	mux.HandleFunc("POST /dashboard/login", h.login)
	mux.HandleFunc("POST /dashboard/logout", h.logout)
	mux.HandleFunc("GET /dashboard/integrations/{provider}", h.settings)
	mux.HandleFunc("POST /dashboard/integrations/{provider}", h.save)
	mux.HandleFunc("POST /dashboard/connect/{provider}", h.connect)
	mux.HandleFunc("POST /dashboard/disconnect/{provider}", h.disconnect)
}
func workspaceHash(s string) string { v := sha256.Sum256([]byte(s)); return hex.EncodeToString(v[:]) }
func (h *WorkspaceHandler) session(w http.ResponseWriter, r *http.Request) (workspace.Member, string, bool) {
	privatePageHeaders(w)
	c, err := r.Cookie(workspaceCookie)
	if err != nil || len(c.Value) != 64 {
		h.signedOut(w, r, "")
		return workspace.Member{}, "", false
	}
	user, err := h.store.SessionUser(r.Context(), workspaceHash(c.Value))
	if err != nil {
		h.signedOut(w, r, "Oturumun sona ermiş. Slack hesabınla yeniden giriş yapabilirsin.")
		return workspace.Member{}, "", false
	}
	m, err := h.store.Member(r.Context(), user)
	if err != nil {
		h.page(w, 503, workspacePage{Error: "Çalışma alanı şu an doğrulanamıyor."})
		return m, "", false
	}
	csrf := workspaceHash("csrf:" + c.Value)
	if r.Method != "GET" {
		r.Body = http.MaxBytesReader(w, r.Body, 131072)
		if r.ParseForm() != nil || r.Header.Get("Sec-Fetch-Site") == "cross-site" || (r.Header.Get("Origin") != "" && r.Header.Get("Origin") != h.base) || !tokenMatches(csrf, r.PostForm.Get("csrf")) {
			h.page(w, 403, workspacePage{Error: "İstek doğrulanamadı. Panele dönüp yeniden dene."})
			return m, "", false
		}
	}
	return m, csrf, true
}
func (h *WorkspaceHandler) signedOut(w http.ResponseWriter, r *http.Request, message string) {
	status := http.StatusUnauthorized
	if r.Method == http.MethodGet {
		status = http.StatusOK
	}
	h.page(w, status, workspacePage{SignIn: true, Title: "Giriş", Message: message})
}
func (h *WorkspaceHandler) startSession(w http.ResponseWriter, r *http.Request, user int64, link string) error {
	if _, err := h.store.Member(r.Context(), user); err != nil {
		return err
	}
	raw, err := newOAuthState()
	if err != nil {
		return err
	}
	if err = h.store.CreateSession(r.Context(), user, workspaceHash(raw), time.Now().Add(12*time.Hour), link); err != nil {
		return err
	}
	// Rotation invalidates this browser's previous session, without affecting other devices.
	if c, e := r.Cookie(workspaceCookie); e == nil {
		_ = h.store.DeleteSession(r.Context(), workspaceHash(c.Value))
	}
	http.SetCookie(w, &http.Cookie{Name: workspaceCookie, Value: raw, Path: "/dashboard", HttpOnly: true, Secure: strings.HasPrefix(h.base, "https://"), SameSite: http.SameSiteLaxMode, MaxAge: 43200})
	return nil
}
func (h *WorkspaceHandler) loginPage(w http.ResponseWriter, r *http.Request) {
	user, _ := strconv.ParseInt(r.URL.Query().Get("user_id"), 10, 64)
	token := r.URL.Query().Get("token")
	if !h.links.Verify(token, user, "workspace", time.Now()) {
		h.page(w, 401, workspacePage{Error: "Bu giriş bağlantısı geçersiz veya süresi dolmuş. Slack hesabınla yeniden giriş yapabilirsin."})
		return
	}
	state, err := newOAuthState()
	if err != nil {
		h.page(w, 503, workspacePage{Error: "Giriş başlatılamadı."})
		return
	}
	setOAuthCookie(w, r, "nudgy_workspace_login", state, "/dashboard/login")
	h.page(w, 200, workspacePage{Login: true, UserID: user, Token: token, CSRF: state})
}
func (h *WorkspaceHandler) login(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	if r.ParseForm() != nil || r.Header.Get("Sec-Fetch-Site") == "cross-site" || (r.Header.Get("Origin") != "" && r.Header.Get("Origin") != h.base) || !consumeOAuthCookie(w, r, "nudgy_workspace_login", r.PostForm.Get("csrf"), "/dashboard/login") {
		h.page(w, 403, workspacePage{Error: "Giriş oturumu doğrulanamadı. Özel bağlantını tekrar aç."})
		return
	}
	user, _ := strconv.ParseInt(r.PostForm.Get("user_id"), 10, 64)
	token := r.PostForm.Get("token")
	if !h.links.Verify(token, user, "workspace", time.Now()) || h.startSession(w, r, user, workspaceHash(token)) != nil {
		h.page(w, 401, workspacePage{Error: "Bu giriş bağlantısı kullanılmış veya süresi dolmuş. Slack hesabınla yeniden giriş yapabilirsin."})
		return
	}
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}
func (h *WorkspaceHandler) logout(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := h.session(w, r); !ok {
		return
	}
	c, _ := r.Cookie(workspaceCookie)
	if h.store.DeleteSession(r.Context(), workspaceHash(c.Value)) != nil {
		h.page(w, 503, workspacePage{Error: "Çıkış tamamlanamadı. Tekrar dene."})
		return
	}
	http.SetCookie(w, &http.Cookie{Name: workspaceCookie, Path: "/dashboard", HttpOnly: true, Secure: strings.HasPrefix(h.base, "https://"), SameSite: http.SameSiteLaxMode, MaxAge: -1})
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}
func (h *WorkspaceHandler) provider(kind string) (workspaceProvider, bool) {
	for _, p := range h.providers {
		if p.Kind == kind {
			return p, true
		}
	}
	return workspaceProvider{}, false
}

type workspaceCard struct {
	Kind, Name, Status, AccountName                      string
	Configured, Enabled, Connected, Manager, Unavailable bool
	Count                                                int
}
type workspaceChoice struct {
	ID, Name string
	Selected bool
}
type workspacePage struct {
	Member                                              workspace.Member
	CSRF, Error, Message, Token, Provider, ProviderName string
	UserID                                              int64
	Login, SignIn, SlackSignIn, Settings, Enabled       bool
	Cards                                               []workspaceCard
	Choices                                             []workspaceChoice
	Section, Title, Description                         string
	AI                                                  workspace.AISettings
	Models                                              []workspace.Model
	Members                                             []workspace.Member
	ActiveIntegrations, VerifiedAccounts                int
}

func (h *WorkspaceHandler) page(w http.ResponseWriter, status int, data workspacePage) {
	data.SlackSignIn = h.slackSignIn
	if data.Error != "" && data.Member.UserID == 0 && !data.Login {
		data.SignIn = true
	}
	if data.Title == "" {
		data.Title = "Çalışma alanı"
	}

	privatePageHeaders(w)
	w.Header().Set("Referrer-Policy", "strict-origin")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'self'; img-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
	var b bytes.Buffer
	if workspaceTemplate.Execute(&b, data) != nil {
		http.Error(w, "Panel gösterilemiyor", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(b.Bytes())
}
func (h *WorkspaceHandler) home(w http.ResponseWriter, r *http.Request) {
	m, csrf, ok := h.session(w, r)
	if !ok {
		return
	}
	section := "overview"
	if r.URL.Path == "/dashboard/integrations" {
		section = "integrations"
		if !m.Manager {
			h.page(w, 403, workspacePage{Member: m, Error: "Entegrasyonları yalnızca çalışma alanı yöneticisi yönetebilir."})
			return
		}
	}
	if r.URL.Path == "/dashboard/accounts" {
		section = "accounts"
	}
	data := workspacePage{Member: m, CSRF: csrf, Section: section}
	switch section {
	case "overview":
		data.Title = "Genel bakış"
		data.Description = "Çalışma alanın, AI ayarların ve hesapların tek yerde."
	case "integrations":
		data.Title = "Entegrasyonlar"
		data.Description = "Ekibin kullanacağı servisleri ve ortak alanları belirle."
	case "accounts":
		data.Title = "Hesaplarım"
		data.Description = "Kendi hesaplarını doğrula; yaptığın işlemler senin kimliğinle kaydedilsin."
	}
	if h.ai != nil {
		var err error
		data.AI, err = h.ai.AISettings(r.Context(), m.UserID)
		if err != nil {
			h.page(w, 503, workspacePage{Member: m, Error: "AI ayarları şu an okunamıyor."})
			return
		}
	}
	switch r.URL.Query().Get("done") {
	case "saved":
		data.Message = "Çalışma alanı ayarları kaydedildi."
	case "verified":
		data.Message = "Hesabın doğrulandı. Erişimlerin, çalışma alanı ayarları ve kendi hesap yetkilerinle sınırlıdır."
	case "disconnected":
		data.Message = "Kişisel hesabının Nudgy bağlantısı kaldırıldı."
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	for _, kind := range []string{"github", "gitlab", "jira", "trello"} {
		names := map[string]string{"github": "GitHub", "gitlab": "GitLab", "jira": "Jira", "trello": "Trello"}
		card := workspaceCard{Kind: kind, Name: names[kind], Manager: m.Manager && section == "integrations"}
		p, configured := h.provider(kind)
		card.Configured = configured
		if configured {
			scope, err := h.store.Scope(ctx, m.UserID, kind)
			if err != nil {
				h.page(w, 503, workspacePage{Error: "Entegrasyon ayarları okunamadı. Yeniden dene."})
				return
			}
			card.Enabled = scope.Enabled
			card.Count = len(scope.Resources)
			card.Connected, err = p.Connected(ctx, m.UserID)
			card.Unavailable = err != nil
			if card.Connected && p.AccountName != nil {
				card.AccountName, err = p.AccountName(ctx, m.UserID)
				card.Unavailable = err != nil
			}
		}
		switch {
		case !configured:
			card.Status = "Bu Nudgy kurulumunda mevcut değil"
		case card.Unavailable:
			card.Status = "Hesap durumu alınamadı"
		case !card.Enabled:
			card.Status = "Çalışma alanında kurulmadı"
		case card.Connected:
			card.Status = "Hesap bağlı"
		default:
			card.Status = "Hesabını doğrula"
		}
		if card.Enabled {
			data.ActiveIntegrations++
		}
		if card.Connected {
			data.VerifiedAccounts++
		}
		data.Cards = append(data.Cards, card)
	}
	h.page(w, 200, data)
}
func (h *WorkspaceHandler) settings(w http.ResponseWriter, r *http.Request) {
	m, csrf, ok := h.session(w, r)
	if !ok {
		return
	}
	p, ok := h.provider(r.PathValue("provider"))
	if !ok || !m.Manager {
		h.page(w, 403, workspacePage{Error: "Entegrasyon ayarlarını yalnızca çalışma alanı yöneticisi değiştirebilir."})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	scope, err := h.store.Scope(ctx, m.UserID, p.Kind)
	if err != nil {
		h.page(w, 503, workspacePage{Error: "Ayarlar okunamadı."})
		return
	}
	resources, err := p.Resources(ctx, m)
	if err != nil {
		h.page(w, 409, workspacePage{Member: m, CSRF: csrf, Error: "Alanları listelemek için önce bu serviste kendi hesabını bağla veya yeniden doğrula."})
		return
	}
	data := workspacePage{Member: m, CSRF: csrf, Settings: true, Section: "integrations", Title: p.Name, Provider: p.Kind, ProviderName: p.Name, Enabled: scope.Enabled}
	selected := map[string]bool{}
	for _, r := range scope.Resources {
		selected[r.ID] = true
	}
	for _, r := range resources {
		data.Choices = append(data.Choices, workspaceChoice{r.ID, r.Name, selected[r.ID]})
	}
	h.page(w, 200, data)
}
func (h *WorkspaceHandler) save(w http.ResponseWriter, r *http.Request) {
	m, _, ok := h.session(w, r)
	if !ok {
		return
	}
	p, ok := h.provider(r.PathValue("provider"))
	if !ok || !m.Manager {
		h.page(w, 403, workspacePage{Error: "Bu ayarı yalnızca çalışma alanı yöneticisi değiştirebilir."})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	scope, err := h.store.Scope(ctx, m.UserID, p.Kind)
	if err != nil {
		h.page(w, 503, workspacePage{Error: "Ayarlar okunamadı."})
		return
	}
	scope.Enabled = r.PostForm.Get("enabled") == "true"
	if scope.Enabled {
		available, err := p.Resources(ctx, m)
		if err != nil {
			h.page(w, 409, workspacePage{Error: "Hesap erişimini doğrulayamadık. Hesabını yeniden doğrulayıp tekrar dene."})
			return
		}
		allowed := map[string]workspace.Resource{}
		for _, v := range available {
			allowed[v.ID] = v
		}
		scope.Resources = nil
		for _, id := range r.PostForm["resource"] {
			v, exists := allowed[id]
			if !exists {
				h.page(w, 400, workspacePage{Error: "Seçilen alanlardan biri erişilebilir değil. Listeyi yenileyip tekrar seç."})
				return
			}
			scope.Resources = append(scope.Resources, v)
			delete(allowed, id)
		}
		if len(scope.Resources) == 0 {
			h.page(w, 400, workspacePage{Error: "Entegrasyonu açmak için en az bir repo, proje veya pano seç."})
			return
		}
	}
	if h.store.SaveScope(ctx, m.UserID, p.Kind, scope) != nil {
		h.page(w, 503, workspacePage{Error: "Ayarlar kaydedilemedi."})
		return
	}
	http.Redirect(w, r, "/dashboard/integrations?done=saved", http.StatusSeeOther)
}
func (h *WorkspaceHandler) connect(w http.ResponseWriter, r *http.Request) {
	m, _, ok := h.session(w, r)
	if !ok {
		return
	}
	p, ok := h.provider(r.PathValue("provider"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	scope, err := h.store.Scope(r.Context(), m.UserID, p.Kind)
	if err != nil || (!m.Manager && !scope.Enabled) {
		h.page(w, 403, workspacePage{Error: "Önce yöneticinin bu entegrasyonu kurması gerekiyor."})
		return
	}
	link, err := h.links.URL(h.base, m.UserID, p.Kind, time.Now())
	if err != nil {
		h.page(w, 503, workspacePage{Error: "Doğrulama bağlantısı oluşturulamadı."})
		return
	}
	if m.Manager && p.Kind == "github" && r.PostForm.Get("setup") == "1" {
		link += "&setup=1"
	}
	http.Redirect(w, r, link, http.StatusSeeOther)
}
func (h *WorkspaceHandler) disconnect(w http.ResponseWriter, r *http.Request) {
	m, _, ok := h.session(w, r)
	if !ok {
		return
	}
	p, ok := h.provider(r.PathValue("provider"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	if p.Disconnect(r.Context(), m.UserID) != nil {
		h.page(w, 503, workspacePage{Error: "Hesap bağlantısı kaldırılamadı."})
		return
	}
	http.Redirect(w, r, "/dashboard/accounts?done=disconnected", http.StatusSeeOther)
}
func (h *WorkspaceHandler) SendLink(ctx context.Context, user int64, team string) error {
	i, err := h.installations.GetByUserID(ctx, user)
	if err != nil {
		return err
	}
	if i.TeamID != team {
		return workspace.ErrDenied
	}
	mapped, err := h.installations.GetUserIDBySlackUserID(ctx, team, i.SlackUserID)
	if err != nil || mapped != user {
		return workspace.ErrDenied
	}
	link, err := h.links.URL(h.base, user, "workspace", time.Now())
	if err != nil {
		return err
	}
	ch, err := h.client.OpenDirectMessage(ctx, i.BotToken, i.SlackUserID)
	if err != nil {
		return err
	}
	return h.client.PostMessage(ctx, i.BotToken, ch, "🔗 *Nudgy panelin*\n<"+link+"|Paneli aç>\nYönetici entegrasyonları bir kez kurar; ekip üyeleri kendi hesaplarını doğrular. Bireysel kullanımda ikisini de aynı panelden yapabilirsin. Bu kişisel giriş bağlantısı tek kullanımlıktır ve 20 dakika geçerlidir; paylaşma.")
}
func (h *SlackEventHandler) WithWorkspacePortal(p *WorkspaceHandler) *SlackEventHandler {
	h.workspace = p
	return h
}
