package slack

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"nudge/internal/domain"
)

func VerifySignature(signingSecret, timestamp, signature string, body []byte) bool {
	if signingSecret == "" || timestamp == "" || signature == "" {
		return false
	}
	timestampValue, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil || abs(time.Now().Unix()-timestampValue) > 300 {
		return false
	}
	basestring := "v0:" + timestamp + ":" + string(body)
	hash := hmac.New(sha256.New, []byte(signingSecret))
	_, _ = hash.Write([]byte(basestring))
	expected := "v0=" + fmt.Sprintf("%x", hash.Sum(nil))
	return subtle.ConstantTimeCompare([]byte(expected), []byte(signature)) == 1
}

func abs(value int64) int64 {
	if value < 0 {
		return -value
	}
	return value
}

const postMessageURL = "https://slack.com/api/chat.postMessage"

type APIClient struct {
	HTTPClient *http.Client
}

type postMessageRequest struct {
	Channel string `json:"channel"`
	Text    string `json:"text"`
}

type postMessageResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error"`
}

type bookmarkMessageRequest struct {
	Channel     string           `json:"channel"`
	Text        string           `json:"text"`
	Blocks      []map[string]any `json:"blocks"`
	UnfurlLinks bool             `json:"unfurl_links"`
	UnfurlMedia bool             `json:"unfurl_media"`
}

func (c APIClient) PostMessage(ctx context.Context, token, channel, text string) error {
	body, err := json.Marshal(postMessageRequest{Channel: channel, Text: text})
	if err != nil {
		return err
	}

	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, postMessageURL, strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var result postMessageResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || !result.OK {
		if result.Error == "" {
			result.Error = resp.Status
		}
		return fmt.Errorf("Slack chat.postMessage failed: %s", result.Error)
	}
	return nil
}

func (c APIClient) PostBookmarkMessage(ctx context.Context, token, channel string, bookmark domain.Bookmark) error {
	valueRead, _ := json.Marshal(map[string]int64{"user_id": bookmark.UserID, "bookmark_id": bookmark.ID})
	valueUnread := string(valueRead)

	content := fmt.Sprintf("*📚 Bugün kaydettikleriniz*\n\n*%s*\n🔗 <%s|Bookmark’ı aç>", escapeMrkdwn(bookmark.Title), bookmark.URL)
	if err := c.PostMessage(ctx, token, channel, content); err != nil {
		return err
	}

	payload := bookmarkMessageRequest{
		Channel:     channel,
		Text:        "Okuma durumu",
		UnfurlLinks: false,
		UnfurlMedia: false,
		Blocks: []map[string]any{
			{
				"type": "actions",
				"elements": []map[string]any{
					{
						"type":      "button",
						"text":      map[string]string{"type": "plain_text", "text": "Okudum"},
						"action_id": "bookmark_read",
						"value":     string(valueRead),
						"style":     "primary",
					},
					{
						"type":      "button",
						"text":      map[string]string{"type": "plain_text", "text": "Okumadım"},
						"action_id": "bookmark_unread",
						"value":     valueUnread,
					},
				},
			},
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return c.post(ctx, token, body)
}

func (c APIClient) post(ctx context.Context, token string, body []byte) error {
	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, postMessageURL, strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var result postMessageResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || !result.OK {
		if result.Error == "" {
			result.Error = resp.Status
		}
		return fmt.Errorf("Slack chat.postMessage failed: %s", result.Error)
	}
	return nil
}

func escapeMrkdwn(value string) string {
	value = strings.ReplaceAll(value, "&", "&amp;")
	value = strings.ReplaceAll(value, "<", "&lt;")
	return strings.ReplaceAll(value, ">", "&gt;")
}
