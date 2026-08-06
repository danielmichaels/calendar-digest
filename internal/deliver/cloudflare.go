package deliver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// CloudflareSender delivers through Cloudflare Email Service's REST API.
// APIURL is injectable for tests and defaults to Cloudflare's v4 API root.
type CloudflareSender struct {
	AccountID string
	APIToken  string
	APIURL    string
	From      string
	Client    *http.Client
}

type cloudflareEmailRequest struct {
	To      string `json:"to"`
	From    string `json:"from"`
	Subject string `json:"subject"`
	Text    string `json:"text"`
	HTML    string `json:"html"`
}

type cloudflareEmailResponse struct {
	Success bool `json:"success"`
	Errors  []struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"errors"`
	Result struct {
		PermanentBounces []string `json:"permanent_bounces"`
	} `json:"result"`
}

func (s *CloudflareSender) Send(ctx context.Context, to, subject, text, html string) error {
	payload, err := json.Marshal(cloudflareEmailRequest{
		To: to, From: s.From, Subject: subject, Text: text, HTML: html,
	})
	if err != nil {
		return fmt.Errorf("deliver: cloudflare: encode request: %w", err)
	}

	url := strings.TrimRight(s.APIURL, "/") + "/accounts/" + s.AccountID + "/email/sending/send"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("deliver: cloudflare: create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+s.APIToken)
	req.Header.Set("Content-Type", "application/json")

	client := s.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("deliver: cloudflare: request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("deliver: cloudflare: read response: %w", err)
	}
	var result cloudflareEmailResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("deliver: cloudflare: decode response (HTTP %d): %w", resp.StatusCode, err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices || !result.Success {
		if len(result.Errors) > 0 {
			return fmt.Errorf("deliver: cloudflare: send to %q: %d %s", to, result.Errors[0].Code, result.Errors[0].Message)
		}
		return fmt.Errorf("deliver: cloudflare: send to %q: HTTP %d", to, resp.StatusCode)
	}
	for _, bounced := range result.Result.PermanentBounces {
		if bounced == to {
			return fmt.Errorf("deliver: cloudflare: recipient %q permanently bounced", to)
		}
	}
	return nil
}
