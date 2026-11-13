package codex

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNativeStateRetentionAndDeletionAreUserScoped(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspaces", strings.Repeat("a", 64))
	first := filepath.Join(workspace, "users", nativeUserDir(1), strings.Repeat("b", 64))
	second := filepath.Join(workspace, "users", nativeUserDir(2), strings.Repeat("c", 64))
	for _, p := range []string{first, second} {
		if err := os.MkdirAll(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	runtime := &TenantRuntime{base: Config{StateDir: root}}
	if err := runtime.DeleteUserState(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(first); !os.IsNotExist(err) {
		t.Fatal("user state retained")
	}
	if _, err := os.Stat(second); err != nil {
		t.Fatal("other user lost", err)
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(second, old, old); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CleanupState(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(second); !os.IsNotExist(err) {
		t.Fatal("expired state retained")
	}
}
