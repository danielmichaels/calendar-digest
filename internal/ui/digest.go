package ui

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/danielmichaels/calendar-digest/internal/calendar"
	"github.com/danielmichaels/calendar-digest/internal/digest"
	"github.com/danielmichaels/calendar-digest/internal/store"
	"github.com/danielmichaels/calendar-digest/internal/ui/templates"

	"github.com/go-chi/chi/v5"
)

// DigestRoutes serves the per-snapshot detail pages, for mounting at /d.
//
// Deliberately outside the session and CSRF middleware: the token in the path
// is the whole of the authorisation, and these pages are opened from a message
// by someone this app has never seen. A session cookie per view would buy
// nothing and write to the store on every read.
func (h *Handlers) DigestRoutes() http.Handler {
	r := chi.NewRouter()
	r.Get("/{token}", h.handleDigestDetail)
	return r
}

func (h *Handlers) handleDigestDetail(w http.ResponseWriter, r *http.Request) {
	snapshot, err := h.Db.FindSnapshotByToken(r.Context(), chi.URLParam(r, "token"))
	if err != nil {
		// A purged snapshot and a token that never existed are the same answer.
		// The link is unguessable rather than private, so a wrong one must not
		// be able to tell the difference.
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		h.serverError(w, r, err, "find snapshot by token")
		return
	}

	recipient, err := h.Db.GetRecipient(r.Context(), snapshot.RecipientID)
	if err != nil {
		h.serverError(w, r, err, "load recipient for detail page")
		return
	}

	var events []calendar.Event
	if err := json.Unmarshal([]byte(snapshot.Events), &events); err != nil {
		h.serverError(w, r, err, "decode snapshot events")
		return
	}

	view, err := digestView(recipient, snapshot, events)
	if err != nil {
		h.serverError(w, r, err, "build detail view")
		return
	}
	if err := templates.Digest(view).Render(r.Context(), w); err != nil {
		h.serverError(w, r, err, "render detail page")
	}
}

func digestView(
	recipient store.Recipients,
	snapshot store.DigestSnapshots,
	events []calendar.Event,
) (templates.DigestView, error) {
	// A recipient's tz is validated when the due check reads it, not here, so a
	// broken one must still render a page rather than 500 on a link somebody
	// was sent.
	loc, err := time.LoadLocation(recipient.Tz)
	if err != nil {
		loc = time.UTC
	}

	date, err := time.Parse(time.DateOnly, snapshot.DigestDate)
	if err != nil {
		return templates.DigestView{}, fmt.Errorf("ui: digest date %q: %w", snapshot.DigestDate, err)
	}
	long := date.Format("Monday, 2 January 2006")

	view := templates.DigestView{
		Title:         fmt.Sprintf("%s — %s", recipient.Name, long),
		RecipientName: recipient.Name,
		Zone:          loc.String(),
		Date:          long,
		CapturedAt:    capturedAt(snapshot.CreatedAt, loc),
	}
	for _, ev := range events {
		view.Events = append(view.Events, eventView(ev))
	}
	return view, nil
}

func eventView(ev calendar.Event) templates.EventView {
	out := templates.EventView{
		Time:          digest.TimeRange(ev),
		Summary:       ev.Summary,
		Tentative:     ev.Status == "tentative",
		Recurring:     ev.Recurring,
		Location:      ev.Location,
		Description:   ev.Description,
		ConferenceURL: ev.ConferenceURL,
		CalendarURL:   ev.HTMLLink,
		Organizer:     ev.Organizer,
	}
	for _, a := range ev.Attendees {
		name := a.DisplayName
		if name == "" {
			name = a.Email
		}
		out.Attendees = append(out.Attendees, templates.AttendeeView{
			Name:     name,
			Response: responseLabel(a.Response),
			Optional: a.Optional,
		})
	}
	return out
}

// responseLabel puts Google's responseStatus into words. Unknown values are
// shown as they arrived rather than blanked, so a new one from Google is
// visible instead of silently missing.
func responseLabel(response string) string {
	switch response {
	case "accepted":
		return "Accepted"
	case "declined":
		return "Declined"
	case "tentative":
		return "Maybe"
	case "needsAction":
		return "No reply"
	default:
		return response
	}
}

// capturedAt renders the snapshot's timestamp in the recipient's zone. A
// timestamp that will not parse is dropped rather than shown wrong: it is a
// footnote, and no page should fail over one.
func capturedAt(stored string, loc *time.Location) string {
	at, err := store.ParseTime(stored)
	if err != nil {
		return ""
	}
	return at.In(loc).Format("2 Jan 2006, 15:04 MST")
}
