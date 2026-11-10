package tracker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"
)

func jiraBase(r Resource) string {
	return "https://api.atlassian.com/ex/jira/" + r.SiteID + "/rest/api/3"
}

func (c Client) CreateOptions(ctx context.Context, token string, r Resource, issueType string) (CreateOptions, error) {
	result := CreateOptions{Options: []Option{}}
	if !validResource(c.Kind(), r) {
		return result, ErrArguments
	}
	if c.Kind() == "trello" {
		if issueType != "" {
			return result, ErrArguments
		}
		var lists []struct {
			ID, Name string
			Board    string `json:"idBoard"`
			Closed   bool
		}
		if err := c.request(ctx, "https://api.trello.com/1/boards/"+r.ID+"/lists?filter=open&fields=name,idBoard,closed", token, nil, &lists); err != nil {
			return result, err
		}
		if len(lists) > 2000 {
			return result, ErrUnavailable
		}
		for _, list := range lists {
			if list.Board != r.ID || !trelloID.MatchString(list.ID) {
				return result, ErrDenied
			}
			if !list.Closed {
				result.Options = append(result.Options, Option{ID: list.ID, Name: list.Name})
			}
		}
		return result, nil
	}
	if issueType != "" && !decimalID.MatchString(issueType) {
		return result, ErrArguments
	}
	endpoint := jiraBase(r) + "/issue/createmeta/" + r.ProjectID + "/issuetypes"
	if issueType != "" {
		endpoint += "/" + issueType
	}
	for start := 0; start < 2000; {
		var page struct {
			IssueTypes []struct {
				ID, Name string
				Subtask  bool
			} `json:"issueTypes"`
			Fields []Field `json:"fields"`
			Total  int     `json:"total"`
		}
		if err := c.request(ctx, fmt.Sprintf("%s?startAt=%d&maxResults=100", endpoint, start), token, nil, &page); err != nil {
			return result, err
		}
		count := len(page.IssueTypes)
		if issueType == "" {
			for _, typ := range page.IssueTypes {
				if !decimalID.MatchString(typ.ID) {
					return result, ErrUnavailable
				}
				result.Options = append(result.Options, Option{ID: typ.ID, Name: typ.Name, Subtask: typ.Subtask})
			}
		} else {
			count = len(page.Fields)
			for _, field := range page.Fields {
				if field.ID == "" {
					return result, ErrUnavailable
				}
			}
			result.Fields = append(result.Fields, page.Fields...)
		}
		if count > 100 || page.Total > 2000 {
			return CreateOptions{}, ErrUnavailable
		}
		start += count
		if start >= page.Total {
			return result, nil
		}
		if count == 0 {
			return CreateOptions{}, ErrUnavailable
		}
	}
	return CreateOptions{}, ErrUnavailable
}
func (c Client) MoveOptions(ctx context.Context, token string, r Resource, item string) ([]Option, error) {
	if _, err := c.Item(ctx, token, r, item); err != nil {
		return nil, err
	}
	if c.Kind() == "trello" {
		options, err := c.CreateOptions(ctx, token, r, "")
		return options.Options, err
	}
	var body struct {
		Transitions []struct {
			ID, Name  string
			To        struct{ ID, Name string }
			Available *bool `json:"isAvailable"`
			Fields    map[string]Field
		}
	}
	if err := c.request(ctx, jiraBase(r)+"/issue/"+item+"/transitions?expand=transitions.fields", token, nil, &body); err != nil {
		return nil, err
	}
	if len(body.Transitions) > 1000 {
		return nil, ErrUnavailable
	}
	result := []Option{}
	for _, v := range body.Transitions {
		if v.Available != nil && !*v.Available {
			continue
		}
		if !decimalID.MatchString(v.ID) || !decimalID.MatchString(v.To.ID) {
			return nil, ErrUnavailable
		}
		result = append(result, Option{ID: v.ID, Name: v.Name, StatusID: v.To.ID, Status: v.To.Name, Fields: v.Fields})
	}
	return result, nil
}
func optionByID(options []Option, id string) (Option, error) {
	for _, v := range options {
		if v.ID == id {
			return v, nil
		}
	}
	return Option{}, ErrArguments
}
func validateFields(fields map[string]any, metadata map[string]Field) error {
	for key, value := range fields {
		field, ok := metadata[key]
		if !ok || value == nil {
			return ErrArguments
		}
		set := false
		for _, op := range field.Operations {
			if op == "set" {
				set = true
			}
		}
		if !set {
			return ErrArguments
		}
	}
	for key, field := range metadata {
		v, ok := fields[key]
		if field.Required && !field.HasDefault && (!ok || v == nil || v == "") {
			return ErrRejected
		}
	}
	return nil
}
func additionalFields(extra map[string]any) (map[string]any, error) {
	// Creation/transition cannot relocate an issue or alter its hierarchy.
	result := map[string]any{}
	for k, v := range extra {
		if k == "project" || k == "issuetype" || k == "parent" || k == "status" || k == "summary" || k == "description" {
			return nil, ErrArguments
		}
		result[k] = v
	}
	raw, err := json.Marshal(result)
	if err != nil || len(raw) > 32768 {
		return nil, ErrArguments
	}
	return result, nil
}
func adf(text string) any {
	paragraphs := []any{}
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		content := []any{}
		if line != "" {
			content = append(content, map[string]any{"type": "text", "text": line})
		}
		paragraphs = append(paragraphs, map[string]any{"type": "paragraph", "content": content})
	}
	return map[string]any{"type": "doc", "version": 1, "content": paragraphs}
}
func (c Client) Create(ctx context.Context, token string, r Resource, input CreateInput) (MutationResult, error) {
	if !validResource(c.Kind(), r) || strings.TrimSpace(input.Title) == "" || !utf8.ValidString(input.Title) || utf8.RuneCountInString(input.Title) > 255 || !utf8.ValidString(input.Description) || len(input.Description) > 16000 {
		return MutationResult{}, ErrArguments
	}
	options, err := c.CreateOptions(ctx, token, r, "")
	if err != nil {
		return MutationResult{}, err
	}
	selected, err := optionByID(options.Options, input.Destination)
	if err != nil {
		return MutationResult{}, err
	}
	if c.Kind() == "trello" {
		if input.Parent != "" {
			return MutationResult{}, ErrArguments
		}
		if len(input.Fields) != 0 {
			return MutationResult{}, ErrArguments
		}
		var card trelloCard
		err = c.requestMethod(ctx, http.MethodPost, "https://api.trello.com/1/cards", token, map[string]any{"idList": input.Destination, "name": input.Title, "desc": input.Description}, &card)
		if err != nil {
			return MutationResult{}, err
		}
		if !trelloID.MatchString(card.ID) || card.Board != r.ID || card.List != input.Destination {
			return MutationResult{}, ErrOutcomeUnknown
		}
		return MutationResult{Status: "created", Item: card.item()}, nil
	}
	if selected.Subtask != (input.Parent != "") {
		return MutationResult{}, ErrArguments
	}
	if input.Parent != "" {
		if _, err = c.Item(ctx, token, r, input.Parent); err != nil {
			return MutationResult{}, err
		}
	}
	metadata, err := c.CreateOptions(ctx, token, r, input.Destination)
	if err != nil {
		return MutationResult{}, err
	}
	fields, err := additionalFields(input.Fields)
	if err != nil {
		return MutationResult{}, err
	}
	fields["project"] = map[string]string{"id": r.ProjectID}
	fields["issuetype"] = map[string]string{"id": input.Destination}
	fields["summary"] = input.Title
	if input.Parent != "" {
		parentField := "key"
		if decimalID.MatchString(input.Parent) {
			parentField = "id"
		}
		fields["parent"] = map[string]string{parentField: input.Parent}
	}
	if input.Description != "" {
		fields["description"] = adf(input.Description)
	}
	allowed := map[string]Field{}
	for _, f := range metadata.Fields {
		allowed[f.ID] = f
	}
	if err = validateFields(fields, allowed); err != nil {
		return MutationResult{}, err
	}
	var issue jiraIssue
	if err = c.requestMethod(ctx, http.MethodPost, jiraBase(r)+"/issue", token, map[string]any{"fields": fields}, &issue); err != nil {
		return MutationResult{}, err
	}
	if !decimalID.MatchString(issue.ID) || !issueKey.MatchString(issue.Key) {
		return MutationResult{}, ErrOutcomeUnknown
	}
	// The creation response only contains id/key. Do not turn a successful write
	// into a failure by requiring a follow-up read (which could fail separately).
	result := issue.item(r)
	result.Title = input.Title
	return MutationResult{Status: "created", Item: result}, nil
}
func (c Client) Move(ctx context.Context, token string, r Resource, input MoveInput) (MutationResult, error) {
	if input.ExpectedStatus == "" {
		return MutationResult{}, ErrArguments
	}
	options, err := c.MoveOptions(ctx, token, r, input.Item)
	if err != nil {
		return MutationResult{}, err
	}
	target, err := optionByID(options, input.Destination)
	if err != nil {
		return MutationResult{}, err
	}
	fields, err := additionalFields(input.Fields)
	if err != nil {
		return MutationResult{}, err
	}
	if c.Kind() == "trello" {
		if len(fields) != 0 {
			return MutationResult{}, ErrArguments
		}
	} else if err = validateFields(fields, target.Fields); err != nil {
		return MutationResult{}, err
	}
	// Read immediately before writing to catch a stale Doing -> Testing request.
	current, err := c.Item(ctx, token, r, input.Item)
	if err != nil {
		return MutationResult{}, err
	}
	targetStatus := target.StatusID
	if c.Kind() == "trello" {
		targetStatus = target.ID
	}
	if current.Status == "archived" && c.Kind() == "trello" {
		return MutationResult{}, ErrRejected
	}
	if current.StatusID == targetStatus {
		return MutationResult{Status: "already_at_destination", Item: current}, nil
	}
	if current.StatusID != input.ExpectedStatus {
		return MutationResult{}, ErrChanged
	}
	if c.Kind() == "trello" {
		var card trelloCard
		if err = c.requestMethod(ctx, http.MethodPut, "https://api.trello.com/1/cards/"+input.Item, token, map[string]string{"idList": target.ID}, &card); err != nil {
			return MutationResult{}, err
		}
		if card.ID != input.Item || card.Board != r.ID || card.List != target.ID {
			return MutationResult{}, ErrOutcomeUnknown
		}
		return MutationResult{Status: "moved", Item: card.item()}, nil
	}
	body := map[string]any{"transition": map[string]string{"id": target.ID}}
	if len(fields) > 0 {
		body["fields"] = fields
	}
	if err = c.requestMethod(ctx, http.MethodPost, jiraBase(r)+"/issue/"+input.Item+"/transitions", token, body, nil); err != nil {
		return MutationResult{}, err
	}
	current.StatusID = target.StatusID
	current.Status = target.Status
	return MutationResult{Status: "moved", Item: current}, nil
}
