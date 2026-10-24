package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"nudge/internal/domain"
	"nudge/internal/feedbin"
	"nudge/internal/repository"
)

type agentConnectionFixture struct {
	repository.FeedbinConnectionRepository
	users []int64
	err   error
}

func (f *agentConnectionFixture) GetByUserID(_ context.Context, userID int64) (domain.FeedbinConnection, error) {
	f.users = append(f.users, userID)
	return domain.FeedbinConnection{UserID: userID, Email: "private@example.org", Password: "secret-password"}, f.err
}

type agentFeedFixture struct {
	entries                  []feedbin.Entry
	taggings                 []feedbin.Tagging
	entryCalls, taggingCalls int
	since                    time.Time
	entryErr, taggingErr     error
}

func (f *agentFeedFixture) ListUnreadSince(_ context.Context, email, password string, since time.Time) ([]feedbin.Entry, error) {
	f.entryCalls++
	f.since = since
	if email != "private@example.org" || password != "secret-password" {
		return nil, errors.New("wrong credentials")
	}
	return f.entries, f.entryErr
}

func (f *agentFeedFixture) ListTaggings(context.Context, string, string) ([]feedbin.Tagging, error) {
	f.taggingCalls++
	return f.taggings, f.taggingErr
}

type agentBookmarkFixture struct {
	BookmarkService // Unexpected calls (including writes) fail instead of succeeding.
	items           []domain.Bookmark
	users           []int64
	from, to        time.Time
	err             error
}

func (f *agentBookmarkFixture) ListByDate(_ context.Context, userID int64, from, to time.Time) ([]domain.Bookmark, error) {
	f.users = append(f.users, userID)
	f.from, f.to = from, to
	return f.items, f.err
}

type agentWebFixture struct {
	queries []string
	err     error
}

func (f *agentWebFixture) SearchWeb(_ context.Context, query string) (string, error) {
	f.queries = append(f.queries, query)
	return "external provider output", f.err
}

func agentResult(t *testing.T, tools AgentTools, name, args string) map[string]any {
	t.Helper()
	result, err := tools.Call(context.Background(), name, json.RawMessage(args))
	if err != nil {
		t.Fatalf("%s(%s): %v", name, args, err)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(result), &decoded); err != nil {
		t.Fatalf("invalid result JSON %q: %v", result, err)
	}
	return decoded
}

func agentLocation(t *testing.T, name string) *time.Location {
	t.Helper()
	location, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return location
}

func TestAgentToolsCompleteInventoryAndStablePagination(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	repo := &agentConnectionFixture{}
	client := &agentFeedFixture{taggings: []feedbin.Tagging{{FeedID: 1, Name: "Tech"}, {FeedID: 1, Name: "Tech"}, {FeedID: 1, Name: "AI"}}}
	// More than two maximum-sized pages, reversed and with timestamp ties.
	for id := int64(237); id > 0; id-- {
		client.entries = append(client.entries, feedbin.Entry{ID: id, FeedID: id % 2, Title: fmt.Sprintf("Article %d", id), URL: "https://example.org/shared", Content: "raw body", CreatedAt: now})
	}
	client.entries = append(client.entries, client.entries[0])
	tools := NewAgentTools(NewFeedbinDigestService(repo, client), nil, nil, 42, now, time.UTC)
	stats := agentResult(t, tools, "rss_inventory", `{"scope":"all_unread"}`)
	if stats["total"] != float64(237) || stats["uncategorized_count"] != float64(118) {
		t.Fatalf("incorrect deduplicated counts: %v", stats)
	}
	categories := stats["categories"].([]any)
	if len(categories) != 2 || categories[0].(map[string]any)["name"] != "AI" || categories[0].(map[string]any)["count"] != float64(119) || categories[1].(map[string]any)["count"] != float64(119) {
		t.Fatalf("incorrect categories: %v", categories)
	}
	// Backing slice changes after loading must not affect the snapshot.
	client.entries[0].Title = "changed after snapshot"
	client.entries = nil
	var ids []int64
	for offset := 0; ; {
		page := agentResult(t, tools, "rss_articles", fmt.Sprintf(`{"scope":"all_unread","offset":%d,"limit":100}`, offset))
		for _, raw := range page["articles"].([]any) {
			article := raw.(map[string]any)
			if _, exists := article["content"]; exists {
				t.Fatal("title-only page includes content")
			}
			if article["title"] == "changed after snapshot" {
				t.Fatal("snapshot changed")
			}
			ids = append(ids, int64(article["id"].(float64)))
		}
		if page["next_offset"] == nil {
			break
		}
		offset = int(page["next_offset"].(float64))
	}
	if len(ids) != 237 {
		t.Fatalf("inventory capped: got %d", len(ids))
	}
	for index, id := range ids {
		if id != int64(index+1) {
			t.Fatalf("unstable order at %d: %d", index, id)
		}
	}
	page := agentResult(t, tools, "rss_articles", `{"scope":"all_unread","offset":9223372036854775807}`)
	if len(page["articles"].([]any)) != 0 || page["next_offset"] != nil {
		t.Fatalf("out-of-range offset must terminate: %v", page)
	}
	if _, err := tools.Call(ctx, "rss_inventory", json.RawMessage(`{"scope":"today"}`)); err != nil {
		t.Fatal(err)
	}
	if len(repo.users) != 1 || repo.users[0] != 42 || client.entryCalls != 1 || client.taggingCalls != 1 || !client.since.IsZero() {
		t.Fatalf("snapshot not reused or identity not bound: repo=%v client=%+v", repo.users, client)
	}
}

func TestAgentToolsCalendarTodayAndReminderScopes(t *testing.T) {
	location := agentLocation(t, "Europe/Istanbul")
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, location)
	midnight := time.Date(2026, 10, 24, 0, 0, 0, 0, location)
	reminderEnd := time.Date(2026, 10, 23, 23, 0, 0, 0, location)
	reminderStart := reminderEnd.AddDate(0, 0, -1)
	client := &agentFeedFixture{}
	for i, date := range []time.Time{reminderStart.Add(-time.Nanosecond), reminderStart, reminderEnd.Add(-time.Nanosecond), reminderEnd, midnight.Add(-time.Nanosecond), midnight, now, midnight.AddDate(0, 0, 1)} {
		client.entries = append(client.entries, feedbin.Entry{ID: int64(i + 1), CreatedAt: date.UTC()})
	}
	tools := NewAgentTools(NewFeedbinDigestService(&agentConnectionFixture{}, client), nil, nil, 42, now.UTC(), location)
	for _, test := range []struct {
		scope string
		ids   string
		start time.Time
		end   time.Time
	}{
		{"all_unread", "[1 2 3 4 5 6 7 8]", time.Time{}, time.Time{}},
		{"today", "[6 7]", midnight, midnight.AddDate(0, 0, 1)},
		{"reminder_window", "[2 3]", reminderStart, reminderEnd},
	} {
		t.Run(test.scope, func(t *testing.T) {
			page := agentResult(t, tools, "rss_articles", fmt.Sprintf(`{"scope":%q}`, test.scope))
			ids := []int{}
			for _, raw := range page["articles"].([]any) {
				ids = append(ids, int(raw.(map[string]any)["id"].(float64)))
			}
			if fmt.Sprint(ids) != test.ids {
				t.Fatalf("wrong calendar window: %v", page)
			}
			if !test.start.IsZero() && (page["window_start"] != test.start.Format(time.RFC3339) || page["window_end"] != test.end.Format(time.RFC3339)) {
				t.Fatalf("wrong window metadata: %v", page)
			}
			stats := agentResult(t, tools, "rss_inventory", fmt.Sprintf(`{"scope":%q}`, test.scope))
			if stats["total"] != page["total"] {
				t.Fatalf("stats disagree with page: %v / %v", stats, page)
			}
		})
	}
	if client.entryCalls != 1 || client.taggingCalls != 1 {
		t.Fatalf("fetched again for a new scope: %+v", client)
	}
}

func TestAgentToolsWindowsRespectDSTAndReminderCutoff(t *testing.T) {
	location := agentLocation(t, "America/New_York")
	for _, test := range []struct {
		now   time.Time
		hours time.Duration
	}{
		{time.Date(2026, 3, 8, 23, 0, 0, 0, location), 23},
		{time.Date(2026, 11, 1, 23, 0, 0, 0, location), 25},
	} {
		tools := NewAgentTools(nil, nil, nil, 1, test.now, location).(*agentTools)
		start, end, err := tools.window("today")
		if err != nil || end.Sub(start) != test.hours*time.Hour || start.Hour() != 0 || end.Hour() != 0 {
			t.Fatalf("calendar day is not DST-aware: %v %v %v", start, end, err)
		}
		start, end, err = tools.window("reminder_window")
		if err != nil || !end.Equal(test.now) || end.Sub(start) != test.hours*time.Hour || start.Hour() != 23 {
			t.Fatalf("reminder cutoff is not DST-aware: %v %v %v", start, end, err)
		}
	}
	tools := NewAgentTools(nil, nil, nil, 1, time.Now(), nil).(*agentTools)
	if tools.location != time.UTC {
		t.Fatal("nil timezone should use UTC")
	}
}

func TestAgentToolsRawContentAndHonestTruncation(t *testing.T) {
	client := &agentFeedFixture{entries: []feedbin.Entry{
		{ID: 1, Content: "ç🦊ab", Summary: "must not replace content"},
		{ID: 2, Content: "<p>raw</p>"},
		{ID: 3, Summary: "source summary"},
		{ID: 4},
	}}
	tools := NewAgentTools(NewFeedbinDigestService(&agentConnectionFixture{}, client), nil, nil, 42, time.Now(), nil)
	page := agentResult(t, tools, "rss_articles", `{"scope":"all_unread","include_content":true,"max_content_chars":3}`)
	articles := page["articles"].([]any)
	first := articles[0].(map[string]any)
	if first["content"] != "ç🦊a" || first["truncated"] != true || first["content_chars"] != float64(4) || first["content_source"] != "feedbin_content" {
		t.Fatalf("Unicode content truncation incorrect: %v", first)
	}
	if articles[2].(map[string]any)["content_source"] != "feedbin_summary" || articles[3].(map[string]any)["content_source"] != "none" || articles[3].(map[string]any)["truncated"] != false {
		t.Fatalf("wrong source labels: %v", articles)
	}
	page = agentResult(t, tools, "rss_articles", `{"scope":"all_unread","include_content":true,"max_content_chars":20000}`)
	articles = page["articles"].([]any)
	if articles[1].(map[string]any)["content"] != "<p>raw</p>" || articles[1].(map[string]any)["truncated"] != false {
		t.Fatalf("content was transformed: %v", articles[1])
	}
	page = agentResult(t, tools, "rss_articles", `{"scope":"all_unread","include_content":true,"max_content_chars":4}`)
	if page["articles"].([]any)[0].(map[string]any)["truncated"] != false {
		t.Fatal("exactly-at-limit content reported truncated")
	}
}

func TestAgentToolsBookmarksDatesPaginationAndIdentity(t *testing.T) {
	location := agentLocation(t, "Europe/Istanbul")
	from := time.Date(2026, 10, 23, 0, 0, 0, 0, location)
	to := from.AddDate(0, 0, 1)
	readAt := from.Add(time.Hour)
	bookmarks := &agentBookmarkFixture{items: []domain.Bookmark{
		{ID: 3, UserID: 42, SavedAt: from.Add(time.Hour)},
		{ID: 2, UserID: 42, SavedAt: from, IsRead: true, ReadAt: &readAt},
		{ID: 1, UserID: 42, SavedAt: from},
		{ID: 1, UserID: 42, SavedAt: from},
		{ID: 4, UserID: 42, SavedAt: to},
		{ID: 5, UserID: 42, SavedAt: from.Add(-time.Nanosecond)},
		{ID: 6, UserID: 99, SavedAt: from},
	}}
	tools := NewAgentTools(nil, bookmarks, nil, 42, to, location)
	page := agentResult(t, tools, "bookmarks_list", `{"from":"2026-10-23","to":"2026-10-24","limit":1}`)
	if page["total"] != float64(3) || page["next_offset"] != float64(1) || page["bookmarks"].([]any)[0].(map[string]any)["id"] != float64(1) {
		t.Fatalf("wrong bookmark page: %v", page)
	}
	readAt = readAt.Add(time.Hour)
	bookmarks.items[1].Title = "changed"
	page = agentResult(t, tools, "bookmarks_list", `{"from":"2026-10-23","to":"2026-10-24","offset":1}`)
	item := page["bookmarks"].([]any)[0].(map[string]any)
	if item["read_at"] != from.Add(time.Hour).Format(time.RFC3339) || item["title"] == "changed" {
		t.Fatalf("bookmark snapshot changed: %v", item)
	}
	for _, key := range []string{"saved_at", "created_at", "updated_at", "is_read", "raindrop_bookmark_id"} {
		if _, ok := item[key]; !ok {
			t.Fatalf("missing %s: %v", key, item)
		}
	}
	if _, exists := item["user_id"]; exists {
		t.Fatal("identity exposed in result")
	}
	page = agentResult(t, tools, "bookmarks_list", `{"from":"2026-10-23","to":"2026-10-24","unread_only":true}`)
	if page["total"] != float64(2) || page["next_offset"] != nil {
		t.Fatalf("wrong unread filter: %v", page)
	}
	if len(bookmarks.users) != 1 || bookmarks.users[0] != 42 || !bookmarks.from.Equal(from) || !bookmarks.to.Equal(to) {
		t.Fatalf("wrong query or cache miss: %+v", bookmarks)
	}
	agentResult(t, tools, "bookmarks_list", `{"from":"2026-10-22","to":"2026-10-24"}`)
	if len(bookmarks.users) != 2 {
		t.Fatal("different date range needs its own snapshot")
	}
}

func TestAgentToolsStrictArgumentsHaveNoSideEffects(t *testing.T) {
	repo, client, bookmarks, web := &agentConnectionFixture{}, &agentFeedFixture{}, &agentBookmarkFixture{}, &agentWebFixture{}
	tools := NewAgentTools(NewFeedbinDigestService(repo, client), bookmarks, web, 42, time.Now(), nil)
	cases := []struct{ name, args string }{
		{"unknown", `{}`},
		{"rss_inventory", ``},
		{"rss_inventory", `null`},
		{"rss_inventory", `[]`},
		{"rss_inventory", `{}`},
		{"rss_inventory", `{"scope":"all_unread"} {}`},
		{"rss_inventory", `{"scope":"all_unread"} garbage`},
		{"rss_inventory", `{"scope":"all_unread","scope":"today"}`},
		{"rss_inventory", `{"Scope":"today"}`},
		{"rss_inventory", `{"scope":null}`},
		{"rss_inventory", `{"scope":"last_week"}`},
		{"rss_inventory", `{"scope":12}`},
		{"rss_inventory", `{"scope":"today","limit":1}`},
		{"rss_inventory", `{"scope":"today","user_id":99}`},
		{"rss_inventory", `{"scope":"today","userID":99}`},
		{"rss_inventory", `{"scope":"today","user_ids":[99]}`},
		{"rss_articles", `{"scope":"today","offset":-1}`},
		{"rss_articles", `{"scope":"today","limit":0}`},
		{"rss_articles", `{"scope":"today","limit":101}`},
		{"rss_articles", `{"scope":"today","limit":1.5}`},
		{"rss_articles", `{"scope":"today","limit":"50"}`},
		{"rss_articles", `{"scope":"today","limit":null}`},
		{"rss_articles", `{"scope":"today","offset":9223372036854775808}`},
		{"rss_articles", `{"scope":"today","include_content":"true"}`},
		{"rss_articles", `{"scope":"today","max_content_chars":0}`},
		{"rss_articles", `{"scope":"today","max_content_chars":20001}`},
		{"bookmarks_list", `{"from":"2026-10-23"}`},
		{"bookmarks_list", `{"from":"2026-10-24","to":"2026-10-24"}`},
		{"bookmarks_list", `{"from":"2026-10-25","to":"2026-10-24"}`},
		{"bookmarks_list", `{"from":"2026-02-30","to":"2026-10-24"}`},
		{"bookmarks_list", `{"from":"2026-10-23T00:00:00Z","to":"2026-10-24"}`},
		{"bookmarks_list", `{"from":"2026-10-23","to":"2026-10-24","user_id":99}`},
		{"web_search", `{"query":" "}`},
		{"web_search", `{"query":{"user_id":99}}`},
		{"web_search", `{"query":"hello","user_id":99}`},
		{"web_search", fmt.Sprintf(`{"query":%q}`, strings.Repeat("ç", 1501))},
		{"web_search", fmt.Sprintf(`{"query":%q}`, strings.Repeat("x", 16385))},
	}
	for _, test := range cases {
		t.Run(test.name+test.args[:min(80, len(test.args))], func(t *testing.T) {
			output, err := tools.Call(context.Background(), test.name, json.RawMessage(test.args))
			if err == nil || output != "" {
				t.Fatalf("accepted invalid arguments: %s / %v", output, err)
			}
		})
	}
	if len(repo.users)+client.entryCalls+client.taggingCalls+len(bookmarks.users)+len(web.queries) != 0 {
		t.Fatal("invalid arguments caused I/O")
	}
}

func TestAgentToolsSafeErrorsAndOptionalWeb(t *testing.T) {
	secret := errors.New("https://private@example.org:secret-password@provider.test failed")
	for _, source := range []string{"connection", "entries", "taggings", "bookmarks", "web"} {
		t.Run(source, func(t *testing.T) {
			repo, client, bookmarks, web := &agentConnectionFixture{}, &agentFeedFixture{}, &agentBookmarkFixture{}, &agentWebFixture{}
			name, args := "rss_inventory", `{"scope":"all_unread"}`
			switch source {
			case "connection":
				repo.err = secret
			case "entries":
				client.entryErr = secret
			case "taggings":
				client.taggingErr = secret
			case "bookmarks":
				bookmarks.err = secret
				name, args = "bookmarks_list", `{"from":"2026-10-23","to":"2026-10-24"}`
			case "web":
				web.err = secret
				name, args = "web_search", `{"query":"example"}`
			}
			tools := NewAgentTools(NewFeedbinDigestService(repo, client), bookmarks, web, 42, time.Now(), nil)
			for i := 0; i < 2; i++ {
				output, err := tools.Call(context.Background(), name, json.RawMessage(args))
				if output != "" || err == nil || strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "secret-password") || errors.Is(err, secret) {
					t.Fatalf("unsafe/partial failure: %q %v", output, err)
				}
			}
			if len(repo.users) > 1 || client.entryCalls > 1 || client.taggingCalls > 1 || len(bookmarks.users) > 1 {
				t.Fatal("failed snapshot fetched repeatedly")
			}
		})
	}
	tools := NewAgentTools(nil, nil, nil, 42, time.Now(), nil)
	for _, definition := range tools.Definitions() {
		if definition.Name == "web_search" {
			t.Fatal("unconfigured web search advertised")
		}
	}
	if _, err := tools.Call(context.Background(), "web_search", json.RawMessage(`{"query":"example"}`)); err == nil {
		t.Fatal("unconfigured web search accepted")
	}
	web := &agentWebFixture{}
	tools = NewAgentTools(nil, nil, web, 42, time.Now(), nil)
	result, err := tools.Call(context.Background(), "web_search", json.RawMessage(`{"query":"explicit query"}`))
	if err != nil || result != "external provider output" || len(web.queries) != 1 || web.queries[0] != "explicit query" {
		t.Fatalf("web wrapper changed output: %q %v", result, err)
	}
}

func TestAgentToolsConcurrentCallsShareSnapshot(t *testing.T) {
	repo, client := &agentConnectionFixture{}, &agentFeedFixture{entries: []feedbin.Entry{{ID: 1}}}
	bookmarks := &agentBookmarkFixture{}
	tools := NewAgentTools(NewFeedbinDigestService(repo, client), bookmarks, nil, 42, time.Now(), nil)
	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name, args := "rss_inventory", `{"scope":"all_unread"}`
			if i%3 == 1 {
				name = "rss_articles"
			} else if i%3 == 2 {
				name, args = "bookmarks_list", `{"from":"2026-10-23","to":"2026-10-24"}`
			}
			if _, err := tools.Call(context.Background(), name, json.RawMessage(args)); err != nil {
				t.Errorf("concurrent call failed: %v", err)
			}
		}(i)
	}
	wg.Wait()
	if len(repo.users) != 1 || client.entryCalls != 1 || client.taggingCalls != 1 || len(bookmarks.users) != 1 {
		t.Fatal("concurrent calls fetched more than one snapshot")
	}
}

func TestAgentToolsEmptyResultsDefinitionsAndCancellation(t *testing.T) {
	repo, client := &agentConnectionFixture{}, &agentFeedFixture{}
	tools := NewAgentTools(NewFeedbinDigestService(repo, client), &agentBookmarkFixture{}, nil, 42, time.Now(), nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := tools.Call(ctx, "rss_inventory", json.RawMessage(`{"scope":"all_unread"}`)); !errors.Is(err, context.Canceled) || len(repo.users) != 0 {
		t.Fatalf("canceled call started loading: %v", err)
	}
	stats := agentResult(t, tools, "rss_inventory", `{"scope":"all_unread"}`)
	if stats["total"] != float64(0) || stats["oldest_created_at"] != nil || stats["newest_created_at"] != nil || len(stats["categories"].([]any)) != 0 {
		t.Fatalf("incorrect empty inventory: %v", stats)
	}
	page := agentResult(t, tools, "rss_articles", `{"scope":"all_unread"}`)
	if len(page["articles"].([]any)) != 0 || page["next_offset"] != nil {
		t.Fatalf("incorrect empty article page: %v", page)
	}
	page = agentResult(t, tools, "bookmarks_list", `{"from":"2026-10-23","to":"2026-10-24"}`)
	if len(page["bookmarks"].([]any)) != 0 || page["next_offset"] != nil {
		t.Fatalf("incorrect empty bookmark page: %v", page)
	}
	definitions := tools.Definitions()
	encoded, err := json.Marshal(definitions[0])
	if err != nil || !strings.Contains(string(encoded), `"inputSchema"`) || !strings.Contains(string(encoded), `"name":"rss_inventory"`) || !strings.Contains(string(encoded), `"description":`) {
		t.Fatalf("wrong public definition fields: %s %v", encoded, err)
	}
	definitions[0].InputSchema["properties"].(map[string]any)["user_id"] = map[string]any{"type": "integer"}
	if _, err := tools.Call(context.Background(), "rss_inventory", json.RawMessage(`{"scope":"all_unread","user_id":99}`)); err == nil {
		t.Fatal("caller could mutate tool validation through Definitions")
	}
	invalid := NewAgentTools(nil, nil, nil, 0, time.Now(), nil)
	if _, err := invalid.Call(context.Background(), "rss_inventory", json.RawMessage(`{"scope":"all_unread"}`)); err == nil {
		t.Fatal("invalid bound identity accepted")
	}
	// Separate runs must not reuse a previous user's snapshot.
	other := NewAgentTools(NewFeedbinDigestService(repo, client), nil, nil, 99, time.Now(), nil)
	agentResult(t, other, "rss_inventory", `{"scope":"all_unread"}`)
	if fmt.Sprint(repo.users) != "[42 99]" || client.entryCalls != 2 {
		t.Fatalf("snapshot leaked between toolsets: %v", repo.users)
	}
}
