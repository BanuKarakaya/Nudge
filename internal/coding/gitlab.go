package coding

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// GitLab accepts only gitlab.com endpoints; clone URLs never come from API data.
type GitLab struct {
	ClientID, ClientSecret, RedirectURL string
	HTTPClient                          *http.Client
}

func (g GitLab) Validate() error {
	u, err := url.Parse(g.RedirectURL)
	if g.ClientID == "" || g.ClientSecret == "" || err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "/gitlab/oauth/callback" {
		return ErrArguments
	}
	return nil
}
func (g GitLab) InstallationURL(string) string                                    { return "" }
func (g GitLab) VerifyInstallation(context.Context, string, int64, Account) error { return ErrDenied }
func (g GitLab) AuthorizeURL(state, verifier string) string {
	hash := sha256.Sum256([]byte(verifier))
	return "https://gitlab.com/oauth/authorize?" + url.Values{"client_id": {g.ClientID}, "redirect_uri": {g.RedirectURL}, "response_type": {"code"}, "scope": {"read_user read_api write_repository"}, "state": {state}, "code_challenge": {base64.RawURLEncoding.EncodeToString(hash[:])}, "code_challenge_method": {"S256"}}.Encode()
}
func (g GitLab) request(ctx context.Context, endpoint, token string, form url.Values, out any) error {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.Host != "gitlab.com" || u.User != nil {
		return ErrArguments
	}
	method := http.MethodGet
	var body io.Reader
	if form != nil {
		method = http.MethodPost
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return ErrUnavailable
	}
	req.Header.Set("Accept", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	client := http.Client{Timeout: 15 * time.Second}
	if g.HTTPClient != nil {
		client = *g.HTTPClient
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		return ErrUnavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 {
		return ErrConnect
	}
	if resp.StatusCode == 403 || resp.StatusCode == 404 {
		return ErrDenied
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ErrUnavailable
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, (4<<20)+1))
	if err != nil || len(data) > 4<<20 {
		return ErrUnavailable
	}
	if json.Unmarshal(data, out) != nil {
		return ErrUnavailable
	}
	return nil
}
func (g GitLab) token(ctx context.Context, form url.Values) (Credentials, error) {
	form.Set("client_id", g.ClientID)
	form.Set("client_secret", g.ClientSecret)
	form.Set("redirect_uri", g.RedirectURL)
	var r struct {
		Access  string `json:"access_token"`
		Refresh string `json:"refresh_token"`
		Expires int64  `json:"expires_in"`
		Type    string `json:"token_type"`
		Scope   string `json:"scope"`
	}
	if err := g.request(ctx, "https://gitlab.com/oauth/token", "", form, &r); err != nil {
		return Credentials{}, err
	}
	if !validGitLabToken(r.Access) || !validGitLabToken(r.Refresh) || r.Expires <= 0 || r.Expires > 366*86400 || !strings.EqualFold(r.Type, "bearer") {
		return Credentials{}, ErrConnect
	}
	if r.Scope != "" {
		for _, required := range []string{"read_user", "read_api", "write_repository"} {
			if !strings.Contains(" "+r.Scope+" ", " "+required+" ") {
				return Credentials{}, ErrDenied
			}
		}
	}
	return Credentials{Access: r.Access, Refresh: r.Refresh, Expires: time.Now().Add(time.Duration(r.Expires) * time.Second)}, nil
}
func validGitLabToken(token string) bool {
	if len(token) < 16 || len(token) > 2048 {
		return false
	}
	for _, r := range token {
		if r <= 32 || r >= 127 {
			return false
		}
	}
	return true
}
func (g GitLab) Exchange(ctx context.Context, code, verifier string) (Credentials, error) {
	return g.token(ctx, url.Values{"grant_type": {"authorization_code"}, "code": {code}, "code_verifier": {verifier}})
}
func (g GitLab) Refresh(ctx context.Context, refresh string) (Credentials, error) {
	return g.token(ctx, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}})
}
func (g GitLab) User(ctx context.Context, token string) (int64, string, error) {
	var u struct {
		ID       int64  `json:"id"`
		Username string `json:"username"`
		State    string `json:"state"`
	}
	if err := g.request(ctx, "https://gitlab.com/api/v4/user", token, nil, &u); err != nil {
		return 0, "", err
	}
	if u.ID <= 0 || u.Username == "" || u.State != "active" {
		return 0, "", ErrConnect
	}
	return u.ID, u.Username, nil
}

type gitLabProject struct {
	ID            int64  `json:"id"`
	Path          string `json:"path_with_namespace"`
	DefaultBranch string `json:"default_branch"`
	Archived      bool   `json:"archived"`
	Empty         bool   `json:"empty_repo"`
	Permissions   struct {
		Project *struct {
			Level int `json:"access_level"`
		} `json:"project_access"`
		Group *struct {
			Level int `json:"access_level"`
		} `json:"group_access"`
	} `json:"permissions"`
}

func (p gitLabProject) repository() (Repository, bool) {
	level := 0
	if p.Permissions.Project != nil {
		level = p.Permissions.Project.Level
	}
	if p.Permissions.Group != nil {
		level = max(level, p.Permissions.Group.Level)
	}
	r := Repository{ID: p.ID, FullName: p.Path, DefaultBranch: p.DefaultBranch, Provider: "gitlab"}
	return r, level >= 30 && !p.Archived && !p.Empty && p.DefaultBranch != "" && r.Valid()
}
func (g GitLab) Repositories(ctx context.Context, token string) ([]Repository, error) {
	result := []Repository{}
	seen := map[int64]bool{}
	for page := 1; page <= 20; page++ {
		var projects []gitLabProject
		if err := g.request(ctx, fmt.Sprintf("https://gitlab.com/api/v4/projects?membership=true&min_access_level=30&archived=false&order_by=id&sort=asc&per_page=100&page=%d", page), token, nil, &projects); err != nil {
			return nil, err
		}
		for _, p := range projects {
			if r, ok := p.repository(); ok && !seen[r.ID] {
				seen[r.ID] = true
				result = append(result, r)
			}
		}
		if len(projects) < 100 {
			return result, nil
		}
	}
	return nil, ErrUnavailable
}
func (g GitLab) CheckRepository(ctx context.Context, token string, account Account, repo Repository) error {
	if repo.Provider != "gitlab" || !repo.Valid() || account.ID <= 0 {
		return ErrDenied
	}
	var p gitLabProject
	if err := g.request(ctx, "https://gitlab.com/api/v4/projects/"+strconv.FormatInt(repo.ID, 10), token, nil, &p); err != nil {
		return err
	}
	r, ok := p.repository()
	if !ok || r != repo {
		return ErrDenied
	}
	return nil
}
