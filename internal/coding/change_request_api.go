package coding

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Only provider-constructed URLs reach this transport. Redirects and response
// bodies in errors are excluded so credentials and private contents cannot leak.
func changeAPI(ctx context.Context, client *http.Client, host, token, method, endpoint string, payload, out any) error {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.Host != host || u.User != nil || u.Fragment != "" {
		return ErrArguments
	}
	var body io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return ErrArguments
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return ErrArguments
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if host == "api.github.com" {
		req.Header.Set("X-GitHub-Api-Version", "2026-03-10")
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	c := http.Client{Timeout: 15 * time.Second}
	if client != nil {
		c = *client
	}
	if c.Timeout <= 0 || c.Timeout > 15*time.Second {
		c.Timeout = 15 * time.Second
	}
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := c.Do(req)
	if err != nil {
		return ErrUnavailable
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case 401:
		return ErrConnect
	case 403, 404:
		return ErrDenied
	case 405, 409, 422:
		return ErrMergeBlocked
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return ErrUnavailable
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, (4<<20)+1))
	if err != nil || len(raw) > 4<<20 || json.Unmarshal(raw, out) != nil {
		return ErrUnavailable
	}
	return nil
}

type githubChange struct {
	Number         int    `json:"number"`
	State          string `json:"state"`
	Draft          bool   `json:"draft"`
	Merged         bool   `json:"merged"`
	MergeSHA       string `json:"merge_commit_sha"`
	Mergeable      *bool  `json:"mergeable"`
	MergeableState string `json:"mergeable_state"`
	Head           struct {
		Ref  string `json:"ref"`
		SHA  string `json:"sha"`
		Repo struct {
			ID int64 `json:"id"`
		} `json:"repo"`
	} `json:"head"`
	Base struct {
		Ref  string `json:"ref"`
		Repo struct {
			ID int64 `json:"id"`
		} `json:"repo"`
	} `json:"base"`
}

func (v githubChange) change(r Repository) (ChangeRequest, error) {
	if v.Number <= 0 || v.Base.Repo.ID != r.ID || !gitObjectID.MatchString(v.Head.SHA) || v.Head.Ref == "" || v.Base.Ref == "" {
		return ChangeRequest{}, ErrUnavailable
	}
	state := v.State
	if v.Merged {
		state = "merged"
	}
	if state != "open" && state != "closed" && state != "merged" {
		return ChangeRequest{}, ErrUnavailable
	}
	return ChangeRequest{Number: v.Number, URL: r.Origin() + "/" + r.FullName + "/pull/" + strconv.Itoa(v.Number), State: state, Draft: v.Draft, HeadSHA: v.Head.SHA, SourceBranch: v.Head.Ref, TargetBranch: v.Base.Ref, MergeSHA: v.MergeSHA, Ready: v.Mergeable != nil && *v.Mergeable && v.MergeableState == "clean"}, nil
}
func githubChanges(r Repository) string {
	return "https://api.github.com/repos/" + r.FullName + "/pulls"
}
func (g GitHub) GetChange(ctx context.Context, token string, r Repository, n int) (ChangeRequest, error) {
	if !r.Valid() || r.Kind() != "github" || n <= 0 {
		return ChangeRequest{}, ErrArguments
	}
	var v githubChange
	if err := changeAPI(ctx, g.HTTPClient, "api.github.com", token, "GET", githubChanges(r)+"/"+strconv.Itoa(n), nil, &v); err != nil {
		return ChangeRequest{}, err
	}
	if v.Number != n {
		return ChangeRequest{}, ErrUnavailable
	}
	return v.change(r)
}
func (g GitHub) FindChange(ctx context.Context, token string, r Repository, source, target string) (*ChangeRequest, error) {
	if !r.Valid() || r.Kind() != "github" {
		return nil, ErrArguments
	}
	for page := 1; page <= 20; page++ {
		query := url.Values{"state": {"all"}, "head": {strings.Split(r.FullName, "/")[0] + ":" + source}, "base": {target}, "sort": {"created"}, "direction": {"desc"}, "per_page": {"100"}, "page": {strconv.Itoa(page)}}
		var values []githubChange
		if err := changeAPI(ctx, g.HTTPClient, "api.github.com", token, "GET", githubChanges(r)+"?"+query.Encode(), nil, &values); err != nil {
			return nil, err
		}
		for _, v := range values {
			if v.Head.Ref == source && v.Base.Ref == target && v.Head.Repo.ID == r.ID && v.Base.Repo.ID == r.ID {
				c, err := g.GetChange(ctx, token, r, v.Number)
				return &c, err
			}
		}
		if len(values) < 100 {
			return nil, nil
		}
	}
	return nil, ErrUnavailable
}
func (g GitHub) CreateChange(ctx context.Context, token string, r Repository, source, target, title, body string) (ChangeRequest, error) {
	if !r.Valid() || r.Kind() != "github" {
		return ChangeRequest{}, ErrArguments
	}
	var v githubChange
	err := changeAPI(ctx, g.HTTPClient, "api.github.com", token, "POST", githubChanges(r), map[string]any{"head": source, "base": target, "title": title, "body": body, "draft": false}, &v)
	if err != nil {
		return ChangeRequest{}, err
	}
	if v.Head.Repo.ID != r.ID {
		return ChangeRequest{}, ErrUnavailable
	}
	return v.change(r)
}
func (g GitHub) MergeChange(ctx context.Context, token string, r Repository, expected ChangeRequest) (ChangeRequest, error) {
	current, err := g.GetChange(ctx, token, r, expected.Number)
	if err != nil {
		return ChangeRequest{}, err
	}
	if current.State == "merged" {
		return current, nil
	}
	if current.HeadSHA != expected.HeadSHA || current.TargetBranch != expected.TargetBranch {
		return ChangeRequest{}, ErrChangeMoved
	}
	if !current.Ready || current.Draft || current.State != "open" {
		return ChangeRequest{}, ErrMergeBlocked
	}
	var result struct {
		Merged bool   `json:"merged"`
		SHA    string `json:"sha"`
	}
	// No force, bypass, branch update, source deletion or deferred auto-merge.
	err = changeAPI(ctx, g.HTTPClient, "api.github.com", token, "PUT", githubChanges(r)+"/"+strconv.Itoa(current.Number)+"/merge", map[string]any{"sha": expected.HeadSHA, "merge_method": "merge"}, &result)
	if err != nil {
		return ChangeRequest{}, err
	}
	if !result.Merged || !gitObjectID.MatchString(result.SHA) {
		return ChangeRequest{}, ErrMergeBlocked
	}
	current.State = "merged"
	current.MergeSHA = result.SHA
	current.Ready = false
	return current, nil
}

type gitlabChange struct {
	Number    int    `json:"iid"`
	ProjectID int64  `json:"project_id"`
	SourceID  int64  `json:"source_project_id"`
	TargetID  int64  `json:"target_project_id"`
	State     string `json:"state"`
	Draft     bool   `json:"draft"`
	WIP       bool   `json:"work_in_progress"`
	Source    string `json:"source_branch"`
	Target    string `json:"target_branch"`
	SHA       string `json:"sha"`
	MergeSHA  string `json:"merge_commit_sha"`
	Status    string `json:"detailed_merge_status"`
}

func (v gitlabChange) change(r Repository) (ChangeRequest, error) {
	if v.Number <= 0 || v.ProjectID != r.ID || v.TargetID != r.ID || !gitObjectID.MatchString(v.SHA) || v.Source == "" || v.Target == "" {
		return ChangeRequest{}, ErrUnavailable
	}
	state := v.State
	if state == "opened" {
		state = "open"
	}
	if state != "open" && state != "closed" && state != "merged" {
		return ChangeRequest{}, ErrUnavailable
	}
	return ChangeRequest{Number: v.Number, URL: r.Origin() + "/" + r.FullName + "/-/merge_requests/" + strconv.Itoa(v.Number), State: state, Draft: v.Draft || v.WIP, HeadSHA: v.SHA, SourceBranch: v.Source, TargetBranch: v.Target, MergeSHA: v.MergeSHA, Ready: v.Status == "mergeable"}, nil
}
func gitlabChanges(r Repository) string {
	return "https://gitlab.com/api/v4/projects/" + strconv.FormatInt(r.ID, 10) + "/merge_requests"
}
func (g GitLab) GetChange(ctx context.Context, token string, r Repository, n int) (ChangeRequest, error) {
	if !r.Valid() || r.Kind() != "gitlab" || n <= 0 {
		return ChangeRequest{}, ErrArguments
	}
	var v gitlabChange
	if err := changeAPI(ctx, g.HTTPClient, "gitlab.com", token, "GET", gitlabChanges(r)+"/"+strconv.Itoa(n), nil, &v); err != nil {
		return ChangeRequest{}, err
	}
	if v.Number != n {
		return ChangeRequest{}, ErrUnavailable
	}
	return v.change(r)
}
func (g GitLab) FindChange(ctx context.Context, token string, r Repository, source, target string) (*ChangeRequest, error) {
	if !r.Valid() || r.Kind() != "gitlab" {
		return nil, ErrArguments
	}
	for page := 1; page <= 20; page++ {
		query := url.Values{"scope": {"all"}, "state": {"all"}, "source_branch": {source}, "target_branch": {target}, "order_by": {"created_at"}, "sort": {"desc"}, "per_page": {"100"}, "page": {strconv.Itoa(page)}}
		var values []gitlabChange
		if err := changeAPI(ctx, g.HTTPClient, "gitlab.com", token, "GET", gitlabChanges(r)+"?"+query.Encode(), nil, &values); err != nil {
			return nil, err
		}
		for _, v := range values {
			if v.Source == source && v.Target == target && v.SourceID == r.ID && v.TargetID == r.ID {
				c, err := g.GetChange(ctx, token, r, v.Number)
				return &c, err
			}
		}
		if len(values) < 100 {
			return nil, nil
		}
	}
	return nil, ErrUnavailable
}
func (g GitLab) CreateChange(ctx context.Context, token string, r Repository, source, target, title, body string) (ChangeRequest, error) {
	if !r.Valid() || r.Kind() != "gitlab" {
		return ChangeRequest{}, ErrArguments
	}
	var v gitlabChange
	err := changeAPI(ctx, g.HTTPClient, "gitlab.com", token, "POST", gitlabChanges(r), map[string]any{"source_branch": source, "target_branch": target, "title": title, "description": body, "remove_source_branch": false}, &v)
	if err != nil {
		return ChangeRequest{}, err
	}
	if v.SourceID != r.ID {
		return ChangeRequest{}, ErrUnavailable
	}
	return v.change(r)
}
func (g GitLab) MergeChange(ctx context.Context, token string, r Repository, expected ChangeRequest) (ChangeRequest, error) {
	current, err := g.GetChange(ctx, token, r, expected.Number)
	if err != nil {
		return ChangeRequest{}, err
	}
	if current.State == "merged" {
		return current, nil
	}
	if current.HeadSHA != expected.HeadSHA || current.TargetBranch != expected.TargetBranch {
		return ChangeRequest{}, ErrChangeMoved
	}
	if !current.Ready || current.Draft || current.State != "open" {
		return ChangeRequest{}, ErrMergeBlocked
	}
	var v gitlabChange
	err = changeAPI(ctx, g.HTTPClient, "gitlab.com", token, "PUT", gitlabChanges(r)+"/"+strconv.Itoa(current.Number)+"/merge", map[string]any{"sha": expected.HeadSHA, "auto_merge": false, "should_remove_source_branch": false}, &v)
	if err != nil {
		return ChangeRequest{}, err
	}
	result, err := v.change(r)
	if err != nil {
		return ChangeRequest{}, err
	}
	if result.Number != expected.Number || result.State != "merged" {
		return ChangeRequest{}, ErrMergeBlocked
	}
	return result, nil
}
