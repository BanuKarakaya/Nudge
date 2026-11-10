package tracker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

func validText(s string, max int, empty bool) bool {
	return utf8.ValidString(s) && len(s) <= max && (empty || strings.TrimSpace(s) != "")
}
func stringValue(v any, max int, empty bool) bool {
	s, ok := v.(string)
	return ok && validText(s, max, empty)
}
func jsonIDs(v any) ([]string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, ErrArguments
	}
	var ids []string
	if json.Unmarshal(raw, &ids) != nil || ids == nil || len(ids) > 100 {
		return nil, ErrArguments
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if !trelloID.MatchString(id) || seen[id] {
			return nil, ErrArguments
		}
		seen[id] = true
	}
	return ids, nil
}
func (c Client) trelloUpdate(ctx context.Context, token string, r Resource, item string, fields map[string]any) (MutationResult, error) {
	var card trelloCard
	if err := c.requestMethod(ctx, http.MethodPut, "https://api.trello.com/1/cards/"+item, token, fields, &card); err != nil {
		return MutationResult{}, err
	}
	if card.ID != item || card.Board != r.ID {
		return MutationResult{}, ErrOutcomeUnknown
	}
	return MutationResult{Status: "updated", Item: card.item()}, nil
}
func (c Client) Change(ctx context.Context, token string, r Resource, in ChangeInput) (MutationResult, error) {
	if !validResource(c.Kind(), r) || !validText(in.Text, 16000, true) || len(in.IDs) > 100 {
		return MutationResult{}, ErrArguments
	}
	current, err := c.Item(ctx, token, r, in.Item)
	if err != nil {
		return MutationResult{}, err
	}
	result := MutationResult{Status: "updated", Item: Item{ID: current.ID, Key: current.Key, URL: current.URL}}
	switch in.Operation {
	case "assign":
		if c.Kind() == "trello" {
			if in.Mode != "add" && in.Mode != "remove" && in.Mode != "set" {
				return result, ErrArguments
			}
			members, err := c.trelloMembers(ctx, token, r)
			if err != nil {
				return result, err
			}
			for _, id := range in.IDs {
				if !trelloID.MatchString(id) {
					return result, ErrArguments
				}
				found := false
				for _, m := range members {
					if m.ID == id {
						found = true
					}
				}
				if !found && !(in.Mode == "remove" && slices.Contains(current.MemberIDs, id)) {
					return result, ErrDenied
				}
			}
			ids := append([]string{}, current.MemberIDs...)
			if in.Mode == "set" {
				ids = []string{}
			}
			for _, id := range in.IDs {
				if in.Mode == "remove" {
					ids = slices.DeleteFunc(ids, func(v string) bool { return v == id })
				} else if !slices.Contains(ids, id) {
					ids = append(ids, id)
				}
			}
			return c.trelloUpdate(ctx, token, r, in.Item, map[string]any{"idMembers": strings.Join(ids, ",")})
		}
		if in.Mode != "set" || len(in.IDs) > 1 {
			return result, ErrArguments
		}
		var account any
		if len(in.IDs) == 1 {
			if in.IDs[0] == "" {
				return result, ErrArguments
			}
			users, err := c.jiraMembers(ctx, token, r, in.Item, "", in.IDs[0], "")
			if err != nil {
				return result, err
			}
			found := false
			for _, u := range users.Members {
				if u.ID == in.IDs[0] {
					found = true
				}
			}
			if !found {
				return result, ErrDenied
			}
			account = in.IDs[0]
		}
		if err = c.requestMethod(ctx, http.MethodPut, jiraBase(r)+"/issue/"+in.Item+"/assignee", token, map[string]any{"accountId": account}, nil); err != nil {
			return result, err
		}
		result.Entity = map[string]any{"assignee_account_id": account}
		return result, nil
	case "update":
		if len(in.Fields) == 0 || len(in.Fields) > 50 {
			return result, ErrArguments
		}
		fields := map[string]any{}
		if c.Kind() == "trello" {
			for key, value := range in.Fields {
				switch key {
				case "name":
					if !stringValue(value, 1000, false) {
						return result, ErrArguments
					}
				case "desc":
					if !stringValue(value, 16000, true) {
						return result, ErrArguments
					}
				case "due", "start":
					if value != nil {
						v, ok := value.(string)
						if !ok {
							return result, ErrArguments
						}
						if _, err = time.Parse(time.RFC3339, v); err != nil {
							return result, ErrArguments
						}
					}
				case "dueComplete":
					if _, ok := value.(bool); !ok {
						return result, ErrArguments
					}
				case "idLabels":
					ids, err := jsonIDs(value)
					if err != nil {
						return result, err
					}
					labels, err := c.trelloLabels(ctx, token, r)
					if err != nil {
						return result, err
					}
					for _, id := range ids {
						found := false
						for _, l := range labels {
							if l.ID == id {
								found = true
							}
						}
						if !found {
							return result, ErrDenied
						}
					}
					value = strings.Join(ids, ",")
				default:
					return result, ErrArguments
				}
				fields[key] = value
			}
			return c.trelloUpdate(ctx, token, r, in.Item, fields)
		}
		meta, err := c.editFields(ctx, token, r, in.Item)
		if err != nil {
			return result, err
		}
		for key, value := range in.Fields {
			// Limit patching to task content. Account assignment has its own validated tool;
			// no project/hierarchy/security/reporter/workflow mutations through raw fields.
			switch key {
			case "summary":
				if !stringValue(value, 1000, false) {
					return result, ErrArguments
				}
			case "description", "environment":
				if value != nil {
					if !stringValue(value, 16000, true) {
						return result, ErrArguments
					}
					value = adf(value.(string))
				}
			case "duedate":
				if value != nil {
					v, ok := value.(string)
					if !ok {
						return result, ErrArguments
					}
					if _, err = time.Parse("2006-01-02", v); err != nil {
						return result, ErrArguments
					}
				}
			case "labels", "priority", "components", "fixVersions", "versions", "timetracking":
			default:
				if !strings.HasPrefix(key, "customfield_") || !decimalID.MatchString(strings.TrimPrefix(key, "customfield_")) {
					return result, ErrArguments
				}
			}
			field, ok := meta[key]
			if !ok || !slices.Contains(field.Operations, "set") {
				return result, ErrArguments
			}
			if field.Required && (value == nil || value == "") {
				return result, ErrRejected
			}
			if strings.HasPrefix(key, "customfield_") && field.Schema["custom"] == "com.atlassian.jira.plugin.system.customfieldtypes:textarea" && value != nil {
				if !stringValue(value, 16000, true) {
					return result, ErrArguments
				}
				value = adf(value.(string))
			}
			fields[key] = value
		}
		if err = c.requestMethod(ctx, http.MethodPut, jiraBase(r)+"/issue/"+in.Item, token, map[string]any{"fields": fields}, nil); err != nil {
			return result, err
		}
		result.Entity = map[string]any{"updated_fields": in.Fields}
		return result, nil
	case "archive", "restore":
		if c.Kind() == "trello" {
			return c.trelloUpdate(ctx, token, r, in.Item, map[string]any{"closed": in.Operation == "archive"})
		}
		// Single-issue endpoint; no JQL bulk archive or administrator-setting tools.
		endpoint := "archive"
		if in.Operation == "restore" {
			endpoint = "unarchive"
		}
		var outcome struct {
			NumberOfIssuesUpdated int
			Errors                map[string]json.RawMessage
		}
		if err = c.requestMethod(ctx, http.MethodPut, jiraBase(r)+"/issue/"+endpoint, token, map[string]any{"issueIdsOrKeys": []string{in.Item}}, &outcome); err != nil {
			return result, err
		}
		if outcome.NumberOfIssuesUpdated == 0 {
			return result, ErrRejected
		}
		if len(outcome.Errors) > 0 {
			return result, ErrOutcomeUnknown
		}
		result.Status = in.Operation + "d"
		if in.Operation == "restore" {
			result.Status = "restored"
		}
		return result, nil
	case "comment_add", "comment_update":
		if !validText(in.Text, 16000, false) {
			return result, ErrArguments
		}
		method := http.MethodPost
		var body any
		endpoint := ""
		if c.Kind() == "trello" {
			endpoint = "https://api.trello.com/1/cards/" + in.Item + "/actions/comments"
			body = map[string]string{"text": in.Text}
			if in.Operation == "comment_update" {
				if !trelloID.MatchString(in.Target) {
					return result, ErrArguments
				}
				var action struct {
					ID, Type string
					Data     struct{ Card struct{ ID string } }
				}
				if err = c.request(ctx, "https://api.trello.com/1/actions/"+in.Target, token, nil, &action); err != nil {
					return result, err
				}
				if action.ID != in.Target || action.Type != "commentCard" || action.Data.Card.ID != in.Item {
					return result, ErrDenied
				}
				endpoint = "https://api.trello.com/1/cards/" + in.Item + "/actions/" + in.Target + "/comments"
				method = http.MethodPut
			}
		} else {
			endpoint = jiraBase(r) + "/issue/" + in.Item + "/comment"
			body = map[string]any{"body": adf(in.Text)}
			if in.Operation == "comment_update" {
				if !decimalID.MatchString(in.Target) {
					return result, ErrArguments
				}
				endpoint += "/" + in.Target
				method = http.MethodPut
				var comment struct{ ID string }
				if err = c.request(ctx, endpoint, token, nil, &comment); err != nil {
					return result, err
				}
				if comment.ID != in.Target {
					return result, ErrDenied
				}
			}
		}
		var entity struct {
			ID string `json:"id"`
		}
		if err = c.requestMethod(ctx, method, endpoint, token, body, &entity); err != nil {
			return result, err
		}
		if entity.ID == "" || (in.Operation == "comment_update" && entity.ID != in.Target) {
			return result, ErrOutcomeUnknown
		}
		result.Entity = entity
		result.Status = "comment_saved"
		return result, nil
	case "checklist_create", "checklist_rename", "checkitem_add", "checkitem_update":
		if c.Kind() != "trello" {
			return result, ErrArguments
		}
		endpoint := "https://api.trello.com/1/cards/" + in.Item + "/checklists"
		method := http.MethodPost
		body := map[string]any{"name": in.Text}
		if in.Operation != "checkitem_update" && !validText(in.Text, 1000, false) {
			return result, ErrArguments
		}
		if in.Operation != "checklist_create" {
			if !trelloID.MatchString(in.Target) {
				return result, ErrArguments
			}
			lists, err := c.checklists(ctx, token, r, in.Item)
			if err != nil {
				return result, err
			}
			found := false
			for _, l := range lists {
				if in.Operation == "checkitem_update" {
					for _, v := range l.Items {
						if v.ID == in.Target {
							found = true
						}
					}
				} else if l.ID == in.Target {
					found = true
				}
			}
			if !found {
				return result, ErrDenied
			}
			switch in.Operation {
			case "checklist_rename":
				endpoint = "https://api.trello.com/1/checklists/" + in.Target
				method = http.MethodPut
			case "checkitem_add":
				endpoint = "https://api.trello.com/1/checklists/" + in.Target + "/checkItems"
			case "checkitem_update":
				endpoint = "https://api.trello.com/1/cards/" + in.Item + "/checkItem/" + in.Target
				method = http.MethodPut
				body = map[string]any{}
				if len(in.Fields) == 0 {
					return result, ErrArguments
				}
				for k, v := range in.Fields {
					if k == "name" {
						if !stringValue(v, 1000, false) {
							return result, ErrArguments
						}
					} else if k == "state" {
						if v != "complete" && v != "incomplete" {
							return result, ErrArguments
						}
					} else {
						return result, ErrArguments
					}
					body[k] = v
				}
			}
		}
		// Checklist endpoints document query parameters. Use encoded values; never
		// place credentials here. No provider-supplied URL is followed.
		if in.Operation == "checklist_create" || in.Operation == "checkitem_add" || in.Operation == "checklist_rename" {
			endpoint += "?" + url.Values{"name": {in.Text}}.Encode()
			body = nil
		}
		var entity struct {
			ID    string `json:"id"`
			Name  string `json:"name"`
			State string `json:"state,omitempty"`
		}
		if err = c.requestMethod(ctx, method, endpoint, token, body, &entity); err != nil {
			return result, err
		}
		if !trelloID.MatchString(entity.ID) {
			return result, ErrOutcomeUnknown
		}
		result.Status = "checklist_saved"
		result.Entity = entity
		return result, nil
	}
	return MutationResult{}, ErrArguments
}
