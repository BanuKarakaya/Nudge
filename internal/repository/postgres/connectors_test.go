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
	"nudge/internal/connector"
	"nudge/internal/domain"
	"nudge/internal/secure"
	"nudge/internal/workspace"
)

func TestConnectorUninstallRemovesOnlyOwnCredentials(t *testing.T) {
	db := reviewDB(t, 3)
	ctx := context.Background()
	installs := NewInstallationRepository(db)
	owner, err := installs.Save(ctx, domain.SlackInstallation{TeamID: "MCP-A", SlackUserID: "OWNER", BotToken: "synthetic-a"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := installs.Save(ctx, domain.SlackInstallation{TeamID: "MCP-B", SlackUserID: "OWNER", BotToken: "synthetic-b"})
	if err != nil {
		t.Fatal(err)
	}
	s := NewConnectorStore(db, reviewCipher(t))
	for _, item := range []struct {
		user int64
		team string
	}{{owner, "MCP-A"}, {other, "MCP-B"}} {
		c := connector.Connection{ID: connector.NewID(), Revision: connector.NewID(), Team: item.team, Name: "Demo", Endpoint: "https://company.test/mcp"}
		if err = s.Save(ctx, item.user, c, "synthetic-credential", ""); err != nil {
			t.Fatal(err)
		}
	}
	if err = installs.Deactivate(ctx, "MCP-A", 0); err != nil {
		t.Fatal(err)
	}
	var n int
	if err = db.QueryRow(ctx, `SELECT count(*) FROM workspace_mcp_connections WHERE team_id='MCP-A'`).Scan(&n); err != nil || n != 0 {
		t.Fatal("uninstall retained credentials", n, err)
	}
	if list, err := s.List(ctx, other); err != nil || len(list) != 1 {
		t.Fatal("other workspace affected", err)
	}
}

func testConnectorStore(t *testing.T, db *pgxpool.Pool, owner, member, other int64) {
	t.Helper()
	ctx := context.Background()
	cipher, err := secure.NewCipherFromBase64(base64.StdEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	s := NewConnectorStore(db, cipher)
	c := connector.Connection{ID: connector.NewID(), Team: "legacy", Name: "Internal tickets", Endpoint: "https://company.test/mcp", Revision: connector.NewID(), Enabled: true, Tools: []connector.Tool{{Name: "ticket.read", Schema: map[string]any{"type": "object"}, Fingerprint: "v1", Selected: true}}}
	if err = s.Save(ctx, member, c, "test-credential", ""); !errors.Is(err, workspace.ErrDenied) {
		t.Fatal("member wrote connector", err)
	}
	if err = s.Save(ctx, other, c, "test-credential", ""); !errors.Is(err, workspace.ErrDenied) {
		t.Fatal("other team wrote connector", err)
	}
	if err = s.Save(ctx, owner, c, "test-credential", ""); err != nil {
		t.Fatal(err)
	}
	if list, err := s.List(ctx, member); err != nil || len(list) != 1 || list[0].Team != "legacy" {
		t.Fatal("own workspace connection unavailable", list, err)
	}
	if list, err := s.List(ctx, other); err != nil || len(list) != 0 {
		t.Fatal("cross-tenant catalog", list, err)
	}
	if token, err := s.Secret(ctx, member, c.ID, c.Revision); err != nil || token != "test-credential" {
		t.Fatal("credential unavailable", err)
	}
	if _, err = s.Secret(ctx, other, c.ID, c.Revision); err == nil {
		t.Fatal("cross-tenant credential")
	}
	var encrypted string
	if err = db.QueryRow(ctx, `SELECT credentials FROM workspace_mcp_connections WHERE team_id=$1 AND id=$2`, c.Team, c.ID).Scan(&encrypted); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(encrypted, "v2:") || strings.Contains(encrypted, "test-credential") {
		t.Fatal("plaintext credential")
	}
	clone := c
	clone.Team = "second"
	if err = s.Save(ctx, other, clone, "other-test-credential", ""); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `UPDATE workspace_mcp_connections SET credentials=$1 WHERE team_id='second'`, encrypted); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Secret(ctx, other, c.ID, c.Revision); err == nil {
		t.Fatal("cross-team ciphertext transplant")
	}
	if err = s.Delete(ctx, member, c.ID, c.Revision); !errors.Is(err, workspace.ErrDenied) {
		t.Fatal("member deleted", err)
	}
	if err = s.Delete(ctx, other, clone.ID, clone.Revision); err != nil {
		t.Fatal(err)
	}
	var wins atomic.Int32
	var wg sync.WaitGroup
	for n := 0; n < 2; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			next := c
			next.Revision = connector.NewID()
			err := s.Save(ctx, owner, next, "test-credential", c.Revision)
			if err == nil {
				wins.Add(1)
			} else if !errors.Is(err, connector.ErrConflict) {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatal("concurrent update did not conflict", wins.Load())
	}
	if _, err = s.Secret(ctx, owner, c.ID, c.Revision); err == nil {
		t.Fatal("stale revision still authorized")
	}
	current, err := s.List(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Delete(ctx, owner, c.ID, c.Revision); !errors.Is(err, connector.ErrConflict) {
		t.Fatal("stale deletion accepted", err)
	}
	if err = s.Delete(ctx, owner, c.ID, current[0].Revision); err != nil {
		t.Fatal(err)
	}
	if err = s.Save(ctx, owner, c, "test-credential", current[0].Revision); !errors.Is(err, connector.ErrConflict) {
		t.Fatal("stale save resurrected deleted connection", err)
	}
	if list, err := s.List(ctx, owner); err != nil || len(list) != 0 {
		t.Fatal("deleted connection remains", err)
	}
	var auditCount int
	if err = db.QueryRow(ctx, `SELECT count(*) FROM nudge_audit_log WHERE team_id='legacy' AND actor_id=$1 AND action IN ('mcp.saved','mcp.deleted')`, owner).Scan(&auditCount); err != nil || auditCount != 3 {
		t.Fatal("connector changes not audited", auditCount, err)
	}
}
