package handler

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"html/template"
	"net/http"
	"strconv"
	"time"

	"nudge/internal/coding"
)

//go:embed gitlab_page.html
var gitLabPageHTML string
var gitLabPageTemplate = template.Must(template.New("gitlab-connection").Parse(gitLabPageHTML))

type gitLabPageData struct {
	Title, Description, Login, State string
	Success, Selecting               bool
	Repositories                     []coding.Repository
}

func renderGitLabPage(w http.ResponseWriter, status int, data gitLabPageData) {
	privatePageHeaders(w)
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; img-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
	var body bytes.Buffer
	if gitLabPageTemplate.Execute(&body, data) != nil {
		http.Error(w, "Sayfa gösterilemiyor", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(body.Bytes())
}
func (h *CodingHandler) renderConnectionPage(w http.ResponseWriter, status int, data githubPageData) {
	if h.service.Kind() == "gitlab" {
		renderGitLabPage(w, status, gitLabPageData{Title: data.Title, Description: data.Description, Success: data.Success, Login: data.Login})
		return
	}
	renderGitHubConnectionPage(w, status, data)
}
func (h *CodingHandler) gitLabError(w http.ResponseWriter, status int) {
	renderGitLabPage(w, status, gitLabPageData{Title: "Bağlantıyı yeniden başlatalım.", Description: "Bu bağlantının süresi dolmuş veya GitLab erişimin değişmiş olabilir. Slack’te Nudgy’den yeni bir GitLab bağlantısı iste."})
}
func (h *CodingHandler) registerGitLab(public, admin *http.ServeMux) {
	public.HandleFunc("GET /gitlab/install", h.gitLabInstall)
	public.HandleFunc("GET /gitlab/oauth/callback", h.gitLabCallback)
	public.HandleFunc("POST /gitlab/select", h.gitLabSelect)
	public.HandleFunc("GET /gitlab/review", h.review)
	if admin == nil {
		return
	}
	admin.HandleFunc("GET /api/v1/coding/gitlab/connection", func(w http.ResponseWriter, r *http.Request) {
		privatePageHeaders(w)
		id, err := userIDFromHeader(r)
		if err != nil {
			writeError(w, 400, "invalid user")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		installation, err := h.installations.GetByUserID(ctx, id)
		if err != nil {
			writeError(w, 404, "user not found")
			return
		}
		access, err := h.service.RepositoryAccess(ctx, coding.Identity{UserID: id, Team: installation.TeamID})
		if err != nil {
			writeError(w, 403, "GitLab access could not be verified")
			return
		}
		writeJSON(w, 200, map[string]any{"gitlab_account": access.Account, "repositories": access.Repositories})
	})
	admin.HandleFunc("DELETE /api/v1/coding/gitlab/connection", func(w http.ResponseWriter, r *http.Request) {
		id, err := userIDFromHeader(r)
		if err != nil {
			writeError(w, 400, "invalid user")
			return
		}
		if h.service.Disconnect(r.Context(), id) != nil {
			writeError(w, 503, "disconnect failed")
			return
		}
		w.WriteHeader(204)
	})
}
func (h *CodingHandler) gitLabInstall(w http.ResponseWriter, r *http.Request) {
	privatePageHeaders(w)
	id, err := strconv.ParseInt(r.URL.Query().Get("user_id"), 10, 64)
	if err != nil || !h.links.Verify(r.URL.Query().Get("token"), id, "gitlab", time.Now()) {
		h.gitLabError(w, 401)
		return
	}
	if !beginProviderSession(w, r, h.workspace, id, "gitlab") {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	state, redirect, err := h.service.BeginGitLab(ctx, id)
	if err != nil {
		h.gitLabError(w, 503)
		return
	}
	setOAuthCookie(w, r, "nudgy_gitlab_oauth", state, "/gitlab/oauth/callback")
	http.Redirect(w, r, redirect, http.StatusFound)
}
func (h *CodingHandler) gitLabCallback(w http.ResponseWriter, r *http.Request) {
	if !checkProviderSession(w, r, h.workspace, "gitlab") {
		return
	}
	privatePageHeaders(w)
	state := r.URL.Query().Get("state")
	if !consumeOAuthCookie(w, r, "nudgy_gitlab_oauth", state, "/gitlab/oauth/callback") || r.URL.Query().Get("error") != "" {
		h.gitLabError(w, 400)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	next, account, repos, err := h.service.GitLabSelection(ctx, state, r.URL.Query().Get("code"))
	if err != nil {
		if errors.Is(err, coding.ErrAPIScope) {
			renderGitLabPage(w, http.StatusBadRequest, gitLabPageData{Title: "GitLab izni güncellenmeli.", Description: "Nudgy GitLab uygulamasında api iznini etkinleştirip Slack üzerinden yeni bağlantı iste. Mevcut bağlantın korunur."})
			return
		}
		h.gitLabError(w, 400)
		return
	}
	if next == "" && h.workspace != nil {
		http.Redirect(w, r, "/dashboard/accounts?done=verified", http.StatusSeeOther)
		return
	}
	setOAuthCookie(w, r, "nudgy_gitlab_selection", next, "/gitlab/select")
	renderGitLabPage(w, 200, gitLabPageData{Selecting: true, Title: "Hangi projelerde çalışalım?", Description: "Nudgy’ye açmak istediğin GitLab projelerini seç. Yalnızca yazma yetkin olan projeler gösterilir.", Login: account.Login, State: next, Repositories: repos})
}
func (h *CodingHandler) gitLabSelect(w http.ResponseWriter, r *http.Request) {
	if !checkProviderSession(w, r, h.workspace, "gitlab") {
		return
	}
	privatePageHeaders(w)
	origin := r.Header.Get("Origin")
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" || (origin != "" && origin != "null" && origin != h.publicURL) {
		h.gitLabError(w, 403)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 65536)
	if r.ParseForm() != nil {
		h.gitLabError(w, 400)
		return
	}
	state := r.PostForm.Get("state")
	if !consumeOAuthCookie(w, r, "nudgy_gitlab_selection", state, "/gitlab/select") {
		h.gitLabError(w, 400)
		return
	}
	ids := []int64{}
	for _, raw := range r.PostForm["project"] {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			h.gitLabError(w, 400)
			return
		}
		ids = append(ids, id)
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	account, err := h.service.CompleteGitLabSelection(ctx, state, ids)
	if err != nil {
		h.gitLabError(w, 400)
		return
	}
	renderGitLabPage(w, 200, gitLabPageData{Success: true, Title: "GitLab’ın bağlandı.", Description: "Hesabın ve seçtiğin projeler için bağlantı tamamlandı. Slack’e dönebilirsin.", Login: account.Login})
}
