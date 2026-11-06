package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"nudge/internal/coding"
)

// Namespaced tools allow both accounts in one Slack turn without inferring a
// provider from repository names, numeric IDs or conversation history.
type gitLabTools struct{ base, inner AgentTools }

func GitLabTools(base, inner AgentTools) AgentTools { return &gitLabTools{base: base, inner: inner} }
func gitLabToolName(name string) string {
	switch name {
	case "github_connect":
		return "gitlab_connect"
	case "github_connection_status":
		return "gitlab_connection_status"
	}
	return "gitlab_" + name
}
func (t *gitLabTools) Definitions() []ToolDefinition {
	base := t.base.Definitions()
	all := t.inner.Definitions()
	defs := append([]ToolDefinition(nil), base...)
	for _, d := range all[len(base):] {
		d.Name = gitLabToolName(d.Name)
		d.Description = strings.ReplaceAll(d.Description, "GitHub", "GitLab")
		d.Description = strings.ReplaceAll(d.Description, "github_account", "gitlab_account")
		d.Description = strings.ReplaceAll(d.Description, "GitLab App installation and account connection", "GitLab authorization and project selection")
		d.Description = strings.ReplaceAll(d.Description, "GitLab is not implemented yet.", "")
		d.Description = strings.ReplaceAll(d.Description, "github_connect", "gitlab_connect")
		d.Description = strings.ReplaceAll(d.Description, "pull request", "merge request")
		d.Description = strings.ReplaceAll(d.Description, " PR", " MR")
		d.Description = strings.ReplaceAll(d.Description, "repositories_list", "gitlab_repositories_list")
		d.Description = strings.ReplaceAll(d.Description, "repository_edit_finish", "gitlab_repository_edit_finish")
		switch d.Name {
		case "gitlab_connect":
			d.Description = "Send the current Slack user a private GitLab OAuth connection link. They authorize on gitlab.com and then select projects on Nudgy. Stop after sending a connection-only link; do not list repositories or create tasks. Never ask for credentials in Slack."
		case "gitlab_repositories_list":
			d.Description = "List only the GitLab projects selected by this Slack user, after verifying their connected GitLab account and current Developer-or-higher access. Returns exact namespace/subgroup/project paths and gitlab_account. Use these paths rather than guessing."
		}
		if properties, ok := d.InputSchema["properties"].(map[string]any); ok {
			if _, exists := properties["repository"]; exists {
				properties["repository"] = map[string]any{"type": "string", "description": "Exact namespace/project path returned by gitlab_repositories_list; includes subgroups when present."}
			}
		}
		defs = append(defs, d)
	}
	return defs
}
func (t *gitLabTools) Call(ctx context.Context, name string, args json.RawMessage) (string, error) {
	if !strings.HasPrefix(name, "gitlab_") {
		return t.base.Call(ctx, name, args)
	}
	inner := strings.TrimPrefix(name, "gitlab_")
	switch inner {
	case "connect":
		inner = "github_connect"
	case "connection_status":
		inner = "github_connection_status"
	}
	found := false
	for _, d := range t.Definitions() {
		if d.Name == name {
			found = true
			break
		}
	}
	if !found {
		return "", coding.ErrUnavailable
	}
	result, err := t.inner.Call(ctx, inner, args)
	if errors.Is(err, coding.ErrConnect) {
		return "", fmt.Errorf("GitLab bağlantısı gerekli; gitlab_connect aracını kullan")
	}
	if errors.Is(err, coding.ErrDenied) {
		return "", fmt.Errorf("GitLab projesi seçilmemiş veya güncel yazma yetkin yok")
	}
	if errors.Is(err, coding.ErrUnavailable) {
		return "", fmt.Errorf("GitLab bağlantısı şu anda doğrulanamadı")
	}
	return result, err
}
