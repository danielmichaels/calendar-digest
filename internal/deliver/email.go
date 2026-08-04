package deliver

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"strings"

	"github.com/danielmichaels/calendar-digest/internal/calendar"
	"github.com/danielmichaels/calendar-digest/internal/digest"
)

// descriptionLimit bounds how much of an event's notes reach an inbox. The
// whole description is on the detail page; this is the part that earns its
// place in a message meant to be read at a glance.
const descriptionLimit = 200

// EmailRenderer writes the subject and both body parts of the digest email.
//
// Two parts rather than one because the choice was multipart/alternative: a
// client that will not render HTML gets a message written for it, not a tag
// soup fallback.
type EmailRenderer struct {
	// BaseURL is the root of the detail link. Empty omits the link.
	BaseURL string
}

// emailEvent is one row as both bodies see it.
//
// It exists so that what counts as "detail" is decided once. Adding a field to
// the email means changing this and the two templates below, rather than
// discovering months later that the text part has been quietly poorer than the
// HTML one.
type emailEvent struct {
	Time          string
	Summary       string
	Tentative     bool
	Recurring     bool
	Location      string
	Description   string
	ConferenceURL string
	Organizer     string
	With          string
}

type emailView struct {
	Headline  string
	Count     string
	Events    []emailEvent
	DetailURL string
}

// Render returns the subject and the plain-text and HTML bodies.
//
// The error can only come from executing the HTML template, which is fixed at
// build time — it is returned rather than dropped because a body that failed
// to render must not be sent as an empty email.
func (r EmailRenderer) Render(d digest.Digest) (subject, text, htmlBody string, err error) {
	view := emailView{
		Headline:  headline(d.Date),
		Count:     eventCount(d),
		DetailURL: detailURL(r.BaseURL, d.Token),
	}
	for _, ev := range d.Events {
		view.Events = append(view.Events, emailEvent{
			Time:          timeRange(ev),
			Summary:       ev.Summary,
			Tentative:     ev.Status == "tentative",
			Recurring:     ev.Recurring,
			Location:      ev.Location,
			Description:   condense(ev.Description, descriptionLimit),
			ConferenceURL: ev.ConferenceURL,
			Organizer:     ev.Organizer,
			With:          otherAttendees(ev),
		})
	}

	subject = fmt.Sprintf("%s — %s: %s", d.RecipientName, view.Headline, view.Count)

	var html strings.Builder
	if err := emailHTML.Execute(&html, view); err != nil {
		return "", "", "", fmt.Errorf("deliver: email: render html: %w", err)
	}
	return subject, renderEmailText(view), html.String(), nil
}

func renderEmailText(view emailView) string {
	var b strings.Builder
	b.WriteString(view.Headline + " — " + view.Count + "\n")

	for _, ev := range view.Events {
		b.WriteString("\n" + ev.Time + "  " + ev.Summary)
		if ev.Tentative {
			b.WriteString(" (tentative)")
		}
		b.WriteString("\n")
		for _, line := range detailLines(ev) {
			b.WriteString("    " + line + "\n")
		}
	}

	if view.DetailURL != "" {
		b.WriteString("\nFull detail: " + view.DetailURL + "\n")
	}
	return b.String()
}

// detailLines are the indented lines under an event in the text part, in the
// order they earn their space: where, what, how to join, who.
func detailLines(ev emailEvent) []string {
	var lines []string
	if ev.Location != "" {
		lines = append(lines, ev.Location)
	}
	if ev.Description != "" {
		lines = append(lines, ev.Description)
	}
	if ev.ConferenceURL != "" {
		lines = append(lines, "Join: "+ev.ConferenceURL)
	}
	if ev.With != "" {
		lines = append(lines, "With: "+ev.With)
	}
	if ev.Organizer != "" {
		lines = append(lines, "Organiser: "+ev.Organizer)
	}
	if ev.Recurring {
		lines = append(lines, "Repeats")
	}
	return lines
}

// otherAttendees names everyone but the recipient, who does not need telling
// they are on their own calendar.
func otherAttendees(ev calendar.Event) string {
	var names []string
	for _, a := range ev.Attendees {
		if a.Self {
			continue
		}
		name := a.DisplayName
		if name == "" {
			name = a.Email
		}
		names = append(names, name)
	}
	return strings.Join(names, ", ")
}

// condense flattens an event description onto one line and caps it.
//
// Calendar descriptions arrive with hard newlines, signatures and dial-in
// boilerplate, any of which would swamp the event it belongs to. Truncation is
// by rune so a multi-byte character is never cut in half.
func condense(s string, limit int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > limit {
		return strings.TrimSpace(string(r[:limit])) + "…"
	}
	return s
}

// EmailSender puts one already-rendered message on the wire.
//
// Narrow on purpose: it is the whole of what EmailNotifier needs, so the
// notifier's tests never speak SMTP, and a second transport — a hosted API,
// say — arrives as another implementation rather than a branch in the notifier.
type EmailSender interface {
	Send(ctx context.Context, to, subject, text, html string) error
}

// emailTarget is notification_targets.config for an email row.
type emailTarget struct {
	Address string `json:"address"`
}

type EmailNotifier struct {
	Sender   EmailSender
	Renderer EmailRenderer
}

func (n *EmailNotifier) Kind() string { return "email" }

func (n *EmailNotifier) Send(
	ctx context.Context,
	target json.RawMessage,
	d digest.Digest,
) (string, error) {
	var cfg emailTarget
	if err := decodeTarget(target, &cfg, func() string { return cfg.Address }, "address"); err != nil {
		return "", err
	}

	subject, text, html, err := n.Renderer.Render(d)
	if err != nil {
		return "", err
	}
	if err := n.Sender.Send(ctx, cfg.Address, subject, text, html); err != nil {
		return "", fmt.Errorf("deliver: email: %w", err)
	}
	// The text part, because it is the one a person reading a log wants.
	return text, nil
}

// emailHTML is the alternative part. Styling is inline because email clients
// discard <style> blocks, and the layout is a table for the same reason.
var emailHTML = template.Must(template.New("email").Parse(
	`<div style="font-family:-apple-system,Segoe UI,Roboto,Helvetica,Arial,sans-serif;font-size:15px;line-height:1.5;color:#1a1a1a;max-width:640px">
<h1 style="font-size:19px;font-weight:600;margin:0 0 2px">{{.Headline}}</h1>
<p style="margin:0 0 20px;color:#6b6b6b;font-size:13px">{{.Count}}</p>
{{- if .Events}}
<table style="border-collapse:collapse;width:100%">
{{- range .Events}}
<tr>
<td style="padding:0 14px 18px 0;vertical-align:top;white-space:nowrap;color:#6b6b6b;font-variant-numeric:tabular-nums">{{.Time}}</td>
<td style="padding:0 0 18px;vertical-align:top">
<strong style="font-weight:600">{{.Summary}}</strong>{{if .Tentative}} <span style="color:#9a6b00;font-size:13px">(tentative)</span>{{end}}
{{- if .Location}}<div style="color:#4a4a4a">{{.Location}}</div>{{end}}
{{- if .Description}}<div style="color:#6b6b6b;font-size:13px;margin-top:2px">{{.Description}}</div>{{end}}
{{- if .ConferenceURL}}<div style="margin-top:2px"><a href="{{.ConferenceURL}}" style="color:#1a5fb4">Join</a></div>{{end}}
{{- if .With}}<div style="color:#6b6b6b;font-size:13px;margin-top:2px">With: {{.With}}</div>{{end}}
{{- if .Organizer}}<div style="color:#6b6b6b;font-size:13px">Organiser: {{.Organizer}}</div>{{end}}
{{- if .Recurring}}<div style="color:#6b6b6b;font-size:13px">Repeats</div>{{end}}
</td>
</tr>
{{- end}}
</table>
{{- end}}
{{- if .DetailURL}}
<p style="margin:20px 0 0"><a href="{{.DetailURL}}" style="color:#1a5fb4">Full detail</a></p>
{{- end}}
</div>
`))
