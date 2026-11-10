package tracker

import (
	"context"
	"fmt"
	"net/url"
)

func (c Client) Resources(ctx context.Context, token string) ([]Resource, error) {
	if c.Kind() == "trello" {
		var boards []struct {
			ID, Name, URL string
			Closed        bool
		}
		if err := c.request(ctx, "https://api.trello.com/1/members/me/boards?filter=open&fields=name,url,closed", token, nil, &boards); err != nil {
			return nil, err
		}
		if len(boards) > 2000 {
			return nil, ErrUnavailable
		}
		result := []Resource{}
		for _, b := range boards {
			if trelloID.MatchString(b.ID) && !b.Closed {
				result = append(result, Resource{ID: b.ID, Name: b.Name, URL: safeURL(b.URL)})
			}
		}
		return result, nil
	}
	var sites []struct {
		ID, Name, URL string
		Scopes        []string
	}
	if err := c.request(ctx, "https://api.atlassian.com/oauth/token/accessible-resources", token, nil, &sites); err != nil {
		return nil, err
	}
	if len(sites) > 100 {
		return nil, ErrUnavailable
	}
	result := []Resource{}
	seen := map[string]bool{}
	for _, site := range sites {
		hasScope := false
		for _, scope := range site.Scopes {
			if scope == "read:jira-work" {
				hasScope = true
			}
		}
		if !hasScope || !cloudID.MatchString(site.ID) {
			continue
		}
		start := 0
		for {
			var page struct {
				Values []struct{ ID, Name, Key string }
				IsLast bool
				Total  int
			}
			endpoint := fmt.Sprintf("https://api.atlassian.com/ex/jira/%s/rest/api/3/project/search?startAt=%d&maxResults=100", site.ID, start)
			if err := c.request(ctx, endpoint, token, nil, &page); err != nil {
				return nil, err
			}
			for _, p := range page.Values {
				id := site.ID + ":" + p.ID
				if !decimalID.MatchString(p.ID) || seen[id] {
					return nil, ErrUnavailable
				}
				seen[id] = true
				result = append(result, Resource{ID: id, Name: site.Name + " / " + p.Name + " (" + p.Key + ")", URL: safeURL(site.URL) + "/browse/" + url.PathEscape(p.Key), SiteID: site.ID, ProjectID: p.ID})
			}
			if len(result) > 2000 {
				return nil, ErrUnavailable
			}
			start += len(page.Values)
			if page.IsLast || start >= page.Total {
				break
			}
			if len(page.Values) == 0 {
				return nil, ErrUnavailable
			}
		}
	}
	return result, nil
}

type jiraIssue struct {
	ID, Key string
	Fields  struct {
		Summary      string
		Description  any
		Status       struct{ ID, Name string }
		Project      struct{ ID string }
		Assignee     *struct{ DisplayName, AccountID string }
		Labels       []string
		DueDate      string
		Priority     any
		Components   any
		FixVersions  any
		Versions     any
		Timetracking any
		Subtasks     any
		Parent       any
	}
}

func (issue jiraIssue) item(r Resource) Item {
	// The resource URL is a browser link only; requests always use the fixed API origin.
	u, _ := url.Parse(r.URL)
	browserURL := ""
	if u != nil && u.Host != "" {
		u.Path = "/browse/" + issue.Key
		u.RawQuery = ""
		u.Fragment = ""
		browserURL = u.String()
	}
	item := Item{ID: issue.ID, Key: issue.Key, Title: issue.Fields.Summary, Status: issue.Fields.Status.Name, StatusID: issue.Fields.Status.ID, Description: issue.Fields.Description, URL: browserURL}
	item.Due = issue.Fields.DueDate
	item.Fields = map[string]any{"labels": issue.Fields.Labels, "priority": issue.Fields.Priority, "components": issue.Fields.Components, "fixVersions": issue.Fields.FixVersions, "versions": issue.Fields.Versions, "timetracking": issue.Fields.Timetracking, "subtasks": issue.Fields.Subtasks, "parent": issue.Fields.Parent}
	if issue.Fields.Assignee != nil {
		item.MemberIDs = []string{issue.Fields.Assignee.AccountID}
		item.Assignee = issue.Fields.Assignee.DisplayName
	}
	return item
}

type trelloCard struct {
	ID, Name, URL, Desc string
	Board               string `json:"idBoard"`
	List                string `json:"idList"`
	Due                 string
	Closed              bool
	Start               string
	DueComplete         bool
	Members             []string `json:"idMembers"`
	Labels              []string `json:"idLabels"`
}

func (card trelloCard) item() Item {
	status := "open"
	if card.Closed {
		status = "archived"
	}
	return Item{ID: card.ID, Title: card.Name, URL: safeURL(card.URL), Description: card.Desc, Status: status, StatusID: card.List, Due: card.Due, Start: card.Start, DueComplete: card.DueComplete, MemberIDs: card.Members, LabelIDs: card.Labels}
}
func validResource(kind string, r Resource) bool {
	if kind == "trello" {
		return trelloID.MatchString(r.ID)
	}
	return cloudID.MatchString(r.SiteID) && decimalID.MatchString(r.ProjectID) && r.ID == r.SiteID+":"+r.ProjectID
}
func (c Client) Items(ctx context.Context, token string, r Resource, cursor string) (Page, error) {
	if !validResource(c.Kind(), r) {
		return Page{}, ErrArguments
	}
	result := Page{Items: []Item{}}
	if c.Kind() == "trello" {
		if cursor != "" && !trelloID.MatchString(cursor) {
			return result, ErrArguments
		}
		q := url.Values{"limit": {"50"}, "sort": {"-id"}, "fields": {"name,url,idBoard,idList,due,closed,start,dueComplete,idMembers,idLabels"}}
		if cursor != "" {
			q.Set("before", cursor)
		}
		var cards []trelloCard
		if err := c.request(ctx, "https://api.trello.com/1/boards/"+r.ID+"/cards/open?"+q.Encode(), token, nil, &cards); err != nil {
			return result, err
		}
		if len(cards) > 50 {
			return result, ErrUnavailable
		}
		for _, card := range cards {
			if card.Board != r.ID || !trelloID.MatchString(card.ID) {
				return Page{}, ErrDenied
			}
			result.Items = append(result.Items, card.item())
		}
		if len(cards) == 50 {
			result.NextCursor = cards[len(cards)-1].ID
			if result.NextCursor == cursor {
				return Page{}, ErrUnavailable
			}
		}
		return result, nil
	}
	q := url.Values{"jql": {"project = " + r.ProjectID + " ORDER BY created DESC, key DESC"}, "maxResults": {"50"}, "fields": {"summary,status,assignee,project"}}
	if cursor != "" {
		q.Set("nextPageToken", cursor)
	}
	var page struct {
		Issues        []jiraIssue
		NextPageToken string
		IsLast        bool
	}
	if err := c.request(ctx, "https://api.atlassian.com/ex/jira/"+r.SiteID+"/rest/api/3/search/jql?"+q.Encode(), token, nil, &page); err != nil {
		return result, err
	}
	if len(page.Issues) > 50 {
		return result, ErrUnavailable
	}
	for _, issue := range page.Issues {
		if issue.Fields.Project.ID != r.ProjectID {
			return Page{}, ErrDenied
		}
		result.Items = append(result.Items, issue.item(r))
	}
	if !page.IsLast {
		result.NextCursor = page.NextPageToken
		if result.NextCursor == "" || result.NextCursor == cursor {
			return Page{}, ErrUnavailable
		}
	}
	return result, nil
}
func (c Client) Item(ctx context.Context, token string, r Resource, id string) (Item, error) {
	if !validResource(c.Kind(), r) {
		return Item{}, ErrArguments
	}
	if c.Kind() == "trello" {
		if !trelloID.MatchString(id) {
			return Item{}, ErrArguments
		}
		var card trelloCard
		if err := c.request(ctx, "https://api.trello.com/1/cards/"+id+"?fields=name,url,desc,idBoard,idList,due,closed,start,dueComplete,idMembers,idLabels", token, nil, &card); err != nil {
			return Item{}, err
		}
		if card.Board != r.ID || card.ID != id {
			return Item{}, ErrDenied
		}
		return card.item(), nil
	}
	if len(id) > 100 || !issueKey.MatchString(id) {
		return Item{}, ErrArguments
	}
	var issue jiraIssue
	if err := c.request(ctx, "https://api.atlassian.com/ex/jira/"+r.SiteID+"/rest/api/3/issue/"+id+"?fields=summary,status,assignee,description,project,labels,duedate,priority,components,fixVersions,versions,timetracking,subtasks,parent", token, nil, &issue); err != nil {
		return Item{}, err
	}
	if issue.Fields.Project.ID != r.ProjectID || (issue.ID != id && issue.Key != id) {
		return Item{}, ErrDenied
	}
	return issue.item(r), nil
}
