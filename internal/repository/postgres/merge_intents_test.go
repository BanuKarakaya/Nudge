package postgres

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"nudge/internal/coding"
)

// Called by the existing isolated-schema PostgreSQL coding-store test.
func testMergeIntents(t *testing.T, db *pgxpool.Pool, s *CodingStore) {
	t.Helper()
	ctx := context.Background()
	i := coding.Identity{UserID: 1, Team: "T", Channel: "C", Thread: "1", Event: "merge-E"}
	pin := coding.MergePin{AccountID: 42, RepositoryID: 10, Number: 12, HeadSHA: strings.Repeat("a", 40), TargetBranch: "main"}
	first, err := s.PinMerge(ctx, i, pin)
	if err != nil || first != pin {
		t.Fatal("initial pin", first, err)
	}
	changed := pin
	changed.HeadSHA = strings.Repeat("b", 40)
	restarted := &CodingStore{db: db, cipher: s.cipher}
	var wg sync.WaitGroup
	for n := 0; n < 8; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := restarted.PinMerge(ctx, i, changed)
			if err != nil || got != pin {
				t.Error("replay replaced revision", got, err)
			}
		}()
	}
	wg.Wait()
	other := i
	other.Team = "other"
	if _, err := s.PinMerge(ctx, other, pin); err == nil {
		t.Fatal("cross-workspace pin")
	}
	other = i
	other.Thread = "other"
	if _, err := s.PinMerge(ctx, other, pin); err == nil {
		t.Fatal("same event changed thread")
	}
	other = i
	other.Event = "fresh-merge-E"
	if got, err := s.PinMerge(ctx, other, changed); err != nil || got != changed {
		t.Fatal("fresh request could not pin", err)
	}
	gitlab := &CodingStore{db: db, cipher: s.cipher, provider: "gitlab"}
	if got, err := gitlab.PinMerge(ctx, i, changed); err != nil || got != changed {
		t.Fatal("provider pin collision", err)
	}
	branchIdentity := i
	branchIdentity.Event = "branch-merge-E"
	branchPin := pin
	branchPin.Mode, branchPin.SourceBranch, branchPin.TargetBranch = "branches", "feature/existing", "development"
	if _, exists, err := restarted.GetMergePin(ctx, branchIdentity); err != nil || exists {
		t.Fatal("unexpected existing branch pin", err)
	}
	if _, err := s.PinMerge(ctx, branchIdentity, branchPin); err != nil {
		t.Fatal(err)
	}
	if got, exists, err := restarted.GetMergePin(ctx, branchIdentity); err != nil || !exists || got != branchPin {
		t.Fatal("restart lost branch pin", got, err)
	}
	if _, exists, err := gitlab.GetMergePin(ctx, branchIdentity); err != nil || exists {
		t.Fatal("reader crossed providers", err)
	}
	branchIdentity.Thread = "other"
	if _, _, err := restarted.GetMergePin(ctx, branchIdentity); err == nil {
		t.Fatal("reader crossed conversation")
	}
}
