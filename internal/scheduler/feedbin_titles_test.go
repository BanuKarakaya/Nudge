package scheduler

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"nudge/internal/domain"
	"nudge/internal/feedbin"
	"nudge/internal/repository"
	"nudge/internal/service"
)

type titleInstallations struct {
	repository.InstallationRepository
}

func (titleInstallations) GetByUserID(context.Context, int64) (domain.SlackInstallation, error) {
	return domain.SlackInstallation{TeamID: "T", BotToken: "test"}, nil
}
func (titleInstallations) UpsertUser(_ context.Context, team, user string) (int64, error) {
	if team != "T" {
		return 0, errors.New("wrong workspace")
	}
	if user == "U07NH5V88TC" {
		return 2, nil
	}
	return 1, nil
}

type titleConnections struct {
	repository.FeedbinConnectionRepository
}

func (titleConnections) GetByUserID(_ context.Context, id int64) (domain.FeedbinConnection, error) {
	if id == 2 {
		return domain.FeedbinConnection{Email: "latif", Password: "latif-test"}, nil
	}
	return domain.FeedbinConnection{Email: "banu", Password: "banu-test"}, nil
}

type titleSlack struct{}

func (titleSlack) ListUsers(context.Context, string) ([]domain.SlackMember, error) {
	return []domain.SlackMember{{ID: "U07NW2BGCSD"}, {ID: "U07NH5V88TC"}}, nil
}
func (titleSlack) OpenDirectMessage(_ context.Context, _, user string) (string, error) {
	return "D-" + user, nil
}

type titleSource struct {
	since    time.Time
	failBanu bool
}

func (s titleSource) ListUnreadSince(_ context.Context, email, password string, since time.Time) ([]feedbin.Entry, error) {
	if !since.Equal(s.since) || password != email+"-test" {
		return nil, errors.New("wrong bound account/window")
	}
	if s.failBanu && email == "banu" {
		return nil, errors.New("temporary provider failure")
	}
	return []feedbin.Entry{{ID: 1, Title: email + " RSS", URL: "https://example.com/" + email, CreatedAt: since}}, nil
}
func (titleSource) ListTaggings(context.Context, string, string) ([]feedbin.Tagging, error) {
	panic("scheduled titles must not need categories")
}

func TestScheduledTitlesReachBothWorkspaceMembersAt2300(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Istanbul")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 27, 23, 0, 0, 0, loc)
	for _, failBanu := range []bool{false, true} {
		q := &memoryOutbox{}
		generator := service.NewFeedbinTitles(service.NewFeedbinDigestService(titleConnections{}, titleSource{since: now.AddDate(0, 0, -1), failBanu: failBanu}))
		r := NewFeedbinReminder(1, loc, titleInstallations{}, titleConnections{}, generator, q, titleSlack{})
		if got := r.nextEvent(now.Add(-time.Second)); !got.Equal(now) {
			t.Fatalf("scheduled %s, want %s", got, now)
		}
		if got := r.nextEvent(now); !got.Equal(now.AddDate(0, 0, 1)) {
			t.Fatalf("next day scheduled %s", got)
		}
		if err := r.RunOnce(context.Background(), now); (err != nil) != failBanu {
			t.Fatalf("run: %v", err)
		}
		want := 2
		if failBanu {
			want = 1
		}
		if len(q.pending) != want {
			t.Fatalf("queued %d, want %d", len(q.pending), want)
		}
		foundLatif := false
		for _, d := range q.pending {
			if d.UserID == 2 {
				foundLatif = true
				if d.Message.Channel != "D-U07NH5V88TC" || !strings.Contains(d.Message.Text, "<https://example.com/latif|latif RSS>") || strings.Contains(d.Message.Text, "banu RSS") {
					t.Fatalf("wrong recipient/content: %+v", d)
				}
			}
		}
		if !foundLatif {
			t.Fatal("Latif skipped")
		}
		_ = r.RunOnce(context.Background(), now.Add(70*time.Minute))
		if len(q.pending) != want {
			t.Fatal("retry duplicated titles")
		}
	}
}
