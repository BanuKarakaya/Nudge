package feedbin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type testTransport func(*http.Request) (*http.Response, error)

func (f testTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func jsonResponse(value any) *http.Response {
	body, _ := json.Marshal(value)
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(body)))}
}

func unreadIDs(count int) []int64 {
	ids := make([]int64, count)
	for i := range ids {
		ids[i] = int64(i + 1)
	}
	return ids
}

func TestUnreadPagesConcurrentBoundedCompleteAndOrdered(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	started := make(chan struct{}, 10)
	release := make(chan struct{})
	var active, peak atomic.Int32
	since := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	c := Client{HTTPClient: &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "unread_entries.json") {
			return jsonResponse(unreadIDs(805)), nil
		}
		n := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); n > old; old = peak.Load() {
			if peak.CompareAndSwap(old, n) {
				break
			}
		}
		started <- struct{}{}
		select {
		case <-release:
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
		var page []Entry
		for _, raw := range strings.Split(r.URL.Query().Get("ids"), ",") {
			id, _ := strconv.ParseInt(raw, 10, 64)
			created := since
			if id == 2 {
				created = since.Add(-time.Second)
			}
			page = append(page, Entry{ID: id, CreatedAt: created})
		}
		return jsonResponse(page), nil
	})}}
	type result struct {
		entries []Entry
		err     error
	}
	done := make(chan result, 1)
	go func() { entries, err := c.ListUnreadSince(ctx, "test", "test", since); done <- result{entries, err} }()
	for i := 0; i < 4; i++ {
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatal("independent pages were not fetched concurrently")
		}
	}
	close(release)
	r := <-done
	if r.err != nil || len(r.entries) != 804 || peak.Load() != 4 {
		t.Fatalf("entries=%d peak=%d err=%v", len(r.entries), peak.Load(), r.err)
	}
	for i, entry := range r.entries {
		want := int64(i + 1)
		if i >= 1 {
			want++
		}
		if entry.ID != want {
			t.Fatalf("entry %d: got %d want %d", i, entry.ID, want)
		}
	}
}

func TestUnreadPageFailureCancelsPeersAndDoesNotReturnPartialCount(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ready := make(chan struct{}, 4)
	var cancelled atomic.Int32
	c := Client{HTTPClient: &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "unread_entries.json") {
			return jsonResponse(unreadIDs(500)), nil
		}
		ready <- struct{}{}
		if strings.HasPrefix(r.URL.Query().Get("ids"), "1,") {
			for i := 0; i < 4; i++ {
				select {
				case <-ready:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			return &http.Response{StatusCode: 503, Body: io.NopCloser(strings.NewReader("unavailable"))}, nil
		}
		<-r.Context().Done()
		cancelled.Add(1)
		return nil, r.Context().Err()
	})}}
	entries, err := c.ListUnreadSince(ctx, "test", "test", time.Time{})
	if err == nil || !strings.Contains(err.Error(), "503") || len(entries) != 0 || cancelled.Load() != 3 {
		t.Fatalf("entries=%v cancelled=%d err=%v", entries, cancelled.Load(), err)
	}
}

func TestUnreadCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	c := Client{HTTPClient: &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "unread_entries.json") {
			cancel()
			return jsonResponse(unreadIDs(500)), nil
		}
		t.Error("started a page after cancellation")
		return nil, context.Canceled
	})}}
	entries, err := c.ListUnreadSince(ctx, "test", "test", time.Time{})
	if len(entries) != 0 || !errors.Is(err, context.Canceled) {
		t.Fatalf("%v %v", entries, err)
	}
}

// Controlled network simulation, not a production latency measurement.
// 1707 unread entries require 18 pages; each mock page takes 5 ms.
func BenchmarkUnreadPageLatency(b *testing.B) {
	for _, serial := range []bool{true, false} {
		name := "parallel"
		if serial {
			name = "serial_network"
		}
		b.Run(name, func(b *testing.B) {
			var network sync.Mutex
			c := Client{HTTPClient: &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
				if strings.HasSuffix(r.URL.Path, "unread_entries.json") {
					return jsonResponse(unreadIDs(1707)), nil
				}
				if serial {
					network.Lock()
					defer network.Unlock()
				}
				time.Sleep(5 * time.Millisecond)
				var entries []Entry
				for _, raw := range strings.Split(r.URL.Query().Get("ids"), ",") {
					id, _ := strconv.ParseInt(raw, 10, 64)
					entries = append(entries, Entry{ID: id})
				}
				return jsonResponse(entries), nil
			})}}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				entries, err := c.ListUnreadSince(context.Background(), "test", "test", time.Time{})
				if err != nil || len(entries) != 1707 {
					b.Fatalf("entries=%d err=%v", len(entries), err)
				}
			}
		})
	}
}
