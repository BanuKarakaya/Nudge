package coding

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// GitHub uses user-to-server tokens, never installation-wide credentials. No
// caller-controlled API origin, redirects, clone URLs or write endpoints exist.
type GitHub struct {
	AppID                               int64
	ClientID, ClientSecret, RedirectURL string
	AppSlug                             string
	HTTPClient                          *http.Client
}

func (g GitHub) Validate() error {
	u, err := url.Parse(g.RedirectURL)
	if g.AppID <= 0 || !githubLogin.MatchString(g.AppSlug) || g.ClientID == "" || g.ClientSecret == "" || err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "/github/oauth/callback" {
		return ErrArguments
	}
	return nil
}

func (g GitHub) InstallationURL(state string) string {
	return "https://github.com/apps/" + g.AppSlug + "/installations/new?" + url.Values{"state": {state}}.Encode()
}

// VerifyInstallation binds the GitHub redirect to the authenticated user and
// requires at least one selected, usable repository before saving credentials.
func (g GitHub) VerifyInstallation(ctx context.Context, token string, installationID int64, user Account) error {
	if !strings.HasPrefix(token, "ghu_") || installationID < 0 || user.ID <= 0 || !githubLogin.MatchString(user.Login) {
		return ErrConnect
	}
	for page := 1; page <= 20; page++ {
		var response struct {
			Installations []struct {
				ID      int64 `json:"id"`
				AppID   int64 `json:"app_id"`
				Account struct {
					ID   int64  `json:"id"`
					Type string `json:"type"`
				} `json:"account"`
				SuspendedAt *time.Time `json:"suspended_at"`
				Permissions struct {
					Contents     string `json:"contents"`
					PullRequests string `json:"pull_requests"`
				} `json:"permissions"`
			} `json:"installations"`
		}
		if err := g.request(ctx, fmt.Sprintf("https://api.github.com/user/installations?per_page=100&page=%d", page), token, nil, &response); err != nil {
			return err
		}
		for _, installation := range response.Installations {
			if installationID == 0 && (installation.AppID != g.AppID || installation.Account.ID != user.ID || installation.Account.Type != "User") {
				continue
			}
			if installationID > 0 && installation.ID != installationID {
				continue
			}
			if installation.ID <= 0 || installation.AppID != g.AppID || installation.SuspendedAt != nil || installation.Permissions.Contents != "write" || installation.Permissions.PullRequests != "write" {
				return ErrDenied
			}
			// A personal installation must belong to the connecting account, not a coworker.
			if installation.Account.ID <= 0 || (installation.Account.Type != "Organization" && (installation.Account.Type != "User" || installation.Account.ID != user.ID)) {
				return ErrDenied
			}
			repos, err := g.installationRepos(ctx, token, installation.ID)
			if err != nil {
				return err
			}
			for _, repo := range repos {
				allowed, err := g.repositoryPermission(ctx, token, user, repo)
				if err != nil {
					return err
				}
				if allowed {
					return nil
				}
			}
			return ErrDenied
		}
		if len(response.Installations) < 100 {
			return ErrDenied
		}
	}
	return ErrUnavailable
}

func (g GitHub) AuthorizeURL(state, verifier string) string {
	hash := sha256.Sum256([]byte(verifier))
	q := url.Values{"client_id": {g.ClientID}, "redirect_uri": {g.RedirectURL}, "state": {state}, "code_challenge": {base64.RawURLEncoding.EncodeToString(hash[:])}, "code_challenge_method": {"S256"}, "allow_signup": {"false"}, "prompt": {"select_account"}}
	return "https://github.com/login/oauth/authorize?" + q.Encode()
}

func (g GitHub) Exchange(ctx context.Context, code, verifier string) (Credentials, error) {
	return g.token(ctx, url.Values{"code": {code}, "code_verifier": {verifier}, "redirect_uri": {g.RedirectURL}})
}

func (g GitHub) Refresh(ctx context.Context, refresh string) (Credentials, error) {
	return g.token(ctx, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}})
}

func (g GitHub) token(ctx context.Context, form url.Values) (Credentials, error) {
	form.Set("client_id", g.ClientID)
	form.Set("client_secret", g.ClientSecret)
	var response struct {
		Access         string `json:"access_token"`
		Refresh        string `json:"refresh_token"`
		Expires        int64  `json:"expires_in"`
		RefreshExpires int64  `json:"refresh_token_expires_in"`
		Error          string `json:"error"`
	}
	if err := g.request(ctx, "https://github.com/login/oauth/access_token", "", form, &response); err != nil {
		return Credentials{}, err
	}
	// ghu_ ensures this is a GitHub App user token, not an installation/PAT token.
	if response.Error != "" || !strings.HasPrefix(response.Access, "ghu_") || response.Expires < 0 || response.Expires > 86400 || response.RefreshExpires < 0 || response.RefreshExpires > 366*86400 {
		return Credentials{}, ErrConnect
	}
	c := Credentials{Access: response.Access, Refresh: response.Refresh}
	if response.Expires > 0 {
		if !strings.HasPrefix(response.Refresh, "ghr_") || response.RefreshExpires == 0 {
			return Credentials{}, ErrConnect
		}
		c.Expires = time.Now().Add(time.Duration(response.Expires) * time.Second)
		c.RefreshExpires = time.Now().Add(time.Duration(response.RefreshExpires) * time.Second)
	}
	return c, nil
}

func (g GitHub) User(ctx context.Context, token string) (int64, string, error) {
	var user struct {
		ID    int64  `json:"id"`
		Login string `json:"login"`
	}
	err := g.request(ctx, "https://api.github.com/user", token, nil, &user)
	return user.ID, user.Login, err
}

func (g GitHub) Repositories(ctx context.Context, token string) ([]Repository, error) {
	if !strings.HasPrefix(token, "ghu_") {
		return nil, ErrConnect
	}
	userID, login, err := g.User(ctx, token)
	if err != nil {
		return nil, err
	}
	if userID <= 0 || !githubLogin.MatchString(login) {
		return nil, ErrConnect
	}
	var repos []Repository
	seen := make(map[int64]bool)
	for page := 1; page <= 20; page++ {
		var response struct {
			Installations []struct {
				ID          int64      `json:"id"`
				AppID       int64      `json:"app_id"`
				SuspendedAt *time.Time `json:"suspended_at"`
				Permissions struct {
					Contents     string `json:"contents"`
					PullRequests string `json:"pull_requests"`
				} `json:"permissions"`
			} `json:"installations"`
		}
		if err := g.request(ctx, fmt.Sprintf("https://api.github.com/user/installations?per_page=100&page=%d", page), token, nil, &response); err != nil {
			return nil, err
		}
		for _, installation := range response.Installations {
			if installation.ID <= 0 || installation.AppID != g.AppID || installation.SuspendedAt != nil || installation.Permissions.Contents != "write" || installation.Permissions.PullRequests != "write" {
				continue
			}
			items, err := g.installationRepos(ctx, token, installation.ID)
			if err != nil {
				return nil, err
			}
			for _, repo := range items {
				if !seen[repo.ID] {
					seen[repo.ID] = true
					allowed, err := g.repositoryPermission(ctx, token, Account{ID: userID, Login: login}, repo)
					if err != nil {
						return nil, err
					}
					if allowed {
						repos = append(repos, repo)
					}
				}
			}
		}
		if len(response.Installations) < 100 {
			return repos, nil
		}
	}
	return nil, ErrUnavailable // Never present a partial page traversal as complete.
}

// Installation inventory is only a candidate list. Confirm the named user's
// effective role independently; never equate app write access with user access.
func (g GitHub) repositoryPermission(ctx context.Context, token string, user Account, repo Repository) (bool, error) {
	var permission struct {
		Permission string  `json:"permission"`
		User       Account `json:"user"`
	}
	err := g.request(ctx, "https://api.github.com/repos/"+repo.FullName+"/collaborators/"+user.Login+"/permission", token, nil, &permission)
	if errors.Is(err, ErrDenied) {
		log.Printf("coding_event=repository_permission_unverified github_user_id=%d repository_id=%d", user.ID, repo.ID)
		return false, nil
	}
	if err != nil {
		return false, err
	}
	allowed := permission.User.ID == user.ID && (permission.Permission == "admin" || permission.Permission == "write")
	role := permission.Permission
	switch role {
	case "admin", "write", "read", "none":
	default:
		role = "unknown"
	}
	log.Printf("coding_event=repository_permission_checked github_user_id=%d repository_id=%d role=%q allowed=%t", user.ID, repo.ID, role, allowed)
	return allowed, nil
}

func (g GitHub) installationRepos(ctx context.Context, token string, installationID int64) ([]Repository, error) {
	var repos []Repository
	for page := 1; page <= 20; page++ {
		var response struct {
			Repositories []struct {
				ID            int64  `json:"id"`
				FullName      string `json:"full_name"`
				DefaultBranch string `json:"default_branch"`
				Archived      bool   `json:"archived"`
				Disabled      bool   `json:"disabled"`
				Permissions   struct {
					Push bool `json:"push"`
				} `json:"permissions"`
			} `json:"repositories"`
		}
		if err := g.request(ctx, fmt.Sprintf("https://api.github.com/user/installations/%d/repositories?per_page=100&page=%d", installationID, page), token, nil, &response); err != nil {
			return nil, err
		}
		for _, repo := range response.Repositories {
			if repo.Permissions.Push && !repo.Archived && !repo.Disabled && ValidRepository(repo.FullName) && repo.ID > 0 {
				repos = append(repos, Repository{ID: repo.ID, FullName: repo.FullName, DefaultBranch: repo.DefaultBranch, InstallationID: installationID})
			}
		}
		if len(response.Repositories) < 100 {
			return repos, nil
		}
	}
	return nil, ErrUnavailable
}

func (g GitHub) request(ctx context.Context, endpoint, token string, form url.Values, output any) error {
	var body io.Reader
	method := http.MethodGet
	if form != nil {
		method = http.MethodPost
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return ErrUnavailable
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-GitHub-Api-Version", "2026-03-10")
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
	// Do not follow even same-host redirects: moved resources require reapproval.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if client.Timeout == 0 || client.Timeout > 15*time.Second {
		client.Timeout = 15 * time.Second
	}
	resp, err := client.Do(req)
	if err != nil {
		return ErrUnavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return ErrConnect
	}
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusNotFound {
		return ErrDenied
	}
	if resp.StatusCode != http.StatusOK {
		return ErrUnavailable
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024+1))
	if err != nil || len(raw) > 4*1024*1024 || json.Unmarshal(raw, output) != nil {
		return ErrUnavailable
	}
	return nil
}
