package codex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

func nativeUserDir(user int64) string {
	sum := sha256.Sum256([]byte("nudgy-user:" + strconv.FormatInt(user, 10)))
	return hex.EncodeToString(sum[:])
}
func stateHash(name string) bool {
	raw, err := hex.DecodeString(name)
	return err == nil && len(raw) == 32
}
func workspaceStateDirs(root string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(root, "workspaces"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() && stateHash(e.Name()) {
			dirs = append(dirs, filepath.Join(root, "workspaces", e.Name()))
		}
	}
	return dirs, nil
}

// Old native rollouts cannot be attributed to a person. On upgrade discard this
// rebuildable cache; the database's delivered history bootstraps the next turn.
func removeLegacyState(root string) error {
	dirs, err := workspaceStateDirs(root)
	if err != nil {
		return err
	}
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if e.IsDir() && stateHash(e.Name()) {
				if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
func (t *TenantRuntime) DeleteUserState(ctx context.Context, user int64) error {
	if user <= 0 {
		return errors.New("invalid user")
	}
	dirs, err := workspaceStateDirs(t.base.StateDir)
	if err != nil {
		return err
	}
	for _, dir := range dirs {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := os.RemoveAll(filepath.Join(dir, "users", nativeUserDir(user))); err != nil {
			return err
		}
	}
	return nil
}
func (t *TenantRuntime) CleanupState(ctx context.Context, days int) error {
	if days < 1 || days > 3650 {
		return errors.New("invalid retention")
	}
	dirs, err := workspaceStateDirs(t.base.StateDir)
	if err != nil {
		return err
	}
	cutoff := time.Now().Add(-time.Duration(days) * 24 * time.Hour)
	for _, dir := range dirs {
		users, err := os.ReadDir(filepath.Join(dir, "users"))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		for _, user := range users {
			if !user.IsDir() || !stateHash(user.Name()) {
				continue
			}
			homes, err := os.ReadDir(filepath.Join(dir, "users", user.Name()))
			if err != nil {
				return err
			}
			for _, home := range homes {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				if !home.IsDir() || !stateHash(home.Name()) {
					continue
				}
				info, err := home.Info()
				if err != nil {
					return err
				}
				if info.ModTime().Before(cutoff) {
					if err := os.RemoveAll(filepath.Join(dir, "users", user.Name(), home.Name())); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}
func (t *TenantRuntime) RunStateMaintenance(ctx context.Context, days int) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		if err := t.CleanupState(ctx, days); err != nil && ctx.Err() == nil {
			slog.Error("native_retention_failed")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
