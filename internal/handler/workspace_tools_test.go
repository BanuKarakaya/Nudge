package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"nudge/internal/repository"
	"nudge/internal/service"
	"strings"
	"testing"
	"time"
)

func TestNaturalDashboardRequestSendsPrivateLinkInExistingThread(t *testing.T) {
	for _, mode := range []string{"success", "open", "post", "wrong workspace", "lease"} {
		t.Run(mode, func(t *testing.T) {
			portal, _, _ := workspaceFixture(t)
			dm := &failedConnectionDM{stage: mode}
			portal.installations = codingInstallations{}
			portal.client = dm
			q := &testJobQueue{}
			event := conversationEvent("<@B123> dashboard linki verebilir misin")
			if mode == "wrong workspace" {
				event.TeamID = "OTHER"
			}
			queuedEvent(t, q, event)
			history := make(conversationTestStore)
			key := service.ConversationKey{Team: event.TeamID, Channel: event.Event.Channel, Thread: event.Event.ThreadTS, User: event.Event.User}
			if key.Thread == "" {
				key.Thread = event.Event.TS
			}
			state := deliveredState()
			state.AgentThreadID = "old-without-dashboard-tool"
			state.AgentToolVersion = ""
			if err := history.Save(context.Background(), key, state); err != nil {
				t.Fatal(err)
			}
			sender := &conversationSender{}
			h := NewSlackEventHandler("", codingInstallations{}, nil, nil, agentFunc(func(ctx context.Context, r service.AgentRequest) (service.AgentResult, error) {
				if r.ThreadID != "" || len(r.History.Turns) != 1 || r.History.AgentToolVersion != "+workspace-panel-v1" {
					t.Fatal("existing thread did not refresh tools while retaining delivered history", r.History)
				}
				found := false
				for _, d := range r.Tools.Definitions() {
					if d.Name == "workspace_connect" {
						found = true
					}
				}
				if !found {
					t.Fatal("natural dashboard request has no general panel tool")
				}
				if mode == "lease" {
					q.leaseErr = repository.ErrSlackJobLeaseLost
				}
				out, err := r.Tools.Call(ctx, "workspace_connect", json.RawMessage(`{}`))
				if err != nil {
					return service.AgentResult{}, err
				}
				if strings.Contains(out, "token=") || !strings.Contains(out, "connection_link_sent_privately") {
					t.Fatal("model received capability or incorrect result", out)
				}
				return service.AgentResult{Text: "Panel bağlantını DM’den gönderdim."}, nil
			}), nil, history, sender, nil).WithJobQueue(q).WithWorkspacePortal(portal)
			err := h.ProcessJob(context.Background(), q.job)
			if mode == "lease" {
				if !errors.Is(err, repository.ErrSlackJobLeaseLost) || len(dm.sent) != 0 {
					t.Fatal("lost lease sent link", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			success := mode == "success"
			if q.success != success || !q.completed || len(sender.messages) != 1 {
				t.Fatal("wrong outcome", q.success, q.completed, sender.messages)
			}
			if !success {
				if len(dm.sent) != 0 || strings.Contains(sender.messages[0], "gönderdim") {
					t.Fatal("false DM success")
				}
				return
			}
			if len(dm.sent) != 1 || strings.Contains(sender.messages[0], "token=") {
				t.Fatal("private delivery leak")
			}
			raw := strings.Split(strings.Split(dm.sent[0], "<")[1], "|")[0]
			link, e := url.Parse(raw)
			if e != nil || link.Path != "/dashboard/install" || !portal.links.Verify(link.Query().Get("token"), 1, "workspace", time.Now()) {
				t.Fatal("wrong user or purpose", e, link.Path)
			}
			if err = h.ProcessJob(context.Background(), q.job); err != nil || len(dm.sent) != 1 {
				t.Fatal("checkpoint replay duplicated DM", err)
			}
		})
	}
}
