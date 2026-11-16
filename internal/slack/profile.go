package slack

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Read only the name needed to identify a workspace's usage; never persist email.
func (c APIClient) UserDisplayName(ctx context.Context, token, user string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://slack.com/api/users.info?user="+url.QueryEscape(user), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return "", errors.New("Slack profile unavailable")
	}
	defer resp.Body.Close()
	var data struct {
		OK   bool `json:"ok"`
		User struct {
			ID      string `json:"id"`
			Profile struct {
				DisplayName string `json:"display_name"`
				RealName    string `json:"real_name"`
			} `json:"profile"`
		} `json:"user"`
	}
	if resp.StatusCode != 200 || json.NewDecoder(io.LimitReader(resp.Body, 65536)).Decode(&data) != nil || !data.OK || data.User.ID != user {
		return "", errors.New("Slack profile unavailable")
	}
	name := strings.TrimSpace(data.User.Profile.DisplayName)
	if name == "" {
		name = strings.TrimSpace(data.User.Profile.RealName)
	}
	if len([]rune(name)) > 100 {
		return "", errors.New("Slack name too long")
	}
	return name, nil
}
