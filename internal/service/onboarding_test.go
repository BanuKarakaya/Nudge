package service

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"nudge/internal/domain"
	"nudge/internal/repository"
	"nudge/internal/secure"
	"strings"
	"testing"
	"time"
)

type onboardingStore struct {
	repository.InstallationRepository
	users map[string]int64
}

func (s *onboardingStore) GetByUserID(_ context.Context, id int64) (domain.SlackInstallation, error) {
	return domain.SlackInstallation{TeamID: "T", BotToken: "bot", SlackUserID: fmt.Sprint("U", id)}, nil
}
func (s *onboardingStore) UpsertUser(_ context.Context, team, user string) (int64, error) {
	if team != "T" {
		return 0, errors.New("wrong team")
	}
	if s.users == nil {
		s.users = map[string]int64{}
	}
	s.users[user] = 2
	return 2, nil
}

type onboardingQueue struct {
	repository.NotificationRepository
	messages map[string]repository.NotificationMessage
}

func (q *onboardingQueue) Enqueue(_ context.Context, id int64, kind, key string, m repository.NotificationMessage) error {
	if q.messages == nil {
		q.messages = map[string]repository.NotificationMessage{}
	}
	k := fmt.Sprint(id, "/", kind, "/", key)
	if _, ok := q.messages[k]; !ok {
		q.messages[k] = m
	}
	return nil
}

type onboardingSlack struct {
	recipients, messages []string
	failList             bool
}

func (s *onboardingSlack) OpenDirectMessage(_ context.Context, token, user string) (string, error) {
	if token != "bot" {
		return "", errors.New("wrong token")
	}
	s.recipients = append(s.recipients, user)
	return "D" + user, nil
}
func (s *onboardingSlack) PostMessage(_ context.Context, _, channel, text string) error {
	if !strings.HasPrefix(channel, "DU") {
		return errors.New("not private")
	}
	s.messages = append(s.messages, text)
	return nil
}
func (s *onboardingSlack) ListUsers(context.Context, string) ([]domain.SlackMember, error) {
	if s.failList {
		return nil, errors.New("Slack unavailable")
	}
	return []domain.SlackMember{{ID: "U2"}, {ID: "B", IsBot: true}, {ID: "X", Deleted: true}, {ID: "USLACKBOT"}}, nil
}
func onboardingFixture(t *testing.T) (*OnboardingService, *onboardingStore, *onboardingQueue, *onboardingSlack, secure.LinkSigner) {
	t.Helper()
	links, err := secure.NewLinkSigner(base64.StdEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	store := &onboardingStore{}
	q := &onboardingQueue{}
	client := &onboardingSlack{}
	return NewOnboardingService(store, client, "https://nudgy.example", links).WithQueue(q), store, q, client, links
}
func TestOnboardingDiscoversHumansAndDeduplicatesSeparateInvitations(t *testing.T) {
	s, store, q, client, _ := onboardingFixture(t)
	for _, purpose := range []string{"feedbin", "raindrop", "github"} {
		s.Register(ConnectionInvitation{Purpose: purpose, Name: purpose, Connected: func(context.Context, int64) (bool, error) { return false, nil }})
	}
	s.Register(ConnectionInvitation{Purpose: "disabled", Enabled: func(string) bool { return false }})
	ctx := context.Background()
	for n := 0; n < 2; n++ {
		if err := s.DiscoverWorkspace(ctx, 1); err != nil {
			t.Fatal(err)
		}
	}
	if len(store.users) != 1 || store.users["U2"] != 2 || len(q.messages) != 6 {
		t.Fatalf("wrong recipients or duplicate invitations: users=%v queue=%d", store.users, len(q.messages))
	}
	for _, m := range q.messages {
		if m.Connection == "" || m.Text != "" || m.Channel != "" {
			t.Fatal("must queue each service without a bearer link")
		}
	}
	if len(client.messages) != 0 {
		t.Fatal("discovery bypassed durable delivery")
	}
	if err := s.EnqueueWorkspace(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.EnqueueWorkspace(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if len(q.messages) != 7 {
		t.Fatal("workspace enqueue did not deduplicate")
	}
	client.failList = true
	if err := s.DiscoverWorkspace(ctx, 1); err == nil {
		t.Fatal("discovery failure swallowed")
	}
	if len(q.messages) != 7 {
		t.Fatal("retry duplicated installer invitations")
	}
}
func TestInvitationSkipsConnectedAndUsesFreshUserBoundLink(t *testing.T) {
	s, _, q, client, links := onboardingFixture(t)
	connected := false
	dbFail := false
	s.Register(ConnectionInvitation{Purpose: "github", Name: "GitHub", Connected: func(context.Context, int64) (bool, error) {
		if dbFail {
			return false, errors.New("DB unavailable")
		}
		return connected, nil
	}})
	ctx := context.Background()
	if err := s.Send(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if len(q.messages) != 1 {
		t.Fatal("missing invitation")
	}
	connected = true
	if err := s.DeliverInvitation(ctx, 1, "github"); err != nil {
		t.Fatal(err)
	}
	if len(client.messages) != 0 {
		t.Fatal("already connected user invited")
	}
	connected = false
	dbFail = true
	if err := s.DeliverInvitation(ctx, 1, "github"); err == nil {
		t.Fatal("DB error treated as missing connection")
	}
	if len(client.messages) != 0 {
		t.Fatal("sent without connection check")
	}
	dbFail = false
	if err := s.DeliverInvitation(ctx, 1, "github"); err != nil {
		t.Fatal(err)
	}
	if len(client.messages) != 1 || client.recipients[0] != "U1" {
		t.Fatal("wrong recipient")
	}
	raw := strings.Split(strings.Split(client.messages[0], "<")[1], "|")[0]
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	token := u.Query().Get("token")
	if u.Path != "/github/install" || u.Query().Get("user_id") != "1" || !links.Verify(token, 1, "github", time.Now().Add(19*time.Minute)) || links.Verify(token, 2, "github", time.Now()) || links.Verify(token, 1, "github", time.Now().Add(21*time.Minute)) {
		t.Fatal("wrong link binding or lifetime")
	}
}
