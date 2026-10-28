package slack

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

// ThreadMessage preserves authorship and code formatting without fetching files
// or resolving links embedded in someone else's message.
type ThreadMessage struct {
	TS       string `json:"ts"`
	ThreadTS string `json:"thread_ts,omitempty"`
	User     string `json:"user,omitempty"`
	BotID    string `json:"bot_id,omitempty"`
	Text     string `json:"text"`
}

type ThreadHistory struct {
	Messages  []ThreadMessage `json:"messages"`
	Truncated bool            `json:"truncated"`
}

// ThreadHistory reads only the mentioned thread, strictly before the triggering
// message. Bound work and memory; retain the root and most recent fetched replies.
// Errors may accompany partial history and must not be mistaken for an empty thread.
func (c APIClient) ThreadHistory(ctx context.Context, token, channel, threadTS, beforeTS string) (ThreadHistory, error) {
	var history ThreadHistory
	if token == "" || channel == "" || threadTS == "" || beforeTS == "" {
		return history, fmt.Errorf("Slack thread history requires token, channel and timestamps")
	}
	cursor := ""
	seenCursors := make(map[string]bool)
	seenMessages := make(map[string]bool)
	for page := 0; page < 20; page++ {
		query := url.Values{"channel": {channel}, "ts": {threadTS}, "latest": {beforeTS}, "inclusive": {"false"}, "limit": {"100"}}
		if cursor != "" {
			query.Set("cursor", cursor)
		}
		var result struct {
			OK       bool            `json:"ok"`
			Error    string          `json:"error"`
			Messages []ThreadMessage `json:"messages"`
			HasMore  bool            `json:"has_more"`
			Metadata struct {
				NextCursor string `json:"next_cursor"`
			} `json:"response_metadata"`
		}
		err := func() error {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://slack.com/api/conversations.replies?"+query.Encode(), nil)
			if err != nil {
				return err
			}
			req.Header.Set("Authorization", "Bearer "+token)
			resp, err := c.httpClient().Do(req)
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				return fmt.Errorf("Slack conversations.replies returned HTTP %d", resp.StatusCode)
			}
			if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&result); err != nil {
				return err
			}
			if !result.OK {
				return fmt.Errorf("Slack conversations.replies failed: %s", result.Error)
			}
			return nil
		}()
		if err != nil {
			history.Truncated = true
			return history, err
		}
		for _, message := range result.Messages {
			if message.TS == "" || !TimestampBefore(message.TS, beforeTS) || seenMessages[message.TS] ||
				(message.TS != threadTS && message.ThreadTS != threadTS) {
				continue
			}
			seenMessages[message.TS] = true
			history.Messages = append(history.Messages, message)
		}
		sort.Slice(history.Messages, func(i, j int) bool { return TimestampBefore(history.Messages[i].TS, history.Messages[j].TS) })
		if len(history.Messages) > 100 {
			start := len(history.Messages) - 100
			if history.Messages[0].TS == threadTS {
				history.Messages = append(history.Messages[:1], history.Messages[start+1:]...)
			} else {
				history.Messages = history.Messages[start:]
			}
			history.Truncated = true
		}
		cursor = strings.TrimSpace(result.Metadata.NextCursor)
		if cursor == "" {
			if result.HasMore {
				history.Truncated = true
				return history, fmt.Errorf("Slack thread history pagination cursor missing")
			}
			return history, nil
		}
		if seenCursors[cursor] {
			history.Truncated = true
			return history, fmt.Errorf("Slack thread history pagination cursor repeated")
		}
		seenCursors[cursor] = true
	}
	history.Truncated = true
	return history, fmt.Errorf("Slack thread history page limit reached")
}

// Compare decimal Slack timestamps without floating-point precision loss.
func TimestampBefore(a, b string) bool {
	as, af, _ := strings.Cut(a, ".")
	bs, bf, _ := strings.Cut(b, ".")
	if len(as) != len(bs) {
		return len(as) < len(bs)
	}
	if as != bs {
		return as < bs
	}
	width := max(len(af), len(bf))
	return af+strings.Repeat("0", width-len(af)) < bf+strings.Repeat("0", width-len(bf))
}
