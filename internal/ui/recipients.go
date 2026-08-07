package ui

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/danielmichaels/calendar-digest/internal/calendar"
	"github.com/danielmichaels/calendar-digest/internal/digest"
	"github.com/danielmichaels/calendar-digest/internal/jobs"
	"github.com/danielmichaels/calendar-digest/internal/store"
	"github.com/danielmichaels/calendar-digest/internal/ui/templates"

	"github.com/go-chi/chi/v5"
	"github.com/starfederation/datastar-go/datastar"
)

// addressKey names the one config value each kind needs, as the JSON key
// stored in notification_targets.config. Its keys are also the set of kinds a
// target may have, matching the CHECK on notification_targets.kind.
//
// The mapping lives here rather than in the browser so the stored shape is the
// server's to decide: the notifiers read these keys, and a form free to write
// its own would be free to write one they cannot read.
var addressKey = map[string]string{
	"telegram": "chat_id",
	"email":    "address",
	"sms":      "phone",
}

// recipientSignals is what the form sends. Datastar puts every bound signal in
// the request body, so this is the whole editable surface of a recipient.
type recipientSignals struct {
	Name       string `json:"name"`
	CalendarID string `json:"calendar_id"`
	NotifyTime string `json:"notify_time"`
	Tz         string `json:"tz"`
	Enabled    bool   `json:"enabled"`
}

type targetSignals struct {
	Kind    string `json:"kind"`
	Address string `json:"address"`
}

func (h *Handlers) handleRecipientNew(w http.ResponseWriter, r *http.Request) {
	h.renderForm(w, r, templates.RecipientFormView{
		Title: "Add recipient",
		New:   true,
		// Defaults that make the common case a two-field form rather than a
		// four-field one.
		NotifyTime: "21:00",
		Tz:         "Australia/Brisbane",
		Enabled:    true,
	})
}

func (h *Handlers) handleRecipientEdit(w http.ResponseWriter, r *http.Request) {
	recipient, ok := h.recipientFromPath(w, r)
	if !ok {
		return
	}
	view, err := h.editView(r.Context(), recipient)
	if err != nil {
		h.serverError(w, r, err, "build recipient form")
		return
	}
	h.renderForm(w, r, view)
}

func (h *Handlers) handleRecipientDigestNow(w http.ResponseWriter, r *http.Request) {
	recipient, ok := h.recipientFromPath(w, r)
	if !ok {
		return
	}
	sse := datastar.NewSSE(w, r)
	var status string
	statusOK := false

	if h.DigestRunner == nil {
		status = "Calendar capture is not configured on this server."
	} else if loc, err := time.LoadLocation(recipient.Tz); err != nil {
		status = "Cannot determine the recipient's timezone: " + err.Error()
	} else {
		digestDate := h.now().In(loc).AddDate(0, 0, 1).Format(time.DateOnly)
		err := h.DigestRunner.RunDigestNow(r.Context(), jobs.DigestArgs{
			RecipientID: recipient.ID,
			DigestDate:  digestDate,
		})
		if err != nil {
			status = "Calendar access failed: " + err.Error()
		} else {
			status = "Calendar read succeeded for " + digestDate + "; the digest is queued for delivery."
			statusOK = true
		}
	}
	_ = sse.PatchElementTempl(templates.DigestNow(recipient.ID, status, statusOK))
}

func (h *Handlers) editView(
	ctx context.Context,
	recipient store.Recipients,
) (templates.RecipientFormView, error) {
	view := templates.RecipientFormView{
		Title:      recipient.Name,
		ID:         recipient.ID,
		Name:       recipient.Name,
		CalendarID: recipient.CalendarID,
		NotifyTime: recipient.NotifyTime,
		Tz:         recipient.Tz,
		Enabled:    recipient.Enabled,
	}
	targets, err := h.Db.ListTargets(ctx, recipient.ID)
	if err != nil {
		return templates.RecipientFormView{}, fmt.Errorf("ui: list targets: %w", err)
	}
	for _, t := range targets {
		view.Targets = append(view.Targets, targetRow(t))
	}
	return view, nil
}

func targetRow(t store.NotificationTargets) templates.TargetFormRow {
	return templates.TargetFormRow{
		ID:      t.ID,
		Kind:    t.Kind,
		Address: targetAddress(t.Kind, t.Config),
		Enabled: t.Enabled,
	}
}

func (h *Handlers) renderForm(w http.ResponseWriter, r *http.Request, view templates.RecipientFormView) {
	if err := templates.RecipientForm(view).Render(r.Context(), w); err != nil {
		h.serverError(w, r, err, "render recipient form")
	}
}

// problems validates the whole recipient at once, so one save reports
// everything wrong with it rather than one field per attempt.
//
// The schedule fields are checked here above all: a tz or notify_time the due
// check cannot read stops that recipient forever, and the only sign of it is
// silence.
func problems(s recipientSignals) map[string]string {
	found := map[string]string{}
	if s.Name == "" {
		found["name"] = "A name is required."
	}
	if s.CalendarID == "" {
		found["calendar_id"] = "A calendar ID is required: it is the address the service account reads."
	}
	if problem := notifyTimeProblem(s.NotifyTime); problem != "" {
		found["notify_time"] = problem
	}
	if problem := tzProblem(s.Tz); problem != "" {
		found["tz"] = problem
	}
	return found
}

func (h *Handlers) handleRecipientCreate(w http.ResponseWriter, r *http.Request) {
	var signals recipientSignals
	if err := datastar.ReadSignals(r, &signals); err != nil {
		h.badRequest(w, r, err)
		return
	}
	sse := datastar.NewSSE(w, r)

	if found := problems(signals); len(found) > 0 {
		h.patchForm(sse, templates.RecipientFormView{
			Title: "Add recipient", New: true,
			Name: signals.Name, CalendarID: signals.CalendarID,
			NotifyTime: signals.NotifyTime, Tz: signals.Tz, Enabled: signals.Enabled,
			Problems: found,
		})
		return
	}

	recipient, err := h.Db.CreateRecipient(r.Context(), store.CreateRecipientParams{
		Name:       signals.Name,
		CalendarID: signals.CalendarID,
		NotifyTime: signals.NotifyTime,
		Tz:         signals.Tz,
		Enabled:    signals.Enabled,
	})
	if err != nil {
		h.serverError(w, r, err, "create recipient")
		return
	}

	// Straight to the edit page rather than back to the overview: a recipient
	// with no targets receives nothing, and that page is where targets are
	// added.
	h.flash(r, "Recipient added. It has no channels yet, so it will receive nothing.")
	_ = sse.Redirect("/app/recipients/" + strconv.FormatInt(recipient.ID, 10))
}

func (h *Handlers) handleRecipientUpdate(w http.ResponseWriter, r *http.Request) {
	recipient, ok := h.recipientFromPath(w, r)
	if !ok {
		return
	}
	var signals recipientSignals
	if err := datastar.ReadSignals(r, &signals); err != nil {
		h.badRequest(w, r, err)
		return
	}
	sse := datastar.NewSSE(w, r)

	if found := problems(signals); len(found) > 0 {
		view, err := h.editView(r.Context(), recipient)
		if err != nil {
			h.serverError(w, r, err, "build recipient form")
			return
		}
		view.Name, view.CalendarID = signals.Name, signals.CalendarID
		view.NotifyTime, view.Tz, view.Enabled = signals.NotifyTime, signals.Tz, signals.Enabled
		view.Problems = found
		h.patchForm(sse, view)
		return
	}

	if _, err := h.Db.UpdateRecipient(r.Context(), store.UpdateRecipientParams{
		ID:         recipient.ID,
		Name:       signals.Name,
		CalendarID: signals.CalendarID,
		NotifyTime: signals.NotifyTime,
		Tz:         signals.Tz,
		Enabled:    signals.Enabled,
	}); err != nil {
		h.serverError(w, r, err, "update recipient")
		return
	}

	h.flash(r, "Saved.")
	_ = sse.Redirect("/app")
}

func (h *Handlers) handleRecipientDelete(w http.ResponseWriter, r *http.Request) {
	recipient, ok := h.recipientFromPath(w, r)
	if !ok {
		return
	}
	// Targets and snapshots go with it: the schema cascades, and a snapshot
	// belonging to nobody is a page nobody can be sent to.
	if err := h.Db.DeleteRecipient(r.Context(), recipient.ID); err != nil {
		h.serverError(w, r, err, "delete recipient")
		return
	}
	h.flash(r, "Removed "+recipient.Name+", along with their captured digests.")
	_ = datastar.NewSSE(w, r).Redirect("/app")
}

func (h *Handlers) handleTargetCreate(w http.ResponseWriter, r *http.Request) {
	recipient, ok := h.recipientFromPath(w, r)
	if !ok {
		return
	}
	var signals targetSignals
	if err := datastar.ReadSignals(r, &signals); err != nil {
		h.badRequest(w, r, err)
		return
	}

	key, known := addressKey[signals.Kind]
	sse := datastar.NewSSE(w, r)
	if !known || signals.Address == "" {
		h.patchTargets(r.Context(), sse, recipient.ID,
			"Pick a channel and give it an address.")
		return
	}

	config, err := json.Marshal(map[string]string{key: signals.Address})
	if err != nil {
		h.serverError(w, r, err, "encode target config")
		return
	}
	if _, err := h.Db.CreateTarget(r.Context(), store.CreateTargetParams{
		RecipientID: recipient.ID,
		Kind:        signals.Kind,
		Config:      string(config),
		Enabled:     true,
	}); err != nil {
		h.serverError(w, r, err, "create target")
		return
	}
	h.patchTargets(r.Context(), sse, recipient.ID, "")
}

func (h *Handlers) handleTargetToggle(w http.ResponseWriter, r *http.Request) {
	target, ok := h.targetFromPath(w, r)
	if !ok {
		return
	}
	if err := h.Db.SetTargetEnabled(r.Context(), store.SetTargetEnabledParams{
		ID:      target.ID,
		Enabled: !target.Enabled,
	}); err != nil {
		h.serverError(w, r, err, "toggle target")
		return
	}
	target.Enabled = !target.Enabled
	_ = datastar.NewSSE(w, r).PatchElementTempl(templates.ChannelRow(targetRow(target)))
}

func (h *Handlers) handleTargetDelete(w http.ResponseWriter, r *http.Request) {
	target, ok := h.targetFromPath(w, r)
	if !ok {
		return
	}
	if err := h.Db.DeleteTarget(r.Context(), target.ID); err != nil {
		h.serverError(w, r, err, "delete target")
		return
	}
	_ = datastar.NewSSE(w, r).PatchElements("",
		datastar.WithModeRemove(),
		datastar.WithSelector("#target-"+strconv.FormatInt(target.ID, 10)))
}

// handleTargetTest delivers through the real notifier, not a simulation.
//
// The point is to exercise everything a nightly run would: the same renderer,
// the same transport, the same credential. A test that stopped short of the
// network would pass for exactly the configurations that fail at 21:00.
func (h *Handlers) handleTargetTest(w http.ResponseWriter, r *http.Request) {
	target, ok := h.targetFromPath(w, r)
	if !ok {
		return
	}
	row := targetRow(target)
	sse := datastar.NewSSE(w, r)

	notifier, wired := h.Notifiers[target.Kind]
	if !wired {
		row.Status = "No " + target.Kind + " delivery is configured on this server."
		_ = sse.PatchElementTempl(templates.ChannelRow(row))
		return
	}

	d, err := h.testDigest(r.Context(), target.RecipientID)
	if err != nil {
		h.serverError(w, r, err, "build test digest")
		return
	}
	if _, err := notifier.Send(r.Context(), json.RawMessage(target.Config), d); err != nil {
		h.Log.WarnContext(r.Context(), "test send failed",
			"target_id", target.ID, "kind", target.Kind, "error", err)
		row.Status = err.Error()
	} else {
		row.Status = "Sent."
		row.StatusOK = true
	}
	_ = sse.PatchElementTempl(templates.ChannelRow(row))
}

// testDigest is the most recent real digest for this recipient, so a test send
// carries a working link and the day they actually received.
//
// With no snapshot yet it falls back to an empty day for today, which is a
// real message too — grill Q5 makes an empty calendar a digest rather than
// silence — but it has no token and therefore no link.
func (h *Handlers) testDigest(ctx context.Context, recipientID int64) (digest.Digest, error) {
	recipient, err := h.Db.GetRecipient(ctx, recipientID)
	if err != nil {
		return digest.Digest{}, fmt.Errorf("ui: recipient %d: %w", recipientID, err)
	}
	d := digest.Digest{RecipientName: recipient.Name}

	latest, err := h.Db.ListLatestSnapshots(ctx)
	if err != nil {
		return digest.Digest{}, fmt.Errorf("ui: latest snapshots: %w", err)
	}
	for _, s := range latest {
		if s.RecipientID != recipientID {
			continue
		}
		full, err := h.Db.FindSnapshotByToken(ctx, s.Token)
		if err != nil {
			return digest.Digest{}, fmt.Errorf("ui: snapshot %s: %w", s.Token, err)
		}
		var events []calendar.Event
		if err := json.Unmarshal([]byte(full.Events), &events); err != nil {
			return digest.Digest{}, fmt.Errorf("ui: decode snapshot %d: %w", full.ID, err)
		}
		d.Date, d.Token, d.Events = full.DigestDate, full.Token, events
		return d, nil
	}

	loc, err := time.LoadLocation(recipient.Tz)
	if err != nil {
		loc = time.UTC
	}
	d.Date = h.now().In(loc).Format(time.DateOnly)
	return d, nil
}

func (h *Handlers) patchTargets(
	ctx context.Context,
	sse *datastar.ServerSentEventGenerator,
	recipientID int64,
	problem string,
) {
	targets, err := h.Db.ListTargets(ctx, recipientID)
	if err != nil {
		h.Log.ErrorContext(ctx, "list targets", "error", err)
		return
	}
	rows := make([]templates.TargetFormRow, 0, len(targets))
	for _, t := range targets {
		rows = append(rows, targetRow(t))
	}
	_ = sse.PatchElementTempl(templates.Targets(recipientID, rows, problem))
}

func (h *Handlers) patchForm(sse *datastar.ServerSentEventGenerator, view templates.RecipientFormView) {
	_ = sse.PatchElementTempl(templates.RecipientFormBody(view))
}

func (h *Handlers) recipientFromPath(w http.ResponseWriter, r *http.Request) (store.Recipients, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return store.Recipients{}, false
	}
	recipient, err := h.Db.GetRecipient(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return store.Recipients{}, false
		}
		h.serverError(w, r, err, "load recipient")
		return store.Recipients{}, false
	}
	return recipient, true
}

func (h *Handlers) targetFromPath(w http.ResponseWriter, r *http.Request) (store.NotificationTargets, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return store.NotificationTargets{}, false
	}
	target, err := h.Db.GetTarget(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return store.NotificationTargets{}, false
		}
		h.serverError(w, r, err, "load target")
		return store.NotificationTargets{}, false
	}
	return target, true
}

func (h *Handlers) flash(r *http.Request, message string) {
	h.Sessions.Put(r.Context(), flashKey, message)
}

func (h *Handlers) badRequest(w http.ResponseWriter, r *http.Request, err error) {
	h.Log.WarnContext(r.Context(), "unreadable request", "error", err, "path", r.URL.Path)
	http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
}
