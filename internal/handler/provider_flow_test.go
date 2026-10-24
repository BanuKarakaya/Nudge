package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"nudge/internal/domain"
	"nudge/internal/feedbin"
	"nudge/internal/repository"
	"nudge/internal/service"
)

type callerInstallations struct {
	repository.InstallationRepository
}

func (callerInstallations) GetUserIDBySlackUserID(_ context.Context, team, user string) (int64, error) {
	if team == "T" && user == "U" {
		return 11, nil
	}
	return 22, nil
}
func (callerInstallations) GetByUserID(_ context.Context, user int64) (domain.SlackInstallation, error) {
	return domain.SlackInstallation{BotToken: fmt.Sprint(user)}, nil
}

type callerBookmarks struct {
	service.BookmarkService
	users     []int64
	locations []string
}

func (b *callerBookmarks) ListByDate(_ context.Context, user int64, from, to time.Time) ([]domain.Bookmark, error) {
	b.users = append(b.users, user)
	b.locations = append(b.locations, from.Location().String())
	return []domain.Bookmark{{ID: user, UserID: user, Title: fmt.Sprintf("Bookmark %d", user), SavedAt: from.Add(time.Hour)}}, nil
}

type callerConnections struct {
	repository.FeedbinConnectionRepository
	users []int64
}

func (c *callerConnections) GetByUserID(_ context.Context, user int64) (domain.FeedbinConnection, error) {
	c.users = append(c.users, user)
	return domain.FeedbinConnection{Email: fmt.Sprint(user), Password: "secret"}, nil
}

type callerFeedbin struct{ emails []string }

func (f *callerFeedbin) ListUnreadSince(_ context.Context, email, password string, since time.Time) ([]feedbin.Entry, error) {
	f.emails = append(f.emails, email)
	return []feedbin.Entry{{ID: 1, Title: "RSS " + email, URL: "https://example.com/" + email, CreatedAt: time.Now()}}, nil
}
func (*callerFeedbin) ListTaggings(context.Context, string, string) ([]feedbin.Tagging, error) {
	return nil, nil
}

type webFunc func(context.Context, string) (string, error)

func (f webFunc) SearchWeb(ctx context.Context, input string) (string, error) { return f(ctx, input) }

func TestAgentToolsAreBoundToResolvedCallerAndConfiguredTimezone(t *testing.T) {
	location, err := time.LoadLocation("Europe/Istanbul")
	if err != nil {
		t.Fatal(err)
	}
	bookmarks := &callerBookmarks{}
	connections := &callerConnections{}
	feed := &callerFeedbin{}
	digest := service.NewFeedbinDigestService(connections, feed)
	searches := 0
	web := webFunc(func(_ context.Context, query string) (string, error) {
		searches++
		if query != "agent architecture" {
			t.Fatalf("unexpected query: %q", query)
		}
		return "external source", nil
	})
	calls := 0
	runtime := agentFunc(func(ctx context.Context, request service.AgentRequest) (service.AgentResult, error) {
		calls++
		user := int64(11)
		if request.Key.User == "U2" {
			user = 22
		}
		assertAgentInput(t, request.Input, "kaynaklarım", "Europe/Istanbul")
		if request.Tools == nil {
			t.Fatal("missing caller tools")
		}
		bookmarkArgs := json.RawMessage(`{"from":"2026-10-01","to":"2026-10-02"}`)
		raw, err := request.Tools.Call(ctx, "bookmarks_list", bookmarkArgs)
		if err != nil || !strings.Contains(raw, fmt.Sprintf("Bookmark %d", user)) {
			t.Fatalf("wrong bookmark tool binding: %s %v", raw, err)
		}
		// A model cannot select a different account via tool arguments.
		if _, err := request.Tools.Call(ctx, "bookmarks_list", json.RawMessage(`{"from":"2026-10-01","to":"2026-10-02","user_id":999}`)); err == nil {
			t.Fatal("tool accepted model-selected user")
		}
		for _, name := range []string{"rss_inventory", "rss_articles"} {
			raw, err = request.Tools.Call(ctx, name, json.RawMessage(`{"scope":"all_unread"}`))
			if err != nil {
				t.Fatal(err)
			}
		}
		if !strings.Contains(raw, "RSS "+fmt.Sprint(user)) {
			t.Fatalf("wrong RSS binding: %s", raw)
		}
		raw, err = request.Tools.Call(ctx, "rss_inventory", json.RawMessage(`{"scope":"today"}`))
		if err != nil {
			t.Fatal(err)
		}
		var window struct {
			Timezone  string
			Reference time.Time `json:"reference_time"`
		}
		if err = json.Unmarshal([]byte(raw), &window); err != nil || window.Timezone != "Europe/Istanbul" || time.Since(window.Reference) > time.Minute {
			t.Fatalf("wrong bound clock: %s %v", raw, err)
		}
		if !strings.Contains(request.Input, "Current time: "+window.Reference.Format(time.RFC3339)) {
			t.Fatalf("model and tools received different clocks: %s", request.Input)
		}
		raw, err = request.Tools.Call(ctx, "web_search", json.RawMessage(`{"query":"agent architecture"}`))
		if err != nil || raw != "external source" {
			t.Fatalf("web dependency missing: %s %v", raw, err)
		}
		return service.AgentResult{ThreadID: fmt.Sprint(user), Text: "Agent synthesized answer"}, nil
	})
	sender := &conversationSender{}
	h := NewSlackEventHandler("signing", callerInstallations{}, bookmarks, digest, runtime, web, make(conversationTestStore), sender, location)
	for _, user := range []string{"U", "U2", "U"} {
		event := conversationEvent("<@B123> kaynaklarım")
		event.Event.User = user
		event.EventID = fmt.Sprintf("event-%d", calls)
		h.replyWithSummary(event)
	}
	if calls != 3 || searches != 3 || len(sender.messages) != 3 {
		t.Fatalf("runs=%d searches=%d messages=%v", calls, searches, sender.messages)
	}
	if !reflect.DeepEqual(bookmarks.users, []int64{11, 22, 11}) || !reflect.DeepEqual(connections.users, []int64{11, 22, 11}) || !reflect.DeepEqual(feed.emails, []string{"11", "22", "11"}) {
		t.Fatalf("caller scope or per-run refresh wrong: bookmarks=%v connections=%v feed=%v", bookmarks.users, connections.users, feed.emails)
	}
	for _, got := range bookmarks.locations {
		if got != "Europe/Istanbul" {
			t.Fatalf("bookmark timezone=%s", got)
		}
	}
}

func TestDeliveredAgentUsageIsPersistedAndUnknownUsageStaysNil(t *testing.T) {
	store := make(conversationTestStore)
	key := service.ConversationKey{Team: "T", Channel: "C", Thread: "1.0", User: "U"}
	sender := &conversationSender{}
	var prior *service.AgentTokenUsage
	for i, usage := range []*service.AgentTokenUsage{{Input: 100, Output: 20, Total: 120}, nil} {
		expectedPrior := prior
		runtime := agentFunc(func(_ context.Context, request service.AgentRequest) (service.AgentResult, error) {
			if !reflect.DeepEqual(request.History.AgentTokenUsage, expectedPrior) {
				t.Fatalf("lost usage baseline: %+v", request.History.AgentTokenUsage)
			}
			return service.AgentResult{ThreadID: "native-thread", Text: "Delivered", TokenUsage: usage}, nil
		})
		sender.beforeSend = func(context.Context, string) {
			state, _ := store.Load(context.Background(), key)
			if !reflect.DeepEqual(state.AgentTokenUsage, expectedPrior) {
				t.Fatal("usage persisted before delivery")
			}
		}
		h := NewSlackEventHandler("", conversationInstallations{}, nil, nil, runtime, nil, store, sender, nil)
		event := conversationEvent("RSS özetimi ver")
		event.Event.TS = fmt.Sprintf("%d.0", i+1)
		event.Event.ThreadTS = "1.0"
		h.replyWithSummary(event)
		state, err := store.Load(context.Background(), key)
		if err != nil || !reflect.DeepEqual(state.AgentTokenUsage, usage) {
			t.Fatalf("usage=%+v want=%+v err=%v", state.AgentTokenUsage, usage, err)
		}
		prior = usage
	}
}
