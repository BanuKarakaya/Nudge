package handler

import (
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"
)

type rateBucket struct {
	start time.Time
	count int
}
type HTTPObservation struct {
	RequestID, Method, Route string
	Status                   int
	DurationMS               int64
}
type HTTPOptions struct {
	TrustedProxies []netip.Prefix
	ReportError    func(*http.Request, HTTPObservation)
}
type observedWriter struct {
	http.ResponseWriter
	status int
}

func (w *observedWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
		w.ResponseWriter.WriteHeader(status)
	}
}
func (w *observedWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	return w.ResponseWriter.Write(b)
}
func (w *observedWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *observedWriter) Flush() {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}

// Forwarding headers are accepted only from explicitly configured proxies. Walk
// right to left so a client-supplied prefix cannot bypass the admission limit.
func clientAddress(r *http.Request, trusted []netip.Prefix) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	address, err := netip.ParseAddr(host)
	if err != nil {
		return host
	}
	isTrusted := func(ip netip.Addr) bool {
		for _, prefix := range trusted {
			if prefix.Contains(ip.Unmap()) {
				return true
			}
		}
		return false
	}
	if !isTrusted(address) {
		return address.String()
	}
	chain := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	if len(chain) > 32 {
		return address.String()
	}
	for i := len(chain) - 1; i >= 0; i-- {
		candidate, err := netip.ParseAddr(strings.TrimSpace(chain[i]))
		if err != nil {
			return address.String()
		}
		address = candidate.Unmap()
		if !isTrusted(address) {
			break
		}
	}
	return address.String()
}
func HTTPMiddleware(next http.Handler, options ...HTTPOptions) http.Handler {
	var cfg HTTPOptions
	if len(options) > 0 {
		cfg = options[0]
	}
	var mu sync.Mutex
	buckets := map[string]rateBucket{}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		var id [12]byte
		_, _ = rand.Read(id[:])
		requestID := hex.EncodeToString(id[:])
		recorder := &observedWriter{ResponseWriter: w}
		w = recorder
		w.Header().Set("X-Request-ID", requestID)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		defer func() {
			status := recorder.status
			if status == 0 {
				status = 200
			}
			route := r.Pattern
			if route == "" {
				route = "unmatched"
			}
			observation := HTTPObservation{RequestID: requestID, Method: r.Method, Route: route, Status: status, DurationMS: time.Since(start).Milliseconds()}
			slog.Info("http_request", "request_id", requestID, "method", r.Method, "route", route, "status", status, "duration_ms", observation.DurationMS)
			if status >= 400 && cfg.ReportError != nil {
				cfg.ReportError(r, observation)
			}
		}()
		if strings.HasSuffix(r.URL.Path, "/install") || r.URL.Path == "/slack/signin" {
			host := clientAddress(r, cfg.TrustedProxies)
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
			blocked := b.count >= 60 || (len(buckets) >= 10000 && b.count == 0)
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
		next.ServeHTTP(w, r)
	})
}
