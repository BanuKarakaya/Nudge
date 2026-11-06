package handler

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"nudge/internal/coding"
	"nudge/internal/service"
)

func TestConnectionRequestRequiresStandaloneIntent(t *testing.T) {
	for _, text := range []string{"gitlab hesabımı bağlamak istiyorum", "GitLab hesabımı bağla", "Lütfen GitHub hesabımı yeniden bağla!", "reconnect gitlab"} {
		if connectionProvider(text) == "" {
			t.Errorf("missed explicit request: %q", text)
		}
	}
	for _, text := range []string{
		"GitLab hesabımı bağlama", "GitLab hesabımı bağlamak istemiyorum",
		"GitLab hesabımı nasıl bağlarım?", `"gitlab hesabımı bağla"`,
		"gitlab hesabımı bağla ve projeyi sil", "gitlab hesabımı bağlamak istiyorum diyen birine ne demeliyim",
		"github ve gitlab hesabımı bağla", "GitLab bağlantısı var mı?",
	} {
		if connectionProvider(text) != "" {
			t.Errorf("treated data/ambiguous request as consent: %q", text)
		}
	}
}

type failedConnectionDM struct {
	codingDM
	stage string
}

func (d *failedConnectionDM) OpenDirectMessage(ctx context.Context, token, user string) (string, error) {
	if d.stage == "open" {
		return "", errors.New("private transport detail")
	}
	return d.codingDM.OpenDirectMessage(ctx, token, user)
}

func (d *failedConnectionDM) PostMessage(ctx context.Context, token, channel, text string) error {
	if d.stage == "post" {
		return errors.New("private transport detail")
	}
	return d.codingDM.PostMessage(ctx, token, channel, text)
}

func TestQueuedConnectionSendsWithoutModelAndChecksDelivery(t *testing.T) {
	for _, provider := range []string{"gitlab", "github"} {
		for _, mode := range []string{"connected", "open", "post", "disabled", "wrong workspace"} {
			t.Run(provider+"/"+mode, func(t *testing.T) {
				ch, store, _, _ := codingHandlerFixture(t)
				if provider == "gitlab" {
					ch, store = glHandlerFixture(t)
				}
				store.saved = coding.Connection{UserID: 1, Login: "existing-account"}
				dm := &failedConnectionDM{stage: mode}
				ch.client = dm
				q := &testJobQueue{}
				event := conversationEvent("<@B123> " + provider + " hesabımı bağlamak istiyorum")
				if mode == "wrong workspace" {
					event.TeamID = "OTHER"
				}
				queuedEvent(t, q, event)
				sender := &conversationSender{}
				h := NewSlackEventHandler("", codingInstallations{}, nil, nil,
					agentFunc(func(context.Context, service.AgentRequest) (service.AgentResult, error) {
						t.Fatal("connection request must not depend on model tool selection")
						return service.AgentResult{}, nil
					}), nil, make(conversationTestStore), sender, nil).WithJobQueue(q)
				if mode != "disabled" {
					if provider == "gitlab" {
						h.gitlab = ch
					} else {
						h.coding = ch
					}
				}
				if err := h.ProcessJob(context.Background(), q.job); err != nil {
					t.Fatal(err)
				}
				wantSuccess := mode == "connected"
				if !q.completed || q.success != wantSuccess || len(sender.messages) != 1 {
					t.Fatalf("completion=%t success=%t replies=%d", q.completed, q.success, len(sender.messages))
				}
				message := sender.messages[0]
				if strings.Contains(message, "token=") || strings.Contains(message, "private transport") || strings.Contains(message, "gönderdim") != wantSuccess {
					t.Fatalf("unsafe or false delivery claim: %s", message)
				}
				if wantSuccess {
					if len(dm.sent) != 1 {
						t.Fatal("no new DM for already connected user")
					}
					u, err := url.Parse(strings.Split(dm.sent[0], "\n")[1])
					if err != nil || u.Path != "/"+provider+"/install" || !ch.links.Verify(u.Query().Get("token"), 1, provider, time.Now()) {
						t.Fatal("wrong provider or user capability")
					}
					// Replaying a checkpointed response must not invoke delivery again.
					if err := h.ProcessJob(context.Background(), q.job); err != nil || len(dm.sent) != 1 {
						t.Fatal("checkpoint replay resent link", err)
					}
				} else if len(dm.sent) != 0 {
					t.Fatal("unexpected private message")
				}
				if store.saved.Login != "existing-account" {
					t.Fatal("sending link altered existing connection")
				}
			})
		}
	}
}
