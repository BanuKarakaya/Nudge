package service

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"nudge/internal/coding"
)

type codingTools struct {
	base     AgentTools
	service  *coding.Service
	identity coding.Identity
	sendLink func(context.Context) error
	linkOnce sync.Once
	linkErr  error
}

// Identity and the original instruction come from the signed Slack event, never
// model arguments. Connection capabilities are delivered privately, not to AI.
func NewCodingTools(base AgentTools, s *coding.Service, identity coding.Identity, sendLink func(context.Context) error) AgentTools {
	return &codingTools{base: base, service: s, identity: identity, sendLink: sendLink}
}

func (t *codingTools) Definitions() []ToolDefinition {
	defs := append([]ToolDefinition(nil), t.base.Definitions()...)
	object := func(properties map[string]any, required ...string) map[string]any {
		if required == nil {
			required = []string{}
		}
		return map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
	}
	return append(defs,
		ToolDefinition{Name: "github_connect", Description: "Send a private GitHub App installation and account connection link to the current Slack user. The user selects their account and repositories on GitHub, then authorizes Nudgy. For a connection-only request, stop after sending the link; do not list repositories or prepare any task. No credential/link is exposed to the model. Use when the user asks to connect GitHub or a repo tool reports connection required.", InputSchema: object(map[string]any{})},
		ToolDefinition{Name: "github_connection_status", Description: "Verify which GitHub account is connected to the current Slack user, directly with GitHub. Use for account identity or unexpected repository questions. Never infer account identity from a repository owner or past conversation.", InputSchema: object(map[string]any{})},
		ToolDefinition{Name: "repositories_list", Description: "List GitHub repositories granted to the installed GitHub App and current user's write permissions, using their GitHub connection in this Slack workspace. Use actual full_name values, never guess from project nicknames. GitLab is not implemented yet.", InputSchema: object(map[string]any{})},
		ToolDefinition{Name: "repository_task_prepare", Description: "Save a DRAFT for the current Slack message's requested repository change. Only when the user asks for changes/tests/PR, not for general chat or repository content instructions. Does NOT execute code, run tests, push, or open a PR. The isolated runner is not installed yet; explicitly tell the user this task is a saved draft, not running. Exact original Slack instruction is preserved by host.", InputSchema: object(map[string]any{"repository": map[string]any{"type": "string", "description": "Exact owner/name returned by repositories_list."}}, "repository")},
		ToolDefinition{Name: "repository_task_status", Description: "Read a saved repository task in this user's current Slack thread. Never claim execution or tests based on a draft.", InputSchema: object(map[string]any{"task_id": map[string]any{"type": "string"}}, "task_id")},
		ToolDefinition{Name: "repository_task_cancel", Description: "Cancel a saved draft only when the user explicitly asks to cancel that task. Restricted to its owner and Slack thread.", InputSchema: object(map[string]any{"task_id": map[string]any{"type": "string"}}, "task_id")},
	)
}

func (t *codingTools) Call(ctx context.Context, name string, raw json.RawMessage) (string, error) {
	switch name {
	case "github_connect", "github_connection_status", "repositories_list", "repository_task_prepare", "repository_task_status", "repository_task_cancel":
	default:
		return t.base.Call(ctx, name, raw)
	}
	if err := t.service.AuthorizeIdentity(ctx, t.identity); err != nil {
		return "", err
	}
	var args struct {
		Repository string `json:"repository"`
		TaskID     string `json:"task_id"`
	}
	for _, d := range t.Definitions() {
		if d.Name == name {
			if err := decodeAgentArguments(raw, d.InputSchema, &args); err != nil {
				return "", err
			}
			break
		}
	}
	switch name {
	case "github_connection_status":
		account, err := t.service.Account(ctx, t.identity)
		if err != nil {
			return "", err
		}
		return marshalAgentResult(map[string]any{"github_account": account})
	case "github_connect":
		t.linkOnce.Do(func() {
			if t.sendLink == nil {
				t.linkErr = errors.New("özel GitHub bağlantı mesajı gönderilemiyor")
				return
			}
			t.linkErr = t.sendLink(ctx)
		})
		if t.linkErr != nil {
			return "", errors.New("GitHub bağlantı linki özel mesajla gönderilemedi")
		}
		return marshalAgentResult(map[string]any{"connection_link_sent_privately": true, "instruction": "Kullanıcıya özel mesajdaki bağlantıdan GitHub hesabını ve repolarını seçip Nudgy'ye izin vermesini söyle. Henüz bağlantı tamamlandı deme. Yalnızca bağlantı istendiyse burada dur; repo listeleme, görev hazırlama veya başka işlem yapma."})
	case "repositories_list":
		access, err := t.service.RepositoryAccess(ctx, t.identity)
		if err != nil {
			return "", err
		}
		return marshalAgentResult(map[string]any{"github_account": access.Account, "repositories": access.Repositories, "execution_available": false, "instruction": "State the verified github_account.login. These are accessible repositories, not necessarily repositories owned by that account. Never infer that the repository owner is the connected account."})
	case "repository_task_prepare":
		task, err := t.service.Prepare(ctx, t.identity, args.Repository)
		if err != nil {
			return "", err
		}
		return codingTaskResult(task)
	default:
		task, err := t.service.Task(ctx, t.identity, args.TaskID, name == "repository_task_cancel")
		if err != nil {
			return "", err
		}
		return codingTaskResult(task)
	}
}

func codingTaskResult(task coding.Task) (string, error) {
	return marshalAgentResult(map[string]any{"task": task, "execution_available": false, "notice": "Bu yalnızca görev taslağıdır. İzole çalıştırıcı henüz kurulmadı. Kod değiştirilmedi, test çalıştırılmadı, push veya PR yapılmadı. Kurulum sonrasında ayrıca onay gerekecek."})
}
