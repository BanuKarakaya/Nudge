package feedbin

import (
	"context"
	"fmt"
	"net/http"
)

const authenticationURL = "https://api.feedbin.com/v2/authentication.json"

type Client struct {
	HTTPClient *http.Client
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
