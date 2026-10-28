package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"nudge/internal/coding"
	"nudge/internal/service"
)

type buttonEditStore struct {
	coding.EditStore
	mu sync.Mutex
	e  coding.Edit
}

func (s *buttonEditStore) GetEdit(_ context.Context, user int64, id string) (coding.Edit, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if user != s.e.Task.Identity.UserID || id != s.e.Task.ID {
		return coding.Edit{}, coding.ErrNotFound
	}
	return s.e, nil
}
func (s *buttonEditStore) WithEdit(_ context.Context, user int64, id string, fn func(*coding.Edit) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if user != s.e.Task.Identity.UserID || id != s.e.Task.ID {
		return coding.ErrNotFound
	}
	e := s.e
	if err := fn(&e); err != nil {
		return err
	}
	s.e = e
	return nil
}

type buttonGit struct {
	coding.GitWorkspace
	calls atomic.Int32
	gate  chan struct{}
}

func (g *buttonGit) Push(ctx context.Context, _ string, _ coding.Review) (string, error) {
	g.calls.Add(1)
	if g.gate != nil {
		select {
		case <-g.gate:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	return strings.Repeat("b", 40), nil
}

type buttonInstallations struct{ codingInstallations }

func (buttonInstallations) GetUserIDBySlackUserID(_ context.Context, team, user string) (int64, error) {
	if team == "T" && user == "U" {
		return 1, nil
	}
	return 2, nil
}

type buttonClient struct {
	sent    chan string
	updates chan []map[string]any
}

func (c *buttonClient) OpenDirectMessage(context.Context, string, string) (string, error) {
	return "D", nil
}
func (c *buttonClient) PostMessage(_ context.Context, _, _, text string) error {
	c.sent <- text
	return nil
}
func (c *buttonClient) PostMessageInThread(_ context.Context, _, _, _, text string) error {
	c.sent <- text
	return nil
}
func (c *buttonClient) PostMessageInThreadWithBlocks(_ context.Context, _, _, _, _ string, blocks []map[string]any) error {
	c.updates <- blocks
	return nil
}
func (c *buttonClient) UpdateMessageBlocks(_ context.Context, _, _, _ string, blocks []map[string]any) error {
	c.updates <- blocks
	return nil
}

func buttonFixture(t *testing.T) (*SlackInteractionHandler, *CodingHandler, *buttonEditStore, *buttonGit, *buttonClient, slackInteractionPayload) {
	t.Helper()
	h, store, _ := reviewHandlerFixture(t)
	store.edit.Task.Identity.Thread = "100.0001"
	edits := &buttonEditStore{e: store.edit}
	git := &buttonGit{}
	if err := h.service.EnableEditing(edits, git); err != nil {
		t.Fatal(err)
	}
	client := &buttonClient{sent: make(chan string, 10), updates: make(chan []map[string]any, 10)}
	h.client = client
	h.installations = buttonInstallations{}
	info := coding.EditInfo{Task: store.edit.Task, Digest: store.edit.Digest}
	reply := &codingReply{h: h, identity: store.edit.Task.Identity, reviews: map[string]coding.EditInfo{info.Task.ID: info}}
	blocks, err := reply.blocks("Kod hazır; test ettikten sonra Pushla’ya bas.")
	if err != nil {
		t.Fatal(err)
	}
	value := blocks[len(blocks)-1]["elements"].([]map[string]any)[0]["value"].(string)
	raw, _ := json.Marshal(map[string]any{"type": "block_actions", "team": map[string]string{"id": "T"}, "user": map[string]string{"id": "U"}, "channel": map[string]string{"id": "C"}, "container": map[string]string{"channel_id": "C", "message_ts": "101.0001"}, "message": map[string]any{"ts": "101.0001", "thread_ts": "100.0001", "blocks": blocks}, "actions": []map[string]string{{"action_id": codingPushAction, "action_ts": "102.0001", "value": value}}})
	var p slackInteractionPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	return NewSlackInteractionHandler("signing", nil, h.installations).WithCoding(h), h, edits, git, client, p
}

func clickRequest(t *testing.T, h *SlackInteractionHandler, p slackInteractionPayload, signed bool) *httptest.ResponseRecorder {
	t.Helper()
	raw, _ := json.Marshal(p)
	body := url.Values{"payload": {string(raw)}}.Encode()
	r := httptest.NewRequest(http.MethodPost, "/slack/interactions", strings.NewReader(body))
	if signed {
		signSlackRequest(r, body)
	}
	w := httptest.NewRecorder()
	h.handle(w, r)
	return w
}
func awaitButtonResult(t *testing.T, c *buttonClient) string {
	t.Helper()
	select {
	case result := <-c.sent:
		return result
	case <-time.After(3 * time.Second):
		t.Fatal("button job did not report result")
		return ""
	}
}

func TestPushButtonAcknowledgesBeforeGitAndPublishesOnce(t *testing.T) {
	h, _, _, git, client, p := buttonFixture(t)
	git.gate = make(chan struct{})
	start := time.Now()
	w := clickRequest(t, h, p, true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "isteğini aldım") || time.Since(start) > time.Second {
		t.Fatal("slow or failed acknowledgment", w.Body.String())
	}
	close(git.gate)
	if result := awaitButtonResult(t, client); !strings.Contains(result, "GitHub’a pushlandı") {
		t.Fatal(result)
	}
	blocks := <-client.updates
	raw, _ := json.Marshal(blocks)
	if strings.Contains(string(raw), `"action_id":"coding_push"`) || !strings.Contains(string(raw), "pushlandı") {
		t.Fatal("completed button remained active")
	}
	clickRequest(t, h, p, true)
	awaitButtonResult(t, client)
	if git.calls.Load() != 1 {
		t.Fatal("duplicate click pushed again")
	}
}

func TestPushButtonRejectsForgedExpiredForeignAndStaleClicks(t *testing.T) {
	for _, scenario := range []string{"unsigned", "other user", "other team", "other channel", "other thread", "expired", "review token", "tampered digest", "stale version", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			h, codingHandler, store, git, client, p := buttonFixture(t)
			var value codingPushValue
			_ = json.Unmarshal([]byte(p.Actions[0].Value), &value)
			async := false
			switch scenario {
			case "other user":
				p.User.ID = "OTHER"
			case "other team":
				p.Team.ID = "OTHER"
			case "other channel":
				p.Channel.ID = "OTHER"
				p.Container.ChannelID = "OTHER"
				async = true
			case "other thread":
				p.Message.ThreadTS = "999.0001"
				async = true
			case "expired":
				value.Token, _ = codingHandler.links.Token(1, pushPurpose(value.TaskID, value.Digest), time.Now().Add(-time.Minute))
			case "review token":
				value.Token, _ = codingHandler.links.Token(1, reviewPurpose(value.TaskID, value.Digest), time.Now().Add(time.Hour))
			case "tampered digest":
				value.Digest = strings.Repeat("f", 64)
			case "stale version":
				store.e.Digest = strings.Repeat("f", 64)
				async = true
			case "cancelled":
				store.e.Task.Status = "cancelled"
				async = true
			}
			raw, _ := json.Marshal(value)
			p.Actions[0].Value = string(raw)
			w := clickRequest(t, h, p, scenario != "unsigned")
			if scenario == "unsigned" && w.Code != 401 {
				t.Fatal("unsigned click accepted")
			}
			if async {
				if text := awaitButtonResult(t, client); strings.Contains(text, "GitHub’a pushlandı") {
					t.Fatal("denied click succeeded")
				}
			} else if strings.Contains(w.Body.String(), "isteğini aldım") {
				t.Fatal("invalid click queued")
			}
			if git.calls.Load() != 0 {
				t.Fatal("denied click reached git")
			}
		})
	}
}

func TestPushButtonsAttachToOriginalReplyAndStayOutOfModelText(t *testing.T) {
	_, h, store, _, client, _ := buttonFixture(t)
	info := coding.EditInfo{Task: store.e.Task, Digest: store.e.Digest}
	reply := &codingReply{h: h, identity: store.e.Task.Identity, reviews: map[string]coding.EditInfo{info.Task.ID: info}}
	events := &SlackEventHandler{client: client}
	if err := events.postReply(context.Background(), "bot", "C", "100.0001", "Actual model reply", reply); err != nil {
		t.Fatal(err)
	}
	blocks := <-client.updates
	if blocks[0]["text"].(map[string]string)["text"] != "Actual model reply" {
		t.Fatal("model reply was replaced")
	}
	last := blocks[len(blocks)-1]
	if last["type"] != "actions" || last["elements"].([]map[string]any)[0]["text"].(map[string]string)["text"] != "Pushla" {
		t.Fatal("button not below reply")
	}
}

func TestAwaitingTaskCanResendButtonWithoutExposingApprovalMaterial(t *testing.T) {
	_, h, store, _, client, _ := buttonFixture(t)
	tools, reply := h.ToolsForReply(service.NewAgentTools(nil, nil, nil, 1, time.Now(), time.UTC), store.e.Task.Identity)
	raw, _ := json.Marshal(map[string]string{"task_id": store.e.Task.ID})
	result, err := tools.Call(context.Background(), "repository_task_status", raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{store.e.Digest, "approval_command", "token=", "pushla "} {
		if strings.Contains(result, secret) {
			t.Fatal("approval material exposed to model", secret)
		}
	}
	if _, err := tools.Call(context.Background(), "repository_push", raw); err == nil {
		t.Fatal("model can invoke push tool")
	}
	select {
	case message := <-client.sent:
		if strings.Contains(message, "```pushla") {
			t.Fatal("DM still asks for command")
		}
	default:
		t.Fatal("review DM not sent")
	}
	blocks, err := reply.blocks("Review ready")
	if err != nil || len(blocks) < 2 {
		t.Fatal("existing task did not get replacement button", err)
	}
}
