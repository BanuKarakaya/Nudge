package raindrop

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	authorizeURL   = "https://raindrop.io/oauth/authorize"
	accessTokenURL = "https://raindrop.io/oauth/access_token"
	apiBaseURL     = "https://api.raindrop.io/rest/v1"
)

type OAuthClient struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string
	HTTPClient   *http.Client
}

type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	TokenType    string `json:"token_type"`
}

type authenticatedUserResponse struct {
	Result bool `json:"result"`
	User   struct {
		ID int64 `json:"_id"`
	} `json:"user"`
}

type RaindropItem struct {
	ID      int64  `json:"_id"`
	Title   string `json:"title"`
	Link    string `json:"link"`
	Excerpt string `json:"excerpt"`
	Created string `json:"created"`
}

type raindropsResponse struct {
	Result bool           `json:"result"`
	Items  []RaindropItem `json:"items"`
}

func (c OAuthClient) InstallURL(state string) string {
	params := url.Values{}
	params.Set("response_type", "code")
	params.Set("client_id", c.ClientID)
	params.Set("redirect_uri", c.RedirectURL)
	params.Set("state", state)
	return authorizeURL + "?" + params.Encode()
}

func (c OAuthClient) ExchangeCode(ctx context.Context, code string) (TokenResponse, error) {
	payload, err := json.Marshal(map[string]string{
		"code":          code,
		"client_id":     c.ClientID,
		"client_secret": c.ClientSecret,
		"redirect_uri":  c.RedirectURL,
		"grant_type":    "authorization_code",
	})
	if err != nil {
		return TokenResponse{}, err
	}

	resp, err := c.do(ctx, http.MethodPost, accessTokenURL, strings.NewReader(string(payload)), "application/json", "")
	if err != nil {
		return TokenResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return TokenResponse{}, fmt.Errorf("Raindrop token exchange returned HTTP %d", resp.StatusCode)
	}
	var result TokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return TokenResponse{}, err
	}
	if result.AccessToken == "" {
		return TokenResponse{}, fmt.Errorf("Raindrop token exchange returned no access token")
	}
	return result, nil
}

func (c OAuthClient) GetAuthenticatedUser(ctx context.Context, accessToken string) (int64, error) {
	resp, err := c.do(ctx, http.MethodGet, apiBaseURL+"/user", nil, "", accessToken)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, fmt.Errorf("Raindrop user request returned HTTP %d", resp.StatusCode)
	}
	var result authenticatedUserResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, err
	}
	if !result.Result || result.User.ID == 0 {
		return 0, fmt.Errorf("Raindrop user request failed")
	}
	return result.User.ID, nil
}

func (c OAuthClient) ListRaindrops(ctx context.Context, accessToken string, page, perPage int) ([]RaindropItem, error) {
	params := url.Values{}
	params.Set("sort", "-created")
	params.Set("page", fmt.Sprintf("%d", page))
	params.Set("perpage", fmt.Sprintf("%d", perPage))
	endpoint := apiBaseURL + "/raindrops/0?" + params.Encode()

	resp, err := c.do(ctx, http.MethodGet, endpoint, nil, "", accessToken)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("Raindrop list request returned HTTP %d", resp.StatusCode)
	}

	var result raindropsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	if !result.Result {
		return nil, fmt.Errorf("Raindrop list request failed")
	}
	return result.Items, nil
}

func (c OAuthClient) do(ctx context.Context, method, endpoint string, body io.Reader, contentType, accessToken string) (*http.Response, error) {
	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+accessToken)
	}
	return client.Do(req)
}

func ExpiresAt(expiresIn int64) *time.Time {
	if expiresIn <= 0 {
		return nil
	}
	value := time.Now().UTC().Add(time.Duration(expiresIn) * time.Second)
	return &value
}
