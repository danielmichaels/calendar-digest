// Package notify carries messages out of the process. It knows how to reach a
// channel; it does not know what a digest is or when one is due.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// telegramAPI is the Bot API root. The bot token sits in the path of every
// request to it, which is why no URL built here is ever put in an error.
const telegramAPI = "https://api.telegram.org"

// Telegram posts messages through the Bot API.
type Telegram struct {
	Token string
	// BaseURL overrides the API root for tests. Empty means the real one.
	BaseURL string
	// HTTP is the client used for requests. Nil gets one with a timeout, since
	// the default http.Client has none and a stuck send would hold a worker
	// until River's own job timeout fired.
	HTTP *http.Client
}

func (t *Telegram) SendMessage(ctx context.Context, chatID, text string) error {
	payload, err := json.Marshal(map[string]string{"chat_id": chatID, "text": text})
	if err != nil {
		return fmt.Errorf("notify: telegram: encode message: %w", err)
	}

	base := t.BaseURL
	if base == "" {
		base = telegramAPI
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("%s/bot%s/sendMessage", base, t.Token), bytes.NewReader(payload))
	if err != nil {
		// Deliberately not %w: the URL carries the bot token and this error is
		// the one shape that would print it.
		return fmt.Errorf("notify: telegram: build request")
	}
	req.Header.Set("Content-Type", "application/json")

	client := t.HTTP
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("notify: telegram: send: %w", redactToken(err, t.Token))
	}
	defer func() { _ = resp.Body.Close() }()

	// Telegram reports application-level failure in the body, and not always
	// with a non-2xx status, so the status alone is not enough to trust.
	var result struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	if err != nil {
		return fmt.Errorf("notify: telegram: read response: %w", err)
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("notify: telegram: HTTP %d with an undecodable body", resp.StatusCode)
	}
	if !result.OK {
		return fmt.Errorf("notify: telegram: HTTP %d: %s", resp.StatusCode, result.Description)
	}
	return nil
}

// TelegramAlerter tells one chat when something needs a person rather than
// another retry.
type TelegramAlerter struct {
	Bot    *Telegram
	ChatID string
}

func (a *TelegramAlerter) Alert(ctx context.Context, subject, detail string) error {
	return a.Bot.SendMessage(ctx, a.ChatID, subject+"\n\n"+detail)
}

// redactToken keeps the bot token out of transport errors, which quote the URL
// they failed on.
func redactToken(err error, token string) error {
	if token == "" {
		return err
	}
	msg := err.Error()
	redacted := strings.ReplaceAll(msg, token, "REDACTED")
	if redacted == msg {
		return err
	}
	return errors.New(redacted)
}
