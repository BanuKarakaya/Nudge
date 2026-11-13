package postgres

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"nudge/internal/secure"
	"nudge/internal/workspace"
)

func testWorkspaceAIStore(t *testing.T, db *pgxpool.Pool, members *WorkspaceStore, owner, member, other int64) {
	t.Helper()
	ctx := context.Background()
	cipher, e := secure.NewCipherFromBase64(base64.StdEncoding.EncodeToString(make([]byte, 32)))
	if e != nil {
		t.Fatal(e)
	}
	s, e := NewWorkspaceAIStore(ctx, db, cipher)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = NewWorkspaceAIStore(ctx, db, cipher); e != nil {
		t.Fatal("AI migration not idempotent", e)
	}
	before, e := s.AISettings(ctx, owner)
	if e != nil || before.HasKey || before.Ready() {
		t.Fatal("new tenant enabled", e)
	}
	c := workspace.AICredentials{AISettings: workspace.AISettings{Provider: "openrouter", Model: "vendor/daily", ComplexModel: "vendor/advanced", SearchModel: "gemini-test"}, APIKey: "private-model-key", SearchAPIKey: "private-google-key"}
	if e = s.SaveAI(ctx, member, c, ""); !errors.Is(e, workspace.ErrDenied) {
		t.Fatal("member saved AI", e)
	}
	if e = s.SaveAI(ctx, owner, c, ""); e != nil {
		t.Fatal(e)
	}
	got, e := s.AICredentials(ctx, member)
	if e != nil || got.APIKey != c.APIKey || got.SearchAPIKey != c.SearchAPIKey || !got.Ready() {
		t.Fatal("own workspace credentials missing", e)
	}
	if otherSettings, e := s.AISettings(ctx, other); e != nil || otherSettings.HasKey {
		t.Fatal("other workspace inherited key", e)
	}
	var encrypted string
	if e = db.QueryRow(ctx, `SELECT credentials FROM workspace_ai_settings WHERE updated_by=$1`, owner).Scan(&encrypted); e != nil {
		t.Fatal(e)
	}
	if !strings.HasPrefix(encrypted, "v2:") || strings.Contains(encrypted, c.APIKey) || strings.Contains(encrypted, c.SearchAPIKey) {
		t.Fatal("plaintext secrets stored")
	}
	// A forged ciphertext transplant cannot make the other team spend this key.
	if e = s.SaveAI(ctx, other, c, ""); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(ctx, `UPDATE workspace_ai_settings SET credentials=$1 WHERE updated_by=$2`, encrypted, other); e != nil {
		t.Fatal(e)
	}
	if _, e = s.AICredentials(ctx, other); !errors.Is(e, workspace.ErrDenied) {
		t.Fatal("ciphertext transplant accepted", e)
	}
	var successes atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := s.SaveAI(ctx, owner, c, got.Revision)
			if err == nil {
				successes.Add(1)
			} else if !errors.Is(err, workspace.ErrAIConflict) {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if successes.Load() != 1 {
		t.Fatal("concurrent settings overwrite", successes.Load())
	}
	current, e := s.AISettings(ctx, owner)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.SaveAI(ctx, owner, workspace.AICredentials{}, current.Revision); e != nil {
		t.Fatal(e)
	}
	removed, e := s.AICredentials(ctx, member)
	if e != nil || removed.APIKey != "" || removed.SearchAPIKey != "" || removed.Ready() {
		t.Fatal("removal retained key", e)
	}
	if e = s.SaveAI(ctx, owner, c, current.Revision); !errors.Is(e, workspace.ErrAIConflict) {
		t.Fatal("stale form resurrected removed key", e)
	}
	roster, e := members.Members(ctx, owner)
	if e != nil || len(roster) != 3 {
		t.Fatal("roster mismatch", len(roster), e)
	}
	for _, m := range roster {
		if m.UserID == other {
			t.Fatal("roster crosses tenant")
		}
	}
	if _, e = members.Members(ctx, member); !errors.Is(e, workspace.ErrDenied) {
		t.Fatal("member read team roster")
	}
}
