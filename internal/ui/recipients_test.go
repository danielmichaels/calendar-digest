package ui

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/danielmichaels/calendar-digest/internal/digest"
	"github.com/danielmichaels/calendar-digest/internal/jobs"
	"github.com/danielmichaels/calendar-digest/internal/store"
)

// send performs a request through the same middleware stack /app has.
func send(t *testing.T, h *Handlers, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	withSession(h, "/app", h.Routes()).ServeHTTP(rec, req)
	return rec
}

type fakeNotifier struct {
	kind   string
	err    error
	sent   []digest.Digest
	target []json.RawMessage
}

type fakeDigestRunner struct {
	args []jobs.DigestArgs
	err  error
}

func (f *fakeDigestRunner) RunDigestNow(_ context.Context, args jobs.DigestArgs) error {
	f.args = append(f.args, args)
	return f.err
}

func (f *fakeNotifier) Kind() string { return f.kind }

func (f *fakeNotifier) Send(
	_ context.Context,
	target json.RawMessage,
	d digest.Digest,
) (string, error) {
	f.sent = append(f.sent, d)
	f.target = append(f.target, target)
	if f.err != nil {
		return "", f.err
	}
	return "rendered body", nil
}

func TestCreateRecipientStoresItAndGoesToItsChannels(t *testing.T) {
	h, q, _ := newHandlers(t)

	rec := send(t, h, http.MethodPost, "/app/recipients",
		`{"name":"Dan","calendar_id":"dan@example.com","notify_time":"21:00","tz":"Australia/Brisbane","enabled":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}

	recipients, err := q.ListRecipients(t.Context())
	if err != nil {
		t.Fatalf("list recipients: %v", err)
	}
	if len(recipients) != 1 {
		t.Fatalf("stored %d recipients, want 1", len(recipients))
	}
	if recipients[0].Tz != "Australia/Brisbane" || recipients[0].NotifyTime != "21:00" {
		t.Errorf("stored %+v", recipients[0])
	}
	// A recipient with no channels receives nothing, so the create flow lands
	// where channels are added rather than back on the overview.
	if body := rec.Body.String(); !strings.Contains(body, "/app/recipients/") {
		t.Errorf("did not redirect to the new recipient:\n%s", body)
	}
}

func TestSendCalendarDigestNowUsesTheRecipientZoneAndReportsSuccess(t *testing.T) {
	h, q, _ := newHandlers(t)
	h.Now = func() time.Time { return homeNow }
	r, _ := q.CreateRecipient(t.Context(), store.CreateRecipientParams{
		Name: "Dan", CalendarID: "dan@example.com", NotifyTime: "21:00",
		Tz: "Australia/Brisbane", Enabled: true,
	})
	runner := &fakeDigestRunner{}
	h.DigestRunner = runner

	rec := send(t, h, http.MethodPost, "/app/recipients/"+itoa(r.ID)+"/digest-now", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if len(runner.args) != 1 || runner.args[0].DigestDate != "2026-08-05" || !runner.args[0].Force {
		t.Fatalf("digest args = %+v, want recipient's next local day", runner.args)
	}
	if !strings.Contains(rec.Body.String(), "Calendar read succeeded for 2026-08-05") {
		t.Errorf("success was not rendered:\n%s", rec.Body.String())
	}
}

func TestSendCalendarDigestNowReportsCalendarFailure(t *testing.T) {
	h, q, _ := newHandlers(t)
	r, _ := q.CreateRecipient(t.Context(), store.CreateRecipientParams{
		Name: "Dan", CalendarID: "dan@example.com", NotifyTime: "21:00",
		Tz: "Australia/Brisbane", Enabled: true,
	})
	h.DigestRunner = &fakeDigestRunner{err: errors.New("calendar: access refused")}

	rec := send(t, h, http.MethodPost, "/app/recipients/"+itoa(r.ID)+"/digest-now", "")

	if !strings.Contains(rec.Body.String(), "Calendar access failed: calendar: access refused") {
		t.Errorf("failure was not rendered:\n%s", rec.Body.String())
	}
}

// A tz the due check cannot load stops that recipient forever and looks
// exactly like an empty calendar. The form is the only place it can be caught
// before it costs anybody a digest.
func TestCreateRecipientRefusesAScheduleThatWouldNeverFire(t *testing.T) {
	tests := []struct {
		name, body, wants string
	}{
		{
			name:  "unknown timezone",
			body:  `{"name":"Dan","calendar_id":"d@e.com","notify_time":"21:00","tz":"Mars/Olympus","enabled":true}`,
			wants: "Mars/Olympus",
		},
		{
			name:  "notify time that is not HH:MM",
			body:  `{"name":"Dan","calendar_id":"d@e.com","notify_time":"9pm","tz":"Australia/Brisbane","enabled":true}`,
			wants: "HH:MM",
		},
		{
			name:  "empty timezone",
			body:  `{"name":"Dan","calendar_id":"d@e.com","notify_time":"21:00","tz":"","enabled":true}`,
			wants: "timezone is required",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, q, _ := newHandlers(t)

			rec := send(t, h, http.MethodPost, "/app/recipients", tc.body)

			recipients, err := q.ListRecipients(t.Context())
			if err != nil {
				t.Fatalf("list recipients: %v", err)
			}
			if len(recipients) != 0 {
				t.Fatalf("stored a recipient with an unusable schedule: %+v", recipients)
			}
			if !strings.Contains(rec.Body.String(), tc.wants) {
				t.Errorf("response does not explain the problem (%q):\n%s", tc.wants, rec.Body.String())
			}
		})
	}
}

// Everything wrong should come back in one pass rather than one field per
// attempt.
func TestCreateRecipientReportsEveryProblemAtOnce(t *testing.T) {
	h, _, _ := newHandlers(t)

	rec := send(t, h, http.MethodPost, "/app/recipients",
		`{"name":"","calendar_id":"","notify_time":"nope","tz":"Mars/Olympus","enabled":true}`)

	body := rec.Body.String()
	for _, want := range []string{"name is required", "calendar ID is required", "HH:MM", "Mars/Olympus"} {
		if !strings.Contains(body, want) {
			t.Errorf("response is missing %q:\n%s", want, body)
		}
	}
}

// A rejected save must come back with what was typed, or correcting one field
// means retyping the rest.
func TestARejectedSaveKeepsWhatWasTyped(t *testing.T) {
	h, _, _ := newHandlers(t)

	rec := send(t, h, http.MethodPost, "/app/recipients",
		`{"name":"Dan","calendar_id":"dan@example.com","notify_time":"9pm","tz":"Australia/Brisbane","enabled":true}`)

	if body := rec.Body.String(); !strings.Contains(body, "dan@example.com") {
		t.Errorf("lost the visitor's input:\n%s", body)
	}
}

func TestUpdateRecipientSaves(t *testing.T) {
	h, q, _ := newHandlers(t)
	r, _ := seedSnapshot(t, q, fixtureEvents(t))

	send(t, h, http.MethodPost, "/app/recipients/"+itoa(r.ID),
		`{"name":"Daniel","calendar_id":"dan@example.com","notify_time":"07:30","tz":"Pacific/Auckland","enabled":false}`)

	updated, err := q.GetRecipient(t.Context(), r.ID)
	if err != nil {
		t.Fatalf("get recipient: %v", err)
	}
	if updated.Name != "Daniel" || updated.NotifyTime != "07:30" ||
		updated.Tz != "Pacific/Auckland" || updated.Enabled {
		t.Errorf("stored %+v", updated)
	}
}

func TestDeleteRecipientTakesItsDigestsWithIt(t *testing.T) {
	h, q, _ := newHandlers(t)
	r, token := seedSnapshot(t, q, fixtureEvents(t))
	addTarget(t, q, r.ID, "telegram", `{"chat_id":"9911"}`, true)

	send(t, h, http.MethodDelete, "/app/recipients/"+itoa(r.ID), "")

	if _, err := q.GetRecipient(t.Context(), r.ID); err == nil {
		t.Error("recipient survived the delete")
	}
	// The cascade matters: a snapshot belonging to nobody is a page that can
	// still be opened by anyone holding the link.
	if _, err := q.FindSnapshotByToken(t.Context(), token); err == nil {
		t.Error("snapshot survived its recipient, so the detail page still resolves")
	}
	if targets, _ := q.ListTargets(t.Context(), r.ID); len(targets) != 0 {
		t.Errorf("targets survived their recipient: %+v", targets)
	}
}

func TestAddTargetStoresTheConfigShapeTheNotifiersRead(t *testing.T) {
	tests := []struct {
		kind, address, wantKey string
	}{
		{"telegram", "9911", "chat_id"},
		{"email", "dan@example.com", "address"},
		{"sms", "+61400000000", "phone"},
	}
	for _, tc := range tests {
		t.Run(tc.kind, func(t *testing.T) {
			h, q, _ := newHandlers(t)
			r, _ := seedSnapshot(t, q, fixtureEvents(t))

			send(t, h, http.MethodPost, "/app/recipients/"+itoa(r.ID)+"/targets",
				`{"kind":"`+tc.kind+`","address":"`+tc.address+`"}`)

			targets, err := q.ListTargets(t.Context(), r.ID)
			if err != nil {
				t.Fatalf("list targets: %v", err)
			}
			if len(targets) != 1 {
				t.Fatalf("stored %d targets, want 1", len(targets))
			}

			var config map[string]string
			if err := json.Unmarshal([]byte(targets[0].Config), &config); err != nil {
				t.Fatalf("stored config is not JSON: %v", err)
			}
			if config[tc.wantKey] != tc.address {
				t.Errorf("config = %v, want %s=%s", config, tc.wantKey, tc.address)
			}
		})
	}
}

// The CHECK on notification_targets.kind would reject this anyway, but as a
// constraint violation rather than a message; the form should not get that far.
func TestAddTargetRefusesAnUnknownKind(t *testing.T) {
	h, q, _ := newHandlers(t)
	r, _ := seedSnapshot(t, q, fixtureEvents(t))

	send(t, h, http.MethodPost, "/app/recipients/"+itoa(r.ID)+"/targets",
		`{"kind":"carrier-pigeon","address":"coo"}`)

	if targets, _ := q.ListTargets(t.Context(), r.ID); len(targets) != 0 {
		t.Errorf("stored a target of an unknown kind: %+v", targets)
	}
}

func TestAddTargetRefusesAnEmptyAddress(t *testing.T) {
	h, q, _ := newHandlers(t)
	r, _ := seedSnapshot(t, q, fixtureEvents(t))

	rec := send(t, h, http.MethodPost, "/app/recipients/"+itoa(r.ID)+"/targets",
		`{"kind":"telegram","address":""}`)

	if targets, _ := q.ListTargets(t.Context(), r.ID); len(targets) != 0 {
		t.Errorf("stored a target with nothing to send to: %+v", targets)
	}
	if !strings.Contains(rec.Body.String(), "address") {
		t.Errorf("no explanation:\n%s", rec.Body.String())
	}
}

func TestToggleTargetFlipsItBothWays(t *testing.T) {
	h, q, _ := newHandlers(t)
	r, _ := seedSnapshot(t, q, fixtureEvents(t))
	target := addTarget(t, q, r.ID, "telegram", `{"chat_id":"9911"}`, true)

	send(t, h, http.MethodPost, "/app/targets/"+itoa(target.ID)+"/toggle", "")
	if after, _ := q.GetTarget(t.Context(), target.ID); after.Enabled {
		t.Error("first toggle did not disable the target")
	}

	send(t, h, http.MethodPost, "/app/targets/"+itoa(target.ID)+"/toggle", "")
	if after, _ := q.GetTarget(t.Context(), target.ID); !after.Enabled {
		t.Error("second toggle did not re-enable the target")
	}
}

func TestDeleteTargetRemovesOnlyThatRow(t *testing.T) {
	h, q, _ := newHandlers(t)
	r, _ := seedSnapshot(t, q, fixtureEvents(t))
	first := addTarget(t, q, r.ID, "telegram", `{"chat_id":"9911"}`, true)
	addTarget(t, q, r.ID, "email", `{"address":"dan@example.com"}`, true)

	send(t, h, http.MethodDelete, "/app/targets/"+itoa(first.ID), "")

	targets, err := q.ListTargets(t.Context(), r.ID)
	if err != nil {
		t.Fatalf("list targets: %v", err)
	}
	if len(targets) != 1 || targets[0].Kind != "email" {
		t.Errorf("targets after delete = %+v, want only the email one", targets)
	}
}

// "Send test" has to go through the real notifier. A test that stopped short
// of the transport would pass for exactly the configurations that fail at the
// notify time.
func TestSendTestDeliversThroughTheLiveNotifier(t *testing.T) {
	h, q, _ := newHandlers(t)
	r, token := seedSnapshot(t, q, fixtureEvents(t))
	target := addTarget(t, q, r.ID, "telegram", `{"chat_id":"9911"}`, true)

	notifier := &fakeNotifier{kind: "telegram"}
	h.Notifiers = map[string]jobs.Notifier{"telegram": notifier}

	rec := send(t, h, http.MethodPost, "/app/targets/"+itoa(target.ID)+"/test", "")

	if len(notifier.sent) != 1 {
		t.Fatalf("notifier called %d times, want 1", len(notifier.sent))
	}
	// The real snapshot, so the test message carries a link that works and the
	// day the recipient actually got.
	if got := notifier.sent[0]; got.Token != token || got.RecipientName != "Dan" {
		t.Errorf("sent %+v, want the recipient's latest snapshot", got)
	}
	if string(notifier.target[0]) != `{"chat_id":"9911"}` {
		t.Errorf("notifier got target config %q", notifier.target[0])
	}
	if !strings.Contains(rec.Body.String(), "Sent.") {
		t.Errorf("no confirmation in the response:\n%s", rec.Body.String())
	}
}

// A failing test send is the whole reason the button exists, so the reason has
// to reach the screen rather than only the log.
func TestSendTestShowsWhyItFailed(t *testing.T) {
	h, q, _ := newHandlers(t)
	r, _ := seedSnapshot(t, q, fixtureEvents(t))
	target := addTarget(t, q, r.ID, "telegram", `{"chat_id":"9911"}`, true)

	h.Notifiers = map[string]jobs.Notifier{
		"telegram": &fakeNotifier{kind: "telegram", err: errors.New("chat not found")},
	}

	rec := send(t, h, http.MethodPost, "/app/targets/"+itoa(target.ID)+"/test", "")

	if !strings.Contains(rec.Body.String(), "chat not found") {
		t.Errorf("failure reason did not reach the page:\n%s", rec.Body.String())
	}
}

// A kind with no notifier wired is a channel this server cannot deliver at
// all. Saying so is more useful than a generic failure.
func TestSendTestSaysWhenTheChannelIsNotConfigured(t *testing.T) {
	h, q, _ := newHandlers(t)
	r, _ := seedSnapshot(t, q, fixtureEvents(t))
	target := addTarget(t, q, r.ID, "email", `{"address":"dan@example.com"}`, true)

	h.Notifiers = map[string]jobs.Notifier{}

	rec := send(t, h, http.MethodPost, "/app/targets/"+itoa(target.ID)+"/test", "")

	if !strings.Contains(rec.Body.String(), "No email delivery is configured") {
		t.Errorf("did not explain the missing notifier:\n%s", rec.Body.String())
	}
}

// With no snapshot yet there is still something to send: grill Q5 makes an
// empty day a digest rather than silence.
func TestSendTestWorksBeforeAnyDigestHasBeenCaptured(t *testing.T) {
	h, q, _ := newHandlers(t)
	h.Now = func() time.Time { return homeNow }

	r, err := q.CreateRecipient(t.Context(), store.CreateRecipientParams{
		Name: "Fresh", CalendarID: "fresh@example.com",
		NotifyTime: "21:00", Tz: "Australia/Brisbane", Enabled: true,
	})
	if err != nil {
		t.Fatalf("create recipient: %v", err)
	}
	target := addTarget(t, q, r.ID, "telegram", `{"chat_id":"9911"}`, true)

	notifier := &fakeNotifier{kind: "telegram"}
	h.Notifiers = map[string]jobs.Notifier{"telegram": notifier}

	send(t, h, http.MethodPost, "/app/targets/"+itoa(target.ID)+"/test", "")

	if len(notifier.sent) != 1 {
		t.Fatalf("notifier called %d times, want 1", len(notifier.sent))
	}
	if got := notifier.sent[0]; got.Date != "2026-08-04" || got.Token != "" {
		t.Errorf("sent %+v, want today in the recipient's zone and no token", got)
	}
}

func TestEditPageShowsTheChannels(t *testing.T) {
	h, q, _ := newHandlers(t)
	r, _ := seedSnapshot(t, q, fixtureEvents(t))
	addTarget(t, q, r.ID, "telegram", `{"chat_id":"9911"}`, true)
	addTarget(t, q, r.ID, "email", `{"address":"dan@example.com"}`, false)

	rec := send(t, h, http.MethodGet, "/app/recipients/"+itoa(r.ID), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"9911", "dan@example.com", "Australia/Brisbane", "21:00", "Send calendar digest now"} {
		if !strings.Contains(body, want) {
			t.Errorf("edit page is missing %q", want)
		}
	}
}

func TestUnknownRecipientAndTarget404(t *testing.T) {
	h, _, _ := newHandlers(t)

	for _, path := range []string{
		"/app/recipients/999",
		"/app/recipients/not-a-number",
	} {
		if rec := send(t, h, http.MethodGet, path, ""); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, rec.Code)
		}
	}
	if rec := send(t, h, http.MethodPost, "/app/targets/999/toggle", ""); rec.Code != http.StatusNotFound {
		t.Errorf("toggling an unknown target = %d, want 404", rec.Code)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// Datastar v1.0.2 — the client bundled in assets — only creates a signal for
// the value form of data-bind. The key form, data-bind-name, is silently
// inert: the input renders, the page looks right, and the form posts none of
// its fields.
//
// Nothing else here can catch that. Every other test in this file posts JSON
// built by hand, which is exactly the body a working client would send, so the
// whole suite stays green while the form sends nothing. This asserts the one
// detail the browser proved.
func TestFormBindingsUseTheSyntaxTheClientSupports(t *testing.T) {
	h, q, _ := newHandlers(t)
	r, _ := seedSnapshot(t, q, fixtureEvents(t))

	body := send(t, h, http.MethodGet, "/app/recipients/"+itoa(r.ID), "").Body.String()

	for _, signal := range []string{"name", "calendar_id", "notify_time", "tz", "enabled"} {
		if !strings.Contains(body, `data-bind="`+signal+`"`) {
			t.Errorf("no data-bind=%q on the form, so that field would post nothing", signal)
		}
	}
	if strings.Contains(body, "data-bind-") {
		t.Error("a key-form data-bind is present, which this client ignores")
	}
}

// The values have to be in the HTML as well as bound, so the form is right
// before Datastar runs and does not depend on signal-evaluation order.
func TestFormRendersItsValuesIntoTheMarkup(t *testing.T) {
	h, q, _ := newHandlers(t)
	r, _ := seedSnapshot(t, q, fixtureEvents(t))

	body := send(t, h, http.MethodGet, "/app/recipients/"+itoa(r.ID), "").Body.String()

	for _, want := range []string{`value="Dan"`, `value="dan@example.com"`, `value="21:00"`, `value="Australia/Brisbane"`} {
		if !strings.Contains(body, want) {
			t.Errorf("form is missing %s, so it renders blank without JavaScript", want)
		}
	}
}
