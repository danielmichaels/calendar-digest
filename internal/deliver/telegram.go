package deliver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/danielmichaels/calendar-digest/internal/digest"
	"github.com/danielmichaels/calendar-digest/internal/notify"
)

// TelegramRenderer writes the message body Telegram sends.
//
// Plain text with no parse_mode: Telegram's MarkdownV2 requires escaping a long
// list of punctuation, and an event summary is arbitrary text from someone
// else's calendar. An unescaped bracket would fail the send rather than render
// oddly.
type TelegramRenderer struct {
	// BaseURL is the root of the detail link. Empty omits the link.
	BaseURL string
}

func (r TelegramRenderer) Render(d digest.Digest) string {
	var b strings.Builder

	b.WriteString(d.RecipientName + " — " + headline(d.Date) + "\n\n")

	if len(d.Events) == 0 {
		b.WriteString("Nothing on.\n")
	}
	for _, ev := range d.Events {
		b.WriteString(timeRange(ev) + "  " + ev.Summary)
		if ev.Location != "" {
			b.WriteString(" — " + ev.Location)
		}
		if ev.Status == "tentative" {
			b.WriteString(" (tentative)")
		}
		b.WriteString("\n")
	}

	if url := detailURL(r.BaseURL, d.Token); url != "" {
		b.WriteString("\n" + url + "\n")
	}
	return b.String()
}

// telegramTarget is notification_targets.config for a telegram row.
type telegramTarget struct {
	ChatID string `json:"chat_id"`
}

// TelegramNotifier delivers a digest to one chat.
//
// It is a renderer on top of the transport built for operator alerts, not a
// second HTTP client: that one already treats ok:false at HTTP 200 as failure
// and keeps the bot token out of its errors, and both matter just as much here.
type TelegramNotifier struct {
	Bot      *notify.Telegram
	Renderer TelegramRenderer
}

func (n *TelegramNotifier) Kind() string { return "telegram" }

func (n *TelegramNotifier) Send(
	ctx context.Context,
	target json.RawMessage,
	d digest.Digest,
) (string, error) {
	var cfg telegramTarget
	if err := decodeTarget(target, &cfg, func() string { return cfg.ChatID }, "chat_id"); err != nil {
		return "", err
	}

	body := n.Renderer.Render(d)
	if err := n.Bot.SendMessage(ctx, cfg.ChatID, body); err != nil {
		return "", fmt.Errorf("deliver: telegram: %w", err)
	}
	return body, nil
}
