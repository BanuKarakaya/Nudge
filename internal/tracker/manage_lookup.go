package tracker

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
)

type Comment struct {
	ID     string `json:"id"`
	Text   any    `json:"text"`
	Author Member `json:"author"`
}
type Checklist struct {
	ID    string      `json:"id"`
	Name  string      `json:"name"`
	Board string      `json:"idBoard"`
	Card  string      `json:"idCard"`
	Items []CheckItem `json:"checkItems"`
}
type CheckItem struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	State string `json:"state"`
}
type CommentPage struct {
	Comments   []Comment `json:"comments"`
	NextCursor string    `json:"next_cursor,omitempty"`
}
type MemberPage struct {
	Members    []Member `json:"members"`
	NextCursor string   `json:"next_cursor,omitempty"`
}

func offset(cursor string) (int, error) {
	if cursor == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(cursor)
	if err != nil || n < 0 || n > 1000000 {
		return 0, ErrArguments
	}
	return n, nil
}
func (c Client) jiraMembers(ctx context.Context, token string, r Resource, item, query, account, cursor string) (MemberPage, error) {
	start, err := offset(cursor)
	if err != nil || start >= 1000 || len(query) > 200 || len(account) > 128 {
		return MemberPage{}, ErrArguments
	}
	q := url.Values{"project": {r.ProjectID}, "startAt": {strconv.Itoa(start)}, "maxResults": {"100"}}
	if item != "" {
		q.Set("issueKey", item)
	}
	if query != "" {
		q.Set("query", query)
	}
	if account != "" {
		q.Set("accountId", account)
	}
	var users []struct {
		AccountID, DisplayName string
		Active                 bool
	}
	if err = c.request(ctx, jiraBase(r)+"/user/assignable/search?"+q.Encode(), token, nil, &users); err != nil {
		return MemberPage{}, err
	}
	if len(users) > 100 {
		return MemberPage{}, ErrUnavailable
	}
	result := MemberPage{Members: []Member{}}
	for _, u := range users {
		if u.Active && u.AccountID != "" {
			result.Members = append(result.Members, Member{ID: u.AccountID, Name: u.DisplayName})
		}
	}
	// Jira filters after slicing the user directory: a short page is not EOF.
	if account == "" && start+100 < 1000 {
		result.NextCursor = strconv.Itoa(start + 100)
	}
	return result, nil
}
func (c Client) trelloMembers(ctx context.Context, token string, r Resource) ([]Member, error) {
	var users []struct{ ID, FullName, Username string }
	if err := c.request(ctx, "https://api.trello.com/1/boards/"+r.ID+"/members?fields=fullName,username", token, nil, &users); err != nil {
		return nil, err
	}
	if len(users) > 2000 {
		return nil, ErrUnavailable
	}
	result := []Member{}
	for _, u := range users {
		if !trelloID.MatchString(u.ID) {
			return nil, ErrUnavailable
		}
		result = append(result, Member{ID: u.ID, Name: u.FullName, Username: u.Username})
	}
	return result, nil
}
func (c Client) trelloLabels(ctx context.Context, token string, r Resource) ([]Label, error) {
	var labels []Label
	if err := c.request(ctx, "https://api.trello.com/1/boards/"+r.ID+"/labels?fields=name,color&limit=1000", token, nil, &labels); err != nil {
		return nil, err
	}
	if len(labels) >= 1000 {
		return nil, ErrUnavailable
	}
	for _, v := range labels {
		if !trelloID.MatchString(v.ID) {
			return nil, ErrUnavailable
		}
	}
	return labels, nil
}
func (c Client) checklists(ctx context.Context, token string, r Resource, item string) ([]Checklist, error) {
	var lists []Checklist
	if err := c.request(ctx, "https://api.trello.com/1/cards/"+item+"/checklists?checkItems=all&fields=name,idBoard,idCard", token, nil, &lists); err != nil {
		return nil, err
	}
	if len(lists) > 1000 {
		return nil, ErrUnavailable
	}
	for _, l := range lists {
		if !trelloID.MatchString(l.ID) || l.Card != item || l.Board != r.ID {
			return nil, ErrDenied
		}
	}
	return lists, nil
}
func (c Client) editFields(ctx context.Context, token string, r Resource, item string) (map[string]Field, error) {
	var meta struct{ Fields map[string]Field }
	err := c.request(ctx, jiraBase(r)+"/issue/"+item+"/editmeta", token, nil, &meta)
	return meta.Fields, err
}
func (c Client) Lookup(ctx context.Context, token string, r Resource, in LookupInput) (any, error) {
	if !validResource(c.Kind(), r) || len(in.Cursor) > 100 || len(in.Query) > 200 {
		return nil, ErrArguments
	}
	if in.Item != "" {
		if _, err := c.Item(ctx, token, r, in.Item); err != nil {
			return nil, err
		}
	}
	switch in.Kind {
	case "members":
		if c.Kind() == "trello" {
			if in.Cursor != "" {
				return nil, ErrArguments
			}
			return c.trelloMembers(ctx, token, r)
		}
		return c.jiraMembers(ctx, token, r, in.Item, in.Query, "", in.Cursor)
	case "labels":
		if c.Kind() != "trello" {
			return nil, ErrArguments
		}
		return c.trelloLabels(ctx, token, r)
	case "edit_fields":
		if c.Kind() != "jira" || in.Item == "" {
			return nil, ErrArguments
		}
		fields, err := c.editFields(ctx, token, r, in.Item)
		if err != nil {
			return nil, err
		}
		var issue struct{ Fields map[string]any }
		if err = c.request(ctx, jiraBase(r)+"/issue/"+in.Item+"?fields=*all", token, nil, &issue); err != nil {
			return nil, err
		}
		project, ok := issue.Fields["project"].(map[string]any)
		if !ok || project["id"] != r.ProjectID {
			return nil, ErrDenied
		}
		values := map[string]any{}
		for key := range fields {
			if value, ok := issue.Fields[key]; ok {
				values[key] = value
			}
		}
		return map[string]any{"fields": fields, "values": values}, nil
	case "checklists":
		if c.Kind() != "trello" || in.Item == "" {
			return nil, ErrArguments
		}
		return c.checklists(ctx, token, r, in.Item)
	case "comments":
		if in.Item == "" {
			return nil, ErrArguments
		}
		result := CommentPage{Comments: []Comment{}}
		if c.Kind() == "trello" {
			q := url.Values{"filter": {"commentCard"}, "limit": {"50"}}
			if in.Cursor != "" {
				if !trelloID.MatchString(in.Cursor) {
					return nil, ErrArguments
				}
				q.Set("before", in.Cursor)
			}
			var actions []struct {
				ID   string
				Data struct {
					Text string
					Card struct{ ID string }
				}
				MemberCreator struct{ ID, FullName, Username string }
			}
			if err := c.request(ctx, "https://api.trello.com/1/cards/"+in.Item+"/actions?"+q.Encode(), token, nil, &actions); err != nil {
				return nil, err
			}
			if len(actions) > 50 {
				return nil, ErrUnavailable
			}
			for _, a := range actions {
				if a.Data.Card.ID != in.Item || !trelloID.MatchString(a.ID) {
					return nil, ErrDenied
				}
				result.Comments = append(result.Comments, Comment{ID: a.ID, Text: a.Data.Text, Author: Member{ID: a.MemberCreator.ID, Name: a.MemberCreator.FullName, Username: a.MemberCreator.Username}})
			}
			if len(actions) == 50 {
				result.NextCursor = actions[49].ID
				if result.NextCursor == in.Cursor {
					return nil, ErrUnavailable
				}
			}
		} else {
			start, err := offset(in.Cursor)
			if err != nil {
				return nil, err
			}
			var page struct {
				Comments []struct {
					ID     string
					Body   any
					Author struct{ AccountID, DisplayName string }
				}
				Total int
			}
			if err = c.request(ctx, fmt.Sprintf("%s/issue/%s/comment?startAt=%d&maxResults=50", jiraBase(r), in.Item, start), token, nil, &page); err != nil {
				return nil, err
			}
			if len(page.Comments) > 50 {
				return nil, ErrUnavailable
			}
			for _, v := range page.Comments {
				result.Comments = append(result.Comments, Comment{ID: v.ID, Text: v.Body, Author: Member{ID: v.Author.AccountID, Name: v.Author.DisplayName}})
			}
			if start+len(page.Comments) < page.Total {
				if len(page.Comments) == 0 {
					return nil, ErrUnavailable
				}
				result.NextCursor = strconv.Itoa(start + len(page.Comments))
			}
		}
		return result, nil
	}
	return nil, ErrArguments
}
