package service

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"nudge/internal/domain"
	"nudge/internal/feedbin"
	"nudge/internal/repository"
)

type digestRuntimeFunc func(context.Context, AgentRequest) (AgentResult, error)

func (f digestRuntimeFunc) Run(ctx context.Context, request AgentRequest) (AgentResult, error) {
	return f(ctx, request)
}

type digestConnections struct {
	repository.FeedbinConnectionRepository
	users []int64
}

func (c *digestConnections) GetByUserID(_ context.Context, userID int64) (domain.FeedbinConnection, error) {
	c.users = append(c.users, userID)
	return domain.FeedbinConnection{Email: "bound-user", Password: "secret"}, nil
}

type digestSource struct {
	entries []feedbin.Entry
	calls   int
	err     error
	tagErr  error
}

func (s *digestSource) ListUnreadSince(_ context.Context, email, password string, _ time.Time) ([]feedbin.Entry, error) {
	s.calls++
	if email != "bound-user" || password != "secret" {
		return nil, errors.New("wrong identity")
	}
	return s.entries, s.err
}

func (s *digestSource) ListTaggings(context.Context, string, string) ([]feedbin.Tagging, error) {
	return []feedbin.Tagging{{FeedID: 10, Name: "AI"}}, s.tagErr
}

func TestAgentDigestEnforcesHalfOpenWindowAndPaginatesSharedSnapshot(t *testing.T) {
	loc := time.FixedZone("Istanbul", 3*60*60)
	until := time.Date(2026, 10, 23, 23, 0, 0, 0, loc)
	since := until.AddDate(0, 0, -1)
	connections := &digestConnections{}
	source := &digestSource{entries: []feedbin.Entry{
		{ID: 1, CreatedAt: since.Add(-time.Nanosecond)},
		{ID: 2, FeedID: 10, CreatedAt: since, Content: "raw source at start"},
		{ID: 3, FeedID: 10, CreatedAt: until.Add(-time.Nanosecond), Content: "raw source at end"},
		{ID: 3, FeedID: 10, CreatedAt: until.Add(-time.Nanosecond)},
		{ID: 4, CreatedAt: until},
		{ID: 5, CreatedAt: until.Add(time.Nanosecond)},
	}}
	runtime := digestRuntimeFunc(func(ctx context.Context, request AgentRequest) (AgentResult, error) {
		if request.ThreadID != "" || !reflect.DeepEqual(request.History, ConversationState{}) {
			t.Fatal("scheduled generation must not resume conversation state")
		}
		if request.Key.User != "42" || !strings.Contains(request.Input, since.Format(time.RFC3339)) || !strings.Contains(request.Input, until.Format(time.RFC3339)) {
			t.Fatalf("missing identity/window: %+v", request)
		}
		if len(request.Tools.Definitions()) != 2 {
			t.Fatal("scheduled jobs should expose only RSS tools")
		}
		var ids []int64
		for _, offset := range []string{"0", "1"} {
			raw, err := request.Tools.Call(ctx, "rss_articles", json.RawMessage(`{"scope":"all_unread","offset":`+offset+`,"limit":1,"include_content":true}`))
			if err != nil {
				t.Fatal(err)
			}
			var page struct {
				Total    int       `json:"total"`
				Next     *int      `json:"next_offset"`
				Start    time.Time `json:"scheduled_window_start"`
				End      time.Time `json:"scheduled_window_end"`
				Articles []struct {
					ID      int64  `json:"id"`
					Content string `json:"content"`
				} `json:"articles"`
			}
			if err := json.Unmarshal([]byte(raw), &page); err != nil {
				t.Fatal(err)
			}
			if page.Total != 2 || len(page.Articles) != 1 || !page.Start.Equal(since) || !page.End.Equal(until) || !strings.HasPrefix(page.Articles[0].Content, "raw source") {
				t.Fatalf("incorrect scheduled page: %s", raw)
			}
			if (offset == "0" && (page.Next == nil || *page.Next != 1)) || (offset == "1" && page.Next != nil) {
				t.Fatalf("incorrect pagination: %s", raw)
			}
			ids = append(ids, page.Articles[0].ID)
		}
		if !reflect.DeepEqual(ids, []int64{2, 3}) {
			t.Fatalf("window included wrong entries: %v", ids)
		}
		// Both statistics and alternate scopes must stay inside the bound window.
		for _, scope := range []string{"all_unread", "reminder_window", "today"} {
			raw, err := request.Tools.Call(ctx, "rss_inventory", json.RawMessage(`{"scope":"`+scope+`"}`))
			if err != nil {
				t.Fatal(err)
			}
			var result struct {
				Total int `json:"total"`
			}
			if err := json.Unmarshal([]byte(raw), &result); err != nil {
				t.Fatal(err)
			}
			want := 2
			if scope == "today" {
				want = 1
			}
			if result.Total != want {
				t.Fatalf("scope=%s result=%s", scope, raw)
			}
		}
		for _, name := range []string{"bookmarks_list", "web_search"} {
			if _, err := request.Tools.Call(ctx, name, json.RawMessage(`{}`)); err == nil {
				t.Fatalf("unexpected tool %s", name)
			}
		}
		return AgentResult{ThreadID: "discarded", Text: "  Finished agent digest.  "}, nil
	})
	generator := NewAgentDigest(runtime, NewFeedbinDigestService(connections, source), loc)
	text, err := generator.Generate(context.Background(), 42, since, until)
	if err != nil || text != "Finished agent digest." {
		t.Fatalf("text=%q err=%v", text, err)
	}
	if source.calls != 1 || !reflect.DeepEqual(connections.users, []int64{42}) {
		t.Fatalf("snapshot/identity not shared: calls=%d users=%v", source.calls, connections.users)
	}
	tools := newScheduledDigestTools(NewFeedbinDigestService(connections, source), 42, since, until, loc)
	if _, err := tools.Call(context.Background(), "rss_articles", json.RawMessage(`{"scope":"all_unread","user_id":99}`)); err == nil {
		t.Fatal("tool permitted identity override")
	}
}

func TestAgentDigestStableIdentityAndFreshRuns(t *testing.T) {
	since := time.Date(2026, 10, 22, 20, 0, 0, 123, time.UTC)
	until := since.AddDate(0, 0, 1)
	var requests []AgentRequest
	runtime := digestRuntimeFunc(func(ctx context.Context, request AgentRequest) (AgentResult, error) {
		requests = append(requests, request)
		if _, err := request.Tools.Call(ctx, "rss_inventory", json.RawMessage(`{"scope":"all_unread"}`)); err != nil {
			t.Fatal(err)
		}
		return AgentResult{ThreadID: "not-resumed", Text: "done"}, nil
	})
	generator := NewAgentDigest(runtime, NewFeedbinDigestService(&digestConnections{}, &digestSource{}), nil)
	otherZone := time.FixedZone("other", 3*60*60)
	for _, run := range []struct {
		user     int64
		from, to time.Time
	}{
		{1, since, until}, {1, since.In(otherZone), until.In(otherZone)},
		{2, since, until}, {1, since.Add(time.Nanosecond), until}, {1, since, until.Add(time.Nanosecond)},
	} {
		if _, err := generator.Generate(context.Background(), run.user, run.from, run.to); err != nil {
			t.Fatal(err)
		}
	}
	if requests[0].Key != requests[1].Key || requests[0].Tools == requests[1].Tools {
		t.Fatal("same window must have a stable key and fresh tools")
	}
	for i, request := range requests {
		if request.ThreadID != "" {
			t.Fatal("agent thread reused")
		}
		if i > 1 && request.Key == requests[0].Key {
			t.Fatal("distinct job reused identity")
		}
	}
}

func TestAgentDigestValidatesOutputWithoutRetry(t *testing.T) {
	since := time.Date(2026, 10, 22, 23, 0, 0, 0, time.UTC)
	runtimeErr := errors.New("runtime failed")
	for _, tc := range []struct {
		name, output string
		err          error
		valid        bool
	}{
		{name: "empty", output: " \n\t"},
		{name: "too large", output: strings.Repeat("ğ", agentDigestMaxChars+1)},
		{name: "invalid UTF-8", output: string([]byte{0xff})},
		{name: "boundary", output: strings.Repeat("ğ", agentDigestMaxChars), valid: true},
		{name: "plain final answer", output: "No new RSS items.", valid: true},
		{name: "runtime failure", output: "partial", err: runtimeErr},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			runtime := digestRuntimeFunc(func(ctx context.Context, request AgentRequest) (AgentResult, error) {
				calls++
				if _, err := request.Tools.Call(ctx, "rss_inventory", json.RawMessage(`{"scope":"all_unread"}`)); err != nil {
					t.Fatal(err)
				}
				return AgentResult{Text: tc.output}, tc.err
			})
			generator := NewAgentDigest(runtime, NewFeedbinDigestService(&digestConnections{}, &digestSource{}), nil)
			text, err := generator.Generate(context.Background(), 1, since, since.AddDate(0, 0, 1))
			if calls != 1 || (err == nil) != tc.valid || (!tc.valid && text != "") || (tc.valid && text != tc.output) {
				t.Fatalf("calls=%d length=%d err=%v", calls, len(text), err)
			}
			if tc.err != nil && !errors.Is(err, tc.err) {
				t.Fatalf("lost runtime error: %v", err)
			}
		})
	}
}

func TestAgentDigestRejectsInvalidJobsBeforeRuntime(t *testing.T) {
	since := time.Date(2026, 10, 22, 23, 0, 0, 0, time.UTC)
	runtime := digestRuntimeFunc(func(context.Context, AgentRequest) (AgentResult, error) {
		t.Fatal("invalid job reached runtime")
		return AgentResult{}, nil
	})
	generator := NewAgentDigest(runtime, NewFeedbinDigestService(&digestConnections{}, &digestSource{}), nil)
	for _, tc := range []struct {
		user     int64
		from, to time.Time
	}{
		{0, since, since.Add(time.Hour)}, {1, time.Time{}, since},
		{1, since, since}, {1, since, since.Add(-time.Hour)},
	} {
		if text, err := generator.Generate(context.Background(), tc.user, tc.from, tc.to); err == nil || text != "" {
			t.Fatal("accepted invalid job")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := generator.Generate(ctx, 1, since, since.Add(time.Hour)); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}

func TestScheduledDigestToolFailureIsNotEmptyInventory(t *testing.T) {
	since := time.Date(2026, 10, 22, 23, 0, 0, 0, time.UTC)
	source := &digestSource{err: errors.New("provider unavailable")}
	tools := newScheduledDigestTools(NewFeedbinDigestService(&digestConnections{}, source), 42, since, since.AddDate(0, 0, 1), time.UTC)
	for _, name := range []string{"rss_inventory", "rss_articles"} {
		if result, err := tools.Call(context.Background(), name, json.RawMessage(`{"scope":"all_unread"}`)); err == nil || result != "" {
			t.Fatal("source failure returned empty inventory")
		}
	}
	if source.calls != 1 {
		t.Fatalf("failure refetched snapshot %d times", source.calls)
	}
}

func TestAgentDigestRequiresSuccessfulRSSRetrieval(t *testing.T) {
	since := time.Date(2026, 10, 22, 23, 0, 0, 0, time.UTC)
	providerErr := errors.New("provider unavailable")
	for _, tc := range []struct {
		name    string
		calls   []string
		source  digestSource
		valid   bool
		fetches int
	}{
		{name: "no tool calls"},
		{name: "unknown tool only", calls: []string{"unknown"}},
		{name: "invalid RSS call only", calls: []string{"invalid"}},
		{name: "inventory retrieval fails", calls: []string{"rss_inventory"}, source: digestSource{err: providerErr}, fetches: 1},
		{name: "articles retrieval fails", calls: []string{"rss_articles"}, source: digestSource{err: providerErr}, fetches: 1},
		{name: "tagging retrieval fails", calls: []string{"rss_inventory"}, source: digestSource{tagErr: providerErr}, fetches: 1},
		{name: "success followed by failure", calls: []string{"rss_inventory", "invalid"}, fetches: 1},
		{name: "failure followed by success", calls: []string{"invalid", "rss_articles"}, fetches: 1},
		{name: "empty inventory succeeds", calls: []string{"rss_inventory"}, valid: true, fetches: 1},
		{name: "empty articles succeeds", calls: []string{"rss_articles"}, valid: true, fetches: 1},
		{name: "nonempty articles succeeds", calls: []string{"rss_articles"}, source: digestSource{entries: []feedbin.Entry{{ID: 1, CreatedAt: since, Content: "source content"}}}, valid: true, fetches: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := tc.source
			runs := 0
			runtime := digestRuntimeFunc(func(ctx context.Context, request AgentRequest) (AgentResult, error) {
				runs++
				for _, name := range tc.calls {
					args := json.RawMessage(`{"scope":"all_unread"}`)
					if name == "invalid" {
						name, args = "rss_articles", json.RawMessage(`{"scope":"invalid"}`)
					}
					// Simulate a runtime that ignores tool errors and still returns
					// a convincing nonempty answer. Its language is not evidence.
					_, _ = request.Tools.Call(ctx, name, args)
				}
				return AgentResult{Text: "📰 Günlük RSS özetin hazır."}, nil
			})
			generator := NewAgentDigest(runtime, NewFeedbinDigestService(&digestConnections{}, &source), time.UTC)
			text, err := generator.Generate(context.Background(), 42, since, since.AddDate(0, 0, 1))
			if (err == nil) != tc.valid || (!tc.valid && text != "") || (tc.valid && text != "📰 Günlük RSS özetin hazır.") {
				t.Fatalf("text=%q err=%v valid=%v", text, err, tc.valid)
			}
			if runs != 1 || source.calls != tc.fetches {
				t.Fatalf("runs=%d fetches=%d want=%d", runs, source.calls, tc.fetches)
			}
		})
	}
}

func TestAgentDigestConcurrentRSSCallsKeepFailure(t *testing.T) {
	since := time.Date(2026, 10, 22, 23, 0, 0, 0, time.UTC)
	source := &digestSource{}
	runtime := digestRuntimeFunc(func(ctx context.Context, request AgentRequest) (AgentResult, error) {
		var calls sync.WaitGroup
		for i := 0; i < 12; i++ {
			calls.Add(1)
			go func(i int) {
				defer calls.Done()
				args := json.RawMessage(`{"scope":"all_unread"}`)
				if i == 0 {
					args = json.RawMessage(`{"scope":"invalid"}`)
				}
				_, err := request.Tools.Call(ctx, "rss_inventory", args)
				if (err != nil) != (i == 0) {
					t.Errorf("call=%d err=%v", i, err)
				}
			}(i)
		}
		calls.Wait()
		return AgentResult{Text: "No unread articles."}, nil
	})
	generator := NewAgentDigest(runtime, NewFeedbinDigestService(&digestConnections{}, source), time.UTC)
	text, err := generator.Generate(context.Background(), 42, since, since.AddDate(0, 0, 1))
	if err == nil || text != "" || source.calls != 1 {
		t.Fatalf("text=%q err=%v fetches=%d", text, err, source.calls)
	}
}

func TestAgentDigestFailedRetrievalDoesNotPoisonNextRun(t *testing.T) {
	since := time.Date(2026, 10, 22, 23, 0, 0, 0, time.UTC)
	source := &digestSource{err: errors.New("temporary source failure")}
	runtime := digestRuntimeFunc(func(ctx context.Context, request AgentRequest) (AgentResult, error) {
		_, _ = request.Tools.Call(ctx, "rss_inventory", json.RawMessage(`{"scope":"all_unread"}`))
		return AgentResult{Text: "No unread articles."}, nil
	})
	generator := NewAgentDigest(runtime, NewFeedbinDigestService(&digestConnections{}, source), time.UTC)
	if text, err := generator.Generate(context.Background(), 42, since, since.AddDate(0, 0, 1)); err == nil || text != "" {
		t.Fatalf("failed retrieval produced text=%q err=%v", text, err)
	}
	source.err = nil
	if text, err := generator.Generate(context.Background(), 42, since, since.AddDate(0, 0, 1)); err != nil || text != "No unread articles." {
		t.Fatalf("retry failed: text=%q err=%v", text, err)
	}
	if source.calls != 2 {
		t.Fatalf("retry did not fetch a fresh snapshot: calls=%d", source.calls)
	}
}
