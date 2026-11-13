package handler

import (
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type rateBucket struct {
	start time.Time
	count int
}

// HTTPMiddleware bounds memory and unauthenticated OAuth admission. It uses
// RemoteAddr, never attacker-supplied forwarding headers. Proxies should also
// apply per-client limits at the edge.
func HTTPMiddleware(next http.Handler) http.Handler {
	var mu sync.Mutex
	buckets := map[string]rateBucket{}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		if strings.HasSuffix(r.URL.Path, "/install") || r.URL.Path == "/slack/signin" {
			host, _, _ := net.SplitHostPort(r.RemoteAddr)
			now := time.Now()
			mu.Lock()
			for key, b := range buckets {
				if now.Sub(b.start) > time.Minute {
					delete(buckets, key)
				}
			}
			b := buckets[host]
			if b.start.IsZero() {
				b.start = now
			}
			blocked := b.count >= 60 || len(buckets) >= 10000
			if !blocked {
				b.count++
				buckets[host] = b
			}
			mu.Unlock()
			if blocked {
				w.Header().Set("Retry-After", "60")
				http.Error(w, "Çok fazla istek; bir dakika sonra tekrar dene.", http.StatusTooManyRequests)
				return
			}
		}
		start := time.Now()
		next.ServeHTTP(w, r)
		// Never log query strings, headers, tokens or response contents.
		slog.Info("http_request", "method", r.Method, "duration_ms", time.Since(start).Milliseconds())
	})
}
