package deliver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/danielmichaels/calendar-digest/internal/calendar"
	"github.com/danielmichaels/calendar-digest/internal/digest"
)

// ErrNotImplemented is what the SMS channel returns instead of delivering.
//
// It is deliberately not nil: a nil error is what sets notified_at, and
// recording a digest as delivered because the channel that was meant to carry
// it does not exist yet is the one outcome worse than not sending it.
var ErrNotImplemented = errors.New("deliver: sms: not implemented")

// gsm7Basic is the GSM 03.38 alphabet, one septet per character.
const gsm7Basic = "@£$¥èéùìòÇ\nØø\rÅåΔ_ΦΓΛΩΠΨΣΘΞÆæßÉ !\"#¤%&'()*+,-./0123456789:;<=>?¡" +
	"ABCDEFGHIJKLMNOPQRSTUVWXYZÄÖÑÜ§¿abcdefghijklmnopqrstuvwxyzäöñüà"

// gsm7Extended costs two septets: an escape, then the character.
const gsm7Extended = "^{}\\[~]|€"

// SMSSegments reports how many SMS segments text occupies.
//
// One character outside GSM-7 re-encodes the whole message as UCS-2 and drops
// the single-segment budget from 160 characters to 70 — so an en dash borrowed
// from the Telegram format costs more than half the message, invisibly.
// Concatenated segments are shorter still, because each carries a header.
func SMSSegments(text string) int {
	if text == "" {
		return 0
	}

	septets, gsm := 0, true
	for _, r := range text {
		switch {
		case strings.ContainsRune(gsm7Basic, r):
			septets++
		case strings.ContainsRune(gsm7Extended, r):
			septets += 2
		default:
			gsm = false
		}
	}
	if !gsm {
		// UCS-2 counts UTF-16 code units, so anything above the BMP is two.
		units := 0
		for _, r := range text {
			units++
			if r > 0xFFFF {
				units++
			}
		}
		return segmentsFor(units, 70, 67)
	}
	return segmentsFor(septets, 160, 153)
}

func segmentsFor(length, single, concatenated int) int {
	if length <= single {
		return 1
	}
	return (length + concatenated - 1) / concatenated
}

// gsmReplacer maps the typography the other renderers use onto characters
// GSM-7 can carry. Every one of these would otherwise re-encode the entire
// message to UCS-2 and more than halve the budget.
var gsmReplacer = strings.NewReplacer(
	"–", "-", "—", "-", "‑", "-",
	"‘", "'", "’", "'", "“", `"`, "”", `"`,
	"…", "...", "•", "*", "→", "->",
	" ", " ",
)

// gsmFold reduces text to one line of GSM-7.
//
// Characters with no GSM-7 equivalent are dropped rather than carried, because
// carrying one costs more than every event it appears alongside. Text that is
// entirely outside GSM-7 — a summary in a script it cannot represent — is kept
// as it stands and left for the fitter to pay for, since returning nothing
// would silently delete the event.
func gsmFold(s string) string {
	var b strings.Builder
	for _, r := range gsmReplacer.Replace(s) {
		if strings.ContainsRune(gsm7Basic, r) || strings.ContainsRune(gsm7Extended, r) {
			b.WriteRune(r)
		}
	}
	if folded := strings.Join(strings.Fields(b.String()), " "); folded != "" {
		return folded
	}
	return strings.Join(strings.Fields(s), " ")
}

// SMSRenderer compresses a day into a message short enough to arrive as one
// SMS.
//
// Nothing here is guaranteed to fit by construction: summaries are arbitrary
// text from someone else's calendar, so the renderer builds the longest
// candidate, measures it, and names fewer events until one segment holds it.
// What is dropped is recoverable from the link, which is why the link is the
// last thing to go.
//
// Location, tentative status and end times are all absent deliberately. At
// roughly a hundred characters after the URL, naming the events at all is
// worth more than qualifying any of them.
type SMSRenderer struct {
	// BaseURL is the root of the detail link. Empty omits the link.
	BaseURL string
}

func (r SMSRenderer) Render(d digest.Digest) string {
	url := detailURL(r.BaseURL, d.Token)
	prefix := gsmFold(shortDate(d.Date)) + ": "

	if len(d.Events) == 0 {
		return withLink(prefix+"nothing on", url)
	}

	entries := make([]string, 0, len(d.Events))
	for _, ev := range d.Events {
		entries = append(entries, gsmFold(smsEntry(ev)))
	}

	// Longest first, then one fewer named event each pass. Counting down rather
	// than estimating a character budget is what makes this correct for text
	// that forces UCS-2: the encoding is only known once the message exists.
	for named := len(entries); named > 0; named-- {
		body := prefix + strings.Join(entries[:named], ", ")
		if dropped := len(entries) - named; dropped > 0 {
			body += fmt.Sprintf(" +%d more", dropped)
		}
		if candidate := withLink(body, url); SMSSegments(candidate) <= 1 {
			return candidate
		}
	}
	// Not even one event fits beside the link. The count still says the day is
	// busy, and the link still says how.
	return withLink(prefix+eventCount(d), url)
}

// smsEntry is one event at its shortest useful length.
//
// An all-day event carries no time because its Start is a midnight boundary
// rather than an instant — and "00:00" would be both wrong and the most
// expensive five characters in the message.
func smsEntry(ev calendar.Event) string {
	if ev.AllDay {
		return ev.Summary
	}
	return ev.Start.Format("15:04") + " " + ev.Summary
}

// shortDate is the date at SMS length: "Wed 5 Aug".
func shortDate(date string) string {
	d, err := time.Parse(time.DateOnly, date)
	if err != nil {
		return date
	}
	return d.Format("Mon 2 Jan")
}

func withLink(body, url string) string {
	if url == "" {
		return body
	}
	return body + "\n" + url
}

// smsTarget is notification_targets.config for an sms row.
type smsTarget struct {
	Phone string `json:"phone"`
}

// SMSWebhookPayload is the JSON body the webhook will receive when SMS is
// implemented, per grill Q14. It is exported because it is the contract the
// README documents and whatever runs at the other end has to match.
type SMSWebhookPayload struct {
	Phone string `json:"phone"`
	Text  string `json:"text"`
	// URL is the detail page, sent alongside the text rather than only inside
	// it, so a gateway that builds its own message body still has the link.
	URL string `json:"url,omitempty"`
}

// SMSNotifier renders what it would have sent, logs it, and refuses.
//
// It exists so an sms target is a visible unimplemented channel rather than a
// silent one: without it the kind has no entry in the notifier map and every
// send fails with jobs.ErrNoNotifier, which says nothing about the payload the
// webhook will eventually need to accept.
type SMSNotifier struct {
	Renderer SMSRenderer
	Log      *slog.Logger
}

func (n *SMSNotifier) Kind() string { return "sms" }

func (n *SMSNotifier) Send(
	ctx context.Context,
	target json.RawMessage,
	d digest.Digest,
) (string, error) {
	var cfg smsTarget
	if err := decodeTarget(target, &cfg, func() string { return cfg.Phone }, "phone"); err != nil {
		return "", err
	}

	text := n.Renderer.Render(d)
	payload := SMSWebhookPayload{
		Phone: cfg.Phone,
		Text:  text,
		URL:   detailURL(n.Renderer.BaseURL, d.Token),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("deliver: sms: encode payload: %w", err)
	}

	n.log().Warn("sms is not implemented: this is the payload that would have been posted",
		"payload", string(body),
		"segments", SMSSegments(text))
	return "", ErrNotImplemented
}

func (n *SMSNotifier) log() *slog.Logger {
	if n.Log != nil {
		return n.Log
	}
	return slog.Default()
}
