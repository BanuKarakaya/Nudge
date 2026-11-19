package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Execute the real entrypoint in an isolated process: main uses fatal startup
// errors and signals, which must never terminate the parent test process.
func TestServerProcess(t *testing.T) {
	if os.Getenv("NUDGY_TEST_SERVER_PROCESS") != "1" {
		return
	}
	main()
}

func TestServerOccupiedPortExitsWithFailure(t *testing.T) {
	dsn := os.Getenv("NUDGE_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set NUDGE_TEST_DATABASE_URL to a disposable PostgreSQL database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	schema := fmt.Sprintf("server_test_%d", time.Now().UnixNano())
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err := db.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := db.Exec(context.Background(), "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Errorf("clean server schema: %v", err)
		}
	}()
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		u, err := url.Parse(dsn)
		if err != nil {
			t.Fatal(err)
		}
		q := u.Query()
		q.Set("search_path", schema)
		u.RawQuery = q.Encode()
		dsn = u.String()
	} else {
		dsn += " search_path=" + schema
	}
	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	key := base64.StdEncoding.EncodeToString(make([]byte, 32))
	cmd := exec.CommandContext(ctx, binary, "-test.run=^TestServerProcess$")
	// No inherited Slack/provider credentials, local .env or production state.
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home,
		"NUDGY_TEST_SERVER_PROCESS=1", "DATABASE_URL=" + dsn, "PORT=" + port,
		"NUDGE_PUBLIC_URL=http://localhost:" + port, "NUDGE_REMINDER_USER_ID=0",
		"NUDGE_LINK_SIGNING_KEY=" + key, "FEEDBIN_ENCRYPTION_KEY=" + key,
		"NUDGY_CODEX_STATE_DIR=" + filepath.Join(home, "state")}
	output, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("server did not exit after bind failure: %v\n%s", ctx.Err(), output)
	}
	if !strings.Contains(string(output), "bind: address already in use") {
		t.Fatalf("server failed before reaching the bind check: %s", output)
	}
	if err == nil {
		t.Fatal("server reported successful exit after failing to listen")
	}
}
