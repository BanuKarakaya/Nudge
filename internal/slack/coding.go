package slack

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

func (c APIClient) PostMessageInThreadWithBlocks(ctx context.Context, token, channel, thread, text string, blocks []map[string]any) error {
	return c.postText(ctx, token, postMessageRequest{Channel: channel, ThreadTS: thread, Text: text, Blocks: blocks, UnfurlLinks: false, UnfurlMedia: false})
}

// Update only the blocks. Slack retains the original fallback text and other
// message content; no user-supplied response_url is used for this operation.
func (c APIClient) UpdateMessageBlocks(ctx context.Context, token, channel, ts string, blocks []map[string]any) error {
	body, err := json.Marshal(map[string]any{"channel": channel, "ts": ts, "blocks": blocks})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://slack.com/api/chat.update", strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	client := *c.httpClient()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		return errors.New("Slack message update unavailable")
	}
	defer resp.Body.Close()
	var result postMessageResponse
	if json.NewDecoder(resp.Body).Decode(&result) != nil || resp.StatusCode != http.StatusOK || !result.OK {
		return errors.New("Slack message update failed")
	}
	return nil
}
