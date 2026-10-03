package slack

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const oauthAuthorizeURL = "https://slack.com/oauth/v2/authorize"
const oauthAccessURL = "https://slack.com/api/oauth.v2.access"

type OAuthClient struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string
	HTTPClient   *http.Client
}

type OAuthResponse struct {
	OK          bool   `json:"ok"`
	Error       string `json:"error"`
	AccessToken string `json:"access_token"`
	Team        struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"team"`
	AuthedUser struct {
		ID string `json:"id"`
	} `json:"authed_user"`
}

func (c OAuthClient) InstallURL(state string) string {
	params := url.Values{}
	params.Set("client_id", c.ClientID)
	params.Set("scope", "chat:write,users:read")
	params.Set("redirect_uri", c.RedirectURL)
	params.Set("state", state)
	return oauthAuthorizeURL + "?" + params.Encode()
}

func (c OAuthClient) ExchangeCode(ctx context.Context, code string) (OAuthResponse, error) {
	form := url.Values{}
	form.Set("client_id", c.ClientID)
	form.Set("client_secret", c.ClientSecret)
	form.Set("code", code)
	form.Set("redirect_uri", c.RedirectURL)

	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, oauthAccessURL, strings.NewReader(form.Encode()))
	if err != nil {
		return OAuthResponse{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := client.Do(req)
	if err != nil {
		return OAuthResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return OAuthResponse{}, fmt.Errorf("Slack OAuth returned HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return OAuthResponse{}, err
	}
	var result OAuthResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return OAuthResponse{}, err
	}
	if !result.OK {
		return OAuthResponse{}, fmt.Errorf("Slack OAuth failed: %s", result.Error)
	}
	return result, nil
}
