package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"nudge/internal/domain"
	"nudge/internal/feedbin"
)

type ToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

type AgentTools interface {
	Definitions() []ToolDefinition
	Call(context.Context, string, json.RawMessage) (string, error)
}

const (
	agentPageLimit    = 100
	agentContentLimit = 20000
)

var errAgentArguments = errors.New("invalid tool arguments")

type agentRSSSnapshot struct {
	entries    []feedbin.Entry
	categories map[int64][]string
}

type agentTools struct {
	now           time.Time
	location      *time.Location
	loadRSS       func(context.Context) (agentRSSSnapshot, error)
	loadBookmarks func(context.Context, time.Time, time.Time) ([]domain.Bookmark, error)
	web           WebAssistant
	validIdentity bool
}

var _ AgentTools = (*agentTools)(nil)

// NewAgentTools binds identity and time to one agent run. RSS is fetched once
// across all scopes, and bookmarks once per date range, including failed loads.
// Create a new toolset to refresh data. The closures never accept a tool user ID.
func NewAgentTools(digest *FeedbinDigestService, bookmarks BookmarkService, web WebAssistant, userID int64, now time.Time, timezone *time.Location) AgentTools {
	if timezone == nil {
		timezone = time.UTC
	}
	t := &agentTools{now: now.In(timezone), location: timezone, web: web, validIdentity: userID > 0}
	var rssOnce sync.Once
	var snapshot agentRSSSnapshot
	var rssErr error
	t.loadRSS = func(ctx context.Context) (agentRSSSnapshot, error) {
		rssOnce.Do(func() {
			rssErr = errors.New("RSS inventory unavailable")
			if digest == nil || digest.connections == nil || digest.client == nil {
				return
			}
			connection, err := digest.connections.GetByUserID(ctx, userID)
			if err != nil {
				return
			}
			entries, err := digest.client.ListUnreadSince(ctx, connection.Email, connection.Password, time.Time{})
			if err != nil {
				return
			}
			taggings, err := digest.client.ListTaggings(ctx, connection.Email, connection.Password)
			if err != nil {
				return
			}
			snapshot.categories = make(map[int64][]string)
			byFeed := make(map[int64]map[string]bool)
			for _, tag := range taggings {
				if byFeed[tag.FeedID] == nil {
					byFeed[tag.FeedID] = make(map[string]bool)
				}
				byFeed[tag.FeedID][tag.Name] = true
			}
			for feedID, names := range byFeed {
				for name := range names {
					snapshot.categories[feedID] = append(snapshot.categories[feedID], name)
				}
				sort.Strings(snapshot.categories[feedID])
			}
			seen := make(map[int64]bool)
			for _, entry := range entries {
				if !seen[entry.ID] {
					seen[entry.ID] = true
					snapshot.entries = append(snapshot.entries, entry)
				}
			}
			sort.Slice(snapshot.entries, func(i, j int) bool {
				a, b := snapshot.entries[i], snapshot.entries[j]
				if a.CreatedAt.Equal(b.CreatedAt) {
					return a.ID < b.ID
				}
				return a.CreatedAt.Before(b.CreatedAt)
			})
			rssErr = nil
		})
		return snapshot, rssErr
	}
	// Copy repository data so later repository mutations cannot change pagination.
	type bookmarkSnapshot struct {
		items []domain.Bookmark
		err   error
	}
	var bookmarkMu sync.Mutex
	bookmarkCache := make(map[[2]time.Time]bookmarkSnapshot)
	t.loadBookmarks = func(ctx context.Context, from, to time.Time) ([]domain.Bookmark, error) {
		bookmarkMu.Lock()
		defer bookmarkMu.Unlock()
		key := [2]time.Time{from, to}
		if cached, ok := bookmarkCache[key]; ok {
			return cached.items, cached.err
		}
		cached := bookmarkSnapshot{err: errors.New("bookmarks unavailable")}
		defer func() { bookmarkCache[key] = cached }()
		if bookmarks == nil {
			return nil, cached.err
		}
		items, err := bookmarks.ListByDate(ctx, userID, from, to)
		if err != nil {
			return nil, cached.err
		}
		seen := make(map[int64]bool)
		for _, item := range items {
			if item.UserID != userID || item.SavedAt.Before(from) || !item.SavedAt.Before(to) || seen[item.ID] {
				continue
			}
			seen[item.ID] = true
			if item.ReadAt != nil {
				readAt := *item.ReadAt
				item.ReadAt = &readAt
			}
			cached.items = append(cached.items, item)
		}
		sort.Slice(cached.items, func(i, j int) bool {
			a, b := cached.items[i], cached.items[j]
			if a.SavedAt.Equal(b.SavedAt) {
				return a.ID < b.ID
			}
			return a.SavedAt.Before(b.SavedAt)
		})
		cached.err = nil
		return cached.items, nil
	}
	return t
}

func (t *agentTools) Definitions() []ToolDefinition {
	scope := map[string]any{"type": "string", "enum": []string{"all_unread", "today", "reminder_window"}, "description": "all_unread: complete unread inventory; today: local calendar midnight to next midnight; reminder_window: most recent completed local 23:00-to-23:00 window. Windows are start-inclusive, end-exclusive and use Feedbin created_at."}
	page := func() map[string]any {
		return map[string]any{
			"offset": map[string]any{"type": "integer", "minimum": 0, "description": "Zero-based offset; default 0. Follow next_offset until null to read the entire inventory."},
			"limit":  map[string]any{"type": "integer", "minimum": 1, "maximum": agentPageLimit, "default": 50},
		}
	}
	object := func(properties map[string]any, required ...string) map[string]any {
		return map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
	}
	articleProps := page()
	articleProps["scope"] = scope
	articleProps["include_content"] = map[string]any{"type": "boolean", "default": false, "description": "Include raw Feedbin content (possibly HTML), falling back to the source summary only if content is empty. No extraction request or generated summary."}
	articleProps["max_content_chars"] = map[string]any{"type": "integer", "minimum": 1, "maximum": agentContentLimit, "default": 4000, "description": "Per-item Unicode character limit when include_content=true; content_source and truncated describe what was returned."}
	bookmarkProps := page()
	bookmarkProps["from"] = map[string]any{"type": "string", "format": "date", "description": "Inclusive YYYY-MM-DD in the bound timezone."}
	bookmarkProps["to"] = map[string]any{"type": "string", "format": "date", "description": "Exclusive YYYY-MM-DD in the bound timezone."}
	bookmarkProps["unread_only"] = map[string]any{"type": "boolean", "default": false}
	definitions := []ToolDefinition{
		{Name: "rss_inventory", Description: "Raw unread RSS statistics for an explicit scope: total, oldest/newest created_at, category counts and uncategorized_count. Category counts can overlap; total deduplicates numeric entry IDs. Reuses this run's complete unread snapshot.", InputSchema: object(map[string]any{"scope": scope}, "scope")},
		{Name: "rss_articles", Description: "Paginate every unread RSS article in an explicit scope, oldest created_at then numeric ID. Returns titles, URLs, dates, categories and optional bounded raw content. next_offset=null means complete; no overall inventory cap. Reuses the inventory snapshot.", InputSchema: object(articleProps, "scope")},
		{Name: "bookmarks_list", Description: "Raw bookmarks saved in a local date range, including read state and dates. Ordered by saved_at then numeric ID; paginated without an overall cap. Reuses one snapshot per date range, with unread filtering in memory.", InputSchema: object(bookmarkProps, "from", "to")},
	}
	if t.web != nil {
		definitions = append(definitions, ToolDefinition{Name: "web_search", Description: "Explicit external web search through the configured WebAssistant. Returns provider output verbatim; never used implicitly for RSS or bookmarks.", InputSchema: object(map[string]any{"query": map[string]any{"type": "string", "minLength": 1, "maxLength": 1500}}, "query")})
	}
	return definitions
}

// Decode exact JSON property names, rejecting duplicate keys, null values,
// unknown fields (including user IDs), trailing JSON and non-object arguments.
func decodeAgentArguments(raw json.RawMessage, schema map[string]any, target any) error {
	if len(raw) > 16384 || !utf8.Valid(raw) {
		return errAgentArguments
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	first, err := d.Token()
	if err != nil || first != json.Delim('{') {
		return errAgentArguments
	}
	properties := schema["properties"].(map[string]any)
	seen := make(map[string]bool)
	for d.More() {
		key, err := d.Token()
		if err != nil {
			return errAgentArguments
		}
		name, ok := key.(string)
		if !ok || seen[name] || properties[name] == nil {
			return errAgentArguments
		}
		seen[name] = true
		var value json.RawMessage
		if d.Decode(&value) != nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return errAgentArguments
		}
	}
	if end, err := d.Token(); err != nil || end != json.Delim('}') {
		return errAgentArguments
	}
	if _, err := d.Token(); err != io.EOF {
		return errAgentArguments
	}
	for _, name := range schema["required"].([]string) {
		if !seen[name] {
			return errAgentArguments
		}
	}
	if json.Unmarshal(raw, target) != nil {
		return errAgentArguments
	}
	return nil
}

type agentToolArgs struct {
	Scope           string `json:"scope"`
	Offset          int    `json:"offset"`
	Limit           int    `json:"limit"`
	IncludeContent  bool   `json:"include_content"`
	MaxContentChars int    `json:"max_content_chars"`
	From            string `json:"from"`
	To              string `json:"to"`
	UnreadOnly      bool   `json:"unread_only"`
	Query           string `json:"query"`
}

func (t *agentTools) Call(ctx context.Context, name string, raw json.RawMessage) (string, error) {
	if !t.validIdentity {
		return "", errors.New("tool identity unavailable")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var schema map[string]any
	for _, definition := range t.Definitions() {
		if definition.Name == name {
			schema = definition.InputSchema
			break
		}
	}
	if schema == nil {
		return "", errors.New("unknown tool")
	}
	args := agentToolArgs{Limit: 50, MaxContentChars: 4000}
	if err := decodeAgentArguments(raw, schema, &args); err != nil {
		return "", err
	}
	if args.Offset < 0 || args.Limit < 1 || args.Limit > agentPageLimit || args.MaxContentChars < 1 || args.MaxContentChars > agentContentLimit {
		return "", errAgentArguments
	}
	if name == "web_search" {
		if strings.TrimSpace(args.Query) == "" || utf8.RuneCountInString(args.Query) > 1500 {
			return "", errAgentArguments
		}
		result, err := t.web.SearchWeb(ctx, args.Query)
		if err != nil {
			return "", errors.New("web search unavailable")
		}
		return result, nil
	}
	if name == "bookmarks_list" {
		return t.bookmarkPage(ctx, args)
	}
	return t.rssResult(ctx, name, args)
}

func (t *agentTools) window(scope string) (time.Time, time.Time, error) {
	midnight := time.Date(t.now.Year(), t.now.Month(), t.now.Day(), 0, 0, 0, 0, t.location)
	switch scope {
	case "all_unread":
		return time.Time{}, time.Time{}, nil
	case "today":
		return midnight, midnight.AddDate(0, 0, 1), nil
	case "reminder_window":
		end := time.Date(t.now.Year(), t.now.Month(), t.now.Day(), 23, 0, 0, 0, t.location)
		if t.now.Before(end) {
			end = end.AddDate(0, 0, -1)
		}
		return end.AddDate(0, 0, -1), end, nil
	default:
		return time.Time{}, time.Time{}, errAgentArguments
	}
}

func (t *agentTools) rssResult(ctx context.Context, name string, args agentToolArgs) (string, error) {
	from, to, err := t.window(args.Scope)
	if err != nil {
		return "", err
	}
	snapshot, err := t.loadRSS(ctx)
	if err != nil {
		return "", err
	}
	items := make([]feedbin.Entry, 0, len(snapshot.entries))
	for _, entry := range snapshot.entries {
		if args.Scope == "all_unread" || (!entry.CreatedAt.Before(from) && entry.CreatedAt.Before(to)) {
			items = append(items, entry)
		}
	}
	result := map[string]any{"scope": args.Scope, "timezone": t.location.String(), "reference_time": t.now, "window_start": nil, "window_end": nil, "total": len(items)}
	if args.Scope != "all_unread" {
		result["window_start"], result["window_end"] = from, to
	}
	if name == "rss_inventory" {
		result["oldest_created_at"], result["newest_created_at"] = nil, nil
		if len(items) > 0 {
			result["oldest_created_at"], result["newest_created_at"] = items[0].CreatedAt, items[len(items)-1].CreatedAt
		}
		counts := make(map[string]int)
		uncategorized := 0
		for _, entry := range items {
			names := snapshot.categories[entry.FeedID]
			if len(names) == 0 {
				uncategorized++
			}
			for _, name := range names {
				counts[name]++
			}
		}
		categories := make([]map[string]any, 0, len(counts))
		names := make([]string, 0, len(counts))
		for name := range counts {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			categories = append(categories, map[string]any{"name": name, "count": counts[name]})
		}
		result["categories"], result["uncategorized_count"] = categories, uncategorized
	} else {
		start, end := agentPageBounds(result, len(items), args)
		articles := make([]map[string]any, 0, end-start)
		for _, entry := range items[start:end] {
			categories := append([]string{}, snapshot.categories[entry.FeedID]...)
			article := map[string]any{"id": entry.ID, "feed_id": entry.FeedID, "title": entry.Title, "url": entry.URL, "created_at": entry.CreatedAt, "categories": categories}
			if args.IncludeContent {
				content, source := entry.Content, "feedbin_content"
				if content == "" {
					content, source = entry.Summary, "feedbin_summary"
				}
				if content == "" {
					source = "none"
				}
				characters := []rune(content)
				article["content"], article["content_source"] = string(characters[:min(len(characters), args.MaxContentChars)]), source
				article["content_chars"], article["truncated"] = len(characters), len(characters) > args.MaxContentChars
			}
			articles = append(articles, article)
		}
		result["articles"] = articles
	}
	return marshalAgentResult(result)
}

func (t *agentTools) bookmarkPage(ctx context.Context, args agentToolArgs) (string, error) {
	from, errFrom := time.ParseInLocation(time.DateOnly, args.From, t.location)
	to, errTo := time.ParseInLocation(time.DateOnly, args.To, t.location)
	if errFrom != nil || errTo != nil || from.IsZero() || !from.Before(to) {
		return "", errAgentArguments
	}
	items, err := t.loadBookmarks(ctx, from, to)
	if err != nil {
		return "", err
	}
	filtered := make([]domain.Bookmark, 0, len(items))
	for _, item := range items {
		if !args.UnreadOnly || !item.IsRead {
			filtered = append(filtered, item)
		}
	}
	result := map[string]any{"from": args.From, "to": args.To, "window_start": from, "window_end": to, "timezone": t.location.String(), "unread_only": args.UnreadOnly, "total": len(filtered)}
	start, end := agentPageBounds(result, len(filtered), args)
	bookmarks := make([]map[string]any, 0, end-start)
	for _, item := range filtered[start:end] {
		bookmarks = append(bookmarks, map[string]any{"id": item.ID, "raindrop_bookmark_id": item.RaindropBookmarkID, "title": item.Title, "url": item.URL, "saved_at": item.SavedAt, "is_read": item.IsRead, "read_at": item.ReadAt, "created_at": item.CreatedAt, "updated_at": item.UpdatedAt})
	}
	result["bookmarks"] = bookmarks
	return marshalAgentResult(result)
}

func agentPageBounds(result map[string]any, total int, args agentToolArgs) (int, int) {
	start := min(args.Offset, total)
	end := start + min(args.Limit, total-start)
	result["offset"], result["limit"], result["next_offset"] = args.Offset, args.Limit, nil
	if end < total {
		result["next_offset"] = end
	}
	return start, end
}

func marshalAgentResult(result any) (string, error) {
	encoded, err := json.Marshal(result)
	if err != nil {
		return "", errors.New("tool result unavailable")
	}
	return string(encoded), nil
}
