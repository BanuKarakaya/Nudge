package coding

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"
)

const DefaultBranchTemplate = "codex/nudgy-{task_id}"

type BranchRules interface {
	BranchTemplate(context.Context, Identity) (string, error)
}

func (s *Service) WithBranchRules(r BranchRules) { s.branchRules = r }

var branchChars = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`)

// A deliberately conservative subset of git check-ref-format --branch.
func ValidTaskBranch(branch, base string) bool {
	if len(branch) > 240 || !branchChars.MatchString(branch) || branch == base || branch == "HEAD" || strings.HasPrefix(branch, "refs/") || strings.Contains(branch, "..") || strings.HasSuffix(branch, ".") {
		return false
	}
	for _, part := range strings.Split(branch, "/") {
		if part == "" || strings.HasPrefix(part, ".") || strings.HasSuffix(strings.ToLower(part), ".lock") {
			return false
		}
	}
	return true
}

// Values never introduce path separators or arbitrary Git options.
func branchSlug(value string, limit int) string {
	value = strings.NewReplacer("ı", "i", "İ", "i", "ş", "s", "Ş", "s", "ğ", "g", "Ğ", "g", "ü", "u", "Ü", "u", "ö", "o", "Ö", "o", "ç", "c", "Ç", "c").Replace(value)
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(value) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			if dash && b.Len() > 0 && b.Len() < limit {
				b.WriteByte('-')
			}
			dash = false
			if b.Len() >= limit {
				break
			}
			b.WriteRune(r)
		} else {
			dash = true
		}
	}
	result := strings.Trim(b.String(), "-")
	if result == "" {
		return "task"
	}
	return result
}

func RenderBranch(template, id, request, login string, repo Repository, created time.Time) (string, error) {
	if len(template) == 0 || len(template) > 160 || strings.TrimSpace(template) != template || !taskID.MatchString(id) {
		return "", errors.New("branch şablonu 1–160 karakter olmalı")
	}
	parts := strings.Split(repo.FullName, "/")
	branch := strings.NewReplacer("{task_id}", id, "{slug}", branchSlug(request, 48), "{user}", branchSlug(login, 32), "{repo}", branchSlug(parts[len(parts)-1], 40), "{date}", created.UTC().Format("20060102")).Replace(template)
	if !ValidTaskBranch(branch, repo.DefaultBranch) {
		return "", errors.New("geçersiz branch şablonu; {task_id}, {slug}, {user}, {repo}, {date} kullanılabilir; ana dal hedeflenemez")
	}
	return branch, nil
}

func ValidateBranchTemplate(template string) (string, error) {
	return RenderBranch(template, strings.Repeat("a", 32), "Giriş ekranını güncelle", "banu", Repository{FullName: "team/mobile-app", DefaultBranch: "main"}, time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC))
}
