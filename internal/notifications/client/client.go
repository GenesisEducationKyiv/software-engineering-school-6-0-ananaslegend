// Package client is the monolith-side HTTP client for the notifications
// service. It implements notifier.NotificationsSender and confirmer.NotificationsSender.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/ananaslegend/reposeetory/internal/notifications/contract"
)

type Client struct {
	baseURL string
	http    *http.Client
}

func New(baseURL string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{baseURL: baseURL, http: httpClient}
}

func (c *Client) SendRelease(ctx context.Context, p contract.SendReleaseRequest) error {
	if err := c.post(ctx, "/v1/notifications/release", p); err != nil {
		return fmt.Errorf("client.Client.SendRelease: %w", err)
	}
	return nil
}

func (c *Client) SendConfirmation(ctx context.Context, p contract.SendConfirmationRequest) error {
	if err := c.post(ctx, "/v1/notifications/confirmation", p); err != nil {
		return fmt.Errorf("client.Client.SendConfirmation: %w", err)
	}
	return nil
}

func (c *Client) post(ctx context.Context, path string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("client.Client.post: json.Marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("client.Client.post: http.NewRequestWithContext: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("client.Client.post: http.Client.Do: %w", err) // transient
	}
	defer resp.Body.Close() //nolint:errcheck

	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return nil
	case resp.StatusCode >= 400 && resp.StatusCode < 500:
		return fmt.Errorf("client.Client.post: status %d: %w", resp.StatusCode, contract.ErrPermanent)
	default:
		return fmt.Errorf("client.Client.post: status %d", resp.StatusCode) // transient
	}
}
