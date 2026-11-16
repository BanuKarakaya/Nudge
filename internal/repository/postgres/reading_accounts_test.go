package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"nudge/internal/domain"
)

func TestReadingAccountDisconnectIsOwnerScoped(t *testing.T) {
	db := reviewDB(t, 2)
	ctx := context.Background()
	cipher := reviewCipher(t)
	installs := NewInstallationRepository(db, cipher)
	owner, err := installs.Save(ctx, domain.SlackInstallation{TeamID: "T", SlackUserID: "OWNER", BotToken: "synthetic"})
	if err != nil {
		t.Fatal(err)
	}
	member, err := installs.UpsertUser(ctx, "T", "MEMBER")
	if err != nil {
		t.Fatal(err)
	}
	feed := NewFeedbinConnectionRepository(db, cipher)
	rain := NewRaindropConnectionRepository(db, cipher)
	for _, user := range []int64{owner, member} {
		if err = feed.Save(ctx, domain.FeedbinConnection{UserID: user, Email: "synthetic@example.test", Password: "synthetic"}); err != nil {
			t.Fatal(err)
		}
		if err = rain.Save(ctx, domain.RaindropConnection{UserID: user, AccessToken: "synthetic", RefreshToken: "synthetic"}); err != nil {
			t.Fatal(err)
		}
	}
	for n := 0; n < 2; n++ {
		if err = feed.Disconnect(ctx, member); err != nil {
			t.Fatal(err)
		}
		if err = rain.Disconnect(ctx, member); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = feed.GetByUserID(ctx, member); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("Feedbin credential remained", err)
	}
	if _, err = rain.GetByUserID(ctx, member); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("Raindrop credential remained", err)
	}
	if _, err = feed.GetByUserID(ctx, owner); err != nil {
		t.Fatal("other Feedbin account changed", err)
	}
	if _, err = rain.GetByUserID(ctx, owner); err != nil {
		t.Fatal("other Raindrop account changed", err)
	}
}
