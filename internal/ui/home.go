package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/danielmichaels/calendar-digest/internal/digest"
	"github.com/danielmichaels/calendar-digest/internal/store"
	"github.com/danielmichaels/calendar-digest/internal/ui/templates"
)

// undeliveredAfter is how long a snapshot may sit with no notified_at before
// the overview calls it a failure.
//
// A SendJob backs off for about seventeen hours across its twelve attempts, so
// this is not "delivery has given up" — it is "delivery is late enough that a
// person should look". Warning any sooner would flag every healthy run for the
// few seconds between capturing a digest and sending it.
const undeliveredAfter = time.Hour

func (h *Handlers) handleHome(w http.ResponseWriter, r *http.Request) {
	view, err := h.homeView(r.Context())
	if err != nil {
		h.serverError(w, r, err, "build home view")
		return
	}
	view.Title = "calendar digest"
	view.Flash = h.Sessions.PopString(r.Context(), flashKey)

	if err := templates.Home(view).Render(r.Context(), w); err != nil {
		h.serverError(w, r, err, "render home")
	}
}

func (h *Handlers) homeView(ctx context.Context) (templates.HomeView, error) {
	now := h.now()

	recipients, err := h.Db.ListRecipients(ctx)
	if err != nil {
		return templates.HomeView{}, fmt.Errorf("ui: list recipients: %w", err)
	}
	targets, err := h.Db.ListAllTargets(ctx)
	if err != nil {
		return templates.HomeView{}, fmt.Errorf("ui: list targets: %w", err)
	}
	latest, err := h.Db.ListLatestSnapshots(ctx)
	if err != nil {
		return templates.HomeView{}, fmt.Errorf("ui: list latest snapshots: %w", err)
	}
	stale, err := h.Db.ListUnnotifiedSnapshotsBefore(ctx,
		store.FormatTime(now.Add(-undeliveredAfter)))
	if err != nil {
		return templates.HomeView{}, fmt.Errorf("ui: list undelivered snapshots: %w", err)
	}

	byRecipient := make(map[int64][]store.NotificationTargets, len(recipients))
	for _, t := range targets {
		byRecipient[t.RecipientID] = append(byRecipient[t.RecipientID], t)
	}
	lastFor := make(map[int64]store.ListLatestSnapshotsRow, len(latest))
	for _, s := range latest {
		lastFor[s.RecipientID] = s
	}
	names := make(map[int64]string, len(recipients))
	for _, r := range recipients {
		names[r.ID] = r.Name
	}

	view := templates.HomeView{BaseURL: h.Conf.AppConf.BaseURL}
	for _, r := range recipients {
		view.Recipients = append(view.Recipients,
			h.recipientRow(now, r, byRecipient[r.ID], lastFor[r.ID]))
	}
	for _, s := range stale {
		view.Undelivered = append(view.Undelivered, templates.UndeliveredRow{
			RecipientName: names[s.RecipientID],
			DigestDate:    s.DigestDate,
			Age:           age(now, s.CreatedAt),
			URL:           h.detailURL(s.Token),
		})
	}
	return view, nil
}

func (h *Handlers) recipientRow(
	now time.Time,
	r store.Recipients,
	targets []store.NotificationTargets,
	last store.ListLatestSnapshotsRow,
) templates.RecipientRow {
	row := templates.RecipientRow{
		ID:       r.ID,
		Name:     r.Name,
		Schedule: r.NotifyTime + " " + r.Tz,
		Enabled:  r.Enabled,
	}

	if next, err := digest.NextRun(now, r); err != nil {
		// Reported, never fatal, and never blank: a recipient whose schedule
		// cannot be read stops receiving digests forever and looks from the
		// outside exactly like somebody with an empty calendar.
		row.ScheduleProblem = scheduleProblem(r)
	} else {
		row.NextRun = next.Format("Mon 2 Jan 2006, 15:04 MST")
	}

	for _, t := range targets {
		row.Targets = append(row.Targets, templates.TargetRow{
			ID:      t.ID,
			Kind:    t.Kind,
			Address: targetAddress(t.Kind, t.Config),
			Enabled: t.Enabled,
		})
	}

	if last.DigestDate != "" {
		row.LastDigest = last.DigestDate
		row.LastDigestURL = h.detailURL(last.Token)
		row.Delivered = last.NotifiedAt.Valid
	}
	return row
}

// tzProblem and notifyTimeProblem return the empty string when the value is
// usable.
//
// They re-derive rather than printing NextRun's error: that error is wrapped
// for a log — package prefixes, recipient ids — and these are the sentences
// somebody reads while editing the field. The form uses them to refuse a
// schedule that would silently never fire; the overview uses them to explain
// one that already does not.
func tzProblem(tz string) string {
	if tz == "" {
		return "A timezone is required: it decides which day the digest covers."
	}
	if _, err := time.LoadLocation(tz); err != nil {
		return fmt.Sprintf("%q is not a timezone this server knows.", tz)
	}
	return ""
}

func notifyTimeProblem(notifyTime string) string {
	if _, err := time.Parse("15:04", notifyTime); err != nil {
		return fmt.Sprintf("%q is not a notify time; it has to be HH:MM on a 24-hour clock.", notifyTime)
	}
	return ""
}

func scheduleProblem(r store.Recipients) string {
	if problem := tzProblem(r.Tz); problem != "" {
		return problem
	}
	return notifyTimeProblem(r.NotifyTime)
}

// targetAddress pulls the human-readable part out of a target's config so the
// overview can say which chat or address a channel points at.
//
// A config that will not decode returns the raw JSON rather than nothing: the
// point of showing it is to spot the one that is wrong.
func targetAddress(kind, config string) string {
	var cfg struct {
		ChatID  string `json:"chat_id"`
		Address string `json:"address"`
		Phone   string `json:"phone"`
	}
	if err := json.Unmarshal([]byte(config), &cfg); err != nil {
		return config
	}
	switch kind {
	case "telegram":
		return cfg.ChatID
	case "email":
		return cfg.Address
	case "sms":
		return cfg.Phone
	default:
		return config
	}
}

// detailURL builds the same link the notifications carry, so the operator
// opens exactly what the recipient was sent. Empty BASE_URL yields a
// site-relative path, which still works from a browser already on this host.
func (h *Handlers) detailURL(token string) string {
	if token == "" {
		return ""
	}
	return h.Conf.AppConf.BaseURL + "/d/" + token
}

// age renders how long ago a stored timestamp was, at the coarseness a person
// reading an alert cares about.
func age(now time.Time, stored string) string {
	at, err := store.ParseTime(stored)
	if err != nil {
		return ""
	}
	d := now.Sub(at)
	switch {
	case d < 2*time.Hour:
		return fmt.Sprintf("%d minutes", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d hours", int(d.Hours()))
	default:
		return fmt.Sprintf("%d days", int(d.Hours()/24))
	}
}
