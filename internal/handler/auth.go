package handler

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strings"
)

func tokenMatches(expected, provided string) bool {
	if strings.TrimSpace(expected) == "" || provided == "" {
		return false
	}
	a, b := sha256.Sum256([]byte(expected)), sha256.Sum256([]byte(provided))
	return subtle.ConstantTimeCompare(a[:], b[:]) == 1
}

// The REST API is administrative. X-User-ID selects a user only after the
// administrator's bearer token has been authenticated; it is not a credential.
func RequireAPIToken(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if strings.TrimSpace(token) == "" {
			writeError(w, http.StatusServiceUnavailable, "administrative API is disabled")
			return
		}
		value, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || !tokenMatches(token, value) {
			writeError(w, http.StatusUnauthorized, "valid API bearer token required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func privatePageHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "frame-ancestors 'none'; form-action 'self'; base-uri 'none'")
}

func setOAuthCookie(w http.ResponseWriter, r *http.Request, name, state, path string) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: state, Path: path, MaxAge: 600,
		HttpOnly: true, Secure: r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https", SameSite: http.SameSiteLaxMode})
}

func consumeOAuthCookie(w http.ResponseWriter, r *http.Request, name, state, path string) bool {
	cookie, err := r.Cookie(name)
	http.SetCookie(w, &http.Cookie{Name: name, Path: path, MaxAge: -1, HttpOnly: true,
		Secure: r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https", SameSite: http.SameSiteLaxMode})
	return err == nil && tokenMatches(cookie.Value, state)
}
