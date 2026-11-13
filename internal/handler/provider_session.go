package handler

import (
	"context"
	"net/http"
	"time"
)

// Signed invitation links select a user; only that user's authenticated panel
// session may initiate OAuth. The extra cookie binds all subsequent redirects
// to the same session, so logout/account switching invalidates the flow.
func beginProviderSession(w http.ResponseWriter, r *http.Request, portal *WorkspaceHandler, user int64, kind string) bool {
	if portal == nil {
		return true
	} // legacy embedders; production always supplies the portal
	member, _, ok := portal.session(w, r)
	if !ok {
		return false
	}
	if member.UserID != user {
		writeError(w, http.StatusForbidden, "Bu bağlantı giriş yaptığın Slack hesabına ait değil. Panelinden yeniden başlat.")
		return false
	}
	if used, ok := portal.store.(interface {
		UseConnectionLink(context.Context, string) error
	}); ok {
		if err := used.UseConnectionLink(r.Context(), workspaceHash(r.URL.Query().Get("token"))); err != nil {
			writeError(w, 401, "Bu bağlantı kullanılmış; panelinden yeniden başlat.")
			return false
		}
	}
	cookie, err := r.Cookie(workspaceCookie)
	if err != nil {
		return false
	}
	http.SetCookie(w, &http.Cookie{Name: "nudgy_" + kind + "_session", Value: workspaceHash(cookie.Value), Path: "/" + kind + "/", HttpOnly: true, Secure: r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https", SameSite: http.SameSiteLaxMode, MaxAge: 1200, Expires: time.Now().Add(20 * time.Minute)})
	return true
}

func checkProviderSession(w http.ResponseWriter, r *http.Request, portal *WorkspaceHandler, kind string) bool {
	if portal == nil {
		return true
	}
	session, err := r.Cookie(workspaceCookie)
	binding, bindErr := r.Cookie("nudgy_" + kind + "_session")
	if err != nil || bindErr != nil || !tokenMatches(workspaceHash(session.Value), binding.Value) {
		writeError(w, http.StatusUnauthorized, "Bağlantıyı kendi panel oturumundan yeniden başlat.")
		return false
	}
	// This check is authentication only; callback POSTs have separate state/CSRF checks.
	user, err := portal.store.SessionUser(r.Context(), workspaceHash(session.Value))
	if err != nil || user <= 0 {
		writeError(w, http.StatusUnauthorized, "Panel oturumun sona ermiş.")
		return false
	}
	return true
}
