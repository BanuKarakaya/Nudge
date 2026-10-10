package feedbin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	apiBaseURL        = "https://api.feedbin.com/v2"
	authenticationURL = apiBaseURL + "/authentication.json"
)

type Entry struct {
	ID                  int64     `json:"id"`
	FeedID              int64     `json:"feed_id"`
	Title               string    `json:"title"`
	URL                 string    `json:"url"`
	Content             string    `json:"content"`
	Summary             string    `json:"summary"`
	ExtractedContentURL string    `json:"extracted_content_url"`
	CreatedAt           time.Time `json:"created_at"`
}

type Tagging struct {
	FeedID int64  `json:"feed_id"`
	Name   string `json:"name"`
}

type Client struct {
	HTTPClient *http.Client
}

func (c Client) ListUnreadSince(ctx context.Context, email, password string, since time.Time) ([]Entry, error) {
	ids, err := c.getIDs(ctx, email, password)
	if err != nil || len(ids) == 0 {
		return nil, err
	}
	var entries []Entry
	for start := 0; start < len(ids); start += 100 {
		end := start + 100
		if end > len(ids) {
			end = len(ids)
		}
		params := url.Values{"ids": {joinIDs(ids[start:end])}}
		var page []Entry
		if err := c.getJSON(ctx, email, password, apiBaseURL+"/entries.json?"+params.Encode(), &page); err != nil {
			return nil, err
		}
		for _, entry := range page {
			if !entry.CreatedAt.Before(since) {
				entries = append(entries, entry)
			}
		}
	}
	return entries, nil
}

func (c Client) ListTaggings(ctx context.Context, email, password string) ([]Tagging, error) {
	var taggings []Tagging
	if err := c.getJSON(ctx, email, password, apiBaseURL+"/taggings.json", &taggings); err != nil {
		return nil, err
	}
	return taggings, nil
}

func (c Client) getIDs(ctx context.Context, email, password string) ([]int64, error) {
	var ids []int64
	if err := c.getJSON(ctx, email, password, apiBaseURL+"/unread_entries.json", &ids); err != nil {
		return nil, err
	}
	return ids, nil
}

func (c Client) getJSON(ctx context.Context, email, password, endpoint string, target any) error {
	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth(email, password)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("Feedbin API returned HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(target)
}

func joinIDs(ids []int64) string {
	values := make([]string, len(ids))
	for i, id := range ids {
		values[i] = strconv.FormatInt(id, 10)
	}
	return strings.Join(values, ",")
}

func (c Client) ValidateCredentials(ctx context.Context, email, password string) error {
	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, authenticationURL, nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth(email, password)

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("Feedbin email or password is incorrect")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("Feedbin credential check returned HTTP %d", resp.StatusCode)
	}
	return nil
}
