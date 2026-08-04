package deliver

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/danielmichaels/calendar-digest/internal/calendar"
	"github.com/danielmichaels/calendar-digest/internal/digest"
)

func TestSMSSegments(t *testing.T) {
	tests := []struct {
		name string
		text string
		want int
	}{
		{"empty", "", 0},
		{"plain ASCII inside the budget", strings.Repeat("a", 160), 1},
		{"one character over becomes two", strings.Repeat("a", 161), 2},
		// The extension table costs an escape septet as well as the character,
		// so half as many fit as a naive character count suggests.
		{"an extended character costs two septets", strings.Repeat("[", 80), 1},
		{"eighty-one extended characters do not fit", strings.Repeat("[", 81), 2},
		// This is the trap the other renderers walk into: one en dash and the
		// budget falls from 160 to 70.
		{"one en dash forces UCS-2", strings.Repeat("a", 100) + "–", 2},
		{"UCS-2 inside its own budget", strings.Repeat("é", 70), 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := SMSSegments(tc.text); got != tc.want {
				t.Errorf("SMSSegments(%d chars) = %d, want %d", len([]rune(tc.text)), got, tc.want)
			}
		})
	}
}

// The whole point of the SMS format: it has to arrive as one message.
func TestSMSFitsOneSegment(t *testing.T) {
	r := SMSRenderer{BaseURL: testBaseURL}

	for _, tc := range []struct {
		name string
		d    func(*testing.T) digest.Digest
	}{
		{"a full day", fullDay},
		{"an empty day", emptyDay},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := r.Render(tc.d(t))
			if n := SMSSegments(got); n != 1 {
				t.Errorf("rendered %d segments, want 1:\n%q", n, got)
			}
		})
	}
}

// Grill Q5 applies to every channel: an empty day sends a confirmation, so
// silence always means something is broken.
func TestSMSConfirmsAnEmptyDay(t *testing.T) {
	got := SMSRenderer{BaseURL: testBaseURL}.Render(emptyDay(t))

	if strings.TrimSpace(got) == "" {
		t.Error("an empty day rendered nothing, so a free day is indistinguishable from a failed run")
	}
}

// The link is the point of the message: what will not fit in 160 characters
// lives on the page.
func TestSMSCarriesTheDetailLink(t *testing.T) {
	got := SMSRenderer{BaseURL: testBaseURL}.Render(fullDay(t))

	if !strings.Contains(got, testBaseURL+"/d/xK3fQ9mTn2vB7cLpR4wZ") {
		t.Errorf("no detail link in:\n%q", got)
	}
}

func TestSMSOmitsTheLinkWithNoBaseURL(t *testing.T) {
	got := SMSRenderer{}.Render(fullDay(t))

	if strings.Contains(got, "/d/") {
		t.Errorf("rendered a link with no BASE_URL:\n%q", got)
	}
}

// A busy day cannot name everything, so it names what fits and counts the
// rest. The link carries the remainder, which is why it is never the thing
// dropped to make room.
func TestSMSNamesWhatFitsAndCountsTheRest(t *testing.T) {
	d := fullDay(t)
	loc := brisbane(t)
	for hour := 6; hour < 18; hour++ {
		d.Events = append(d.Events, calendar.Event{
			Summary: "Recurring project sync with the platform team",
			Start:   time.Date(2026, 8, 5, hour, 0, 0, 0, loc),
			End:     time.Date(2026, 8, 5, hour, 30, 0, 0, loc),
			Status:  "confirmed",
		})
	}

	got := SMSRenderer{BaseURL: testBaseURL}.Render(throughJSON(t, d))

	if n := SMSSegments(got); n != 1 {
		t.Errorf("rendered %d segments for a crowded day, want 1:\n%q", n, got)
	}
	if !strings.Contains(got, "more") {
		t.Errorf("dropped events without saying so:\n%q", got)
	}
	if !strings.Contains(got, testBaseURL) {
		t.Errorf("dropped the link, which is where the rest of the day lives:\n%q", got)
	}
}

// One character outside GSM-7 re-encodes the whole message and costs more than
// every event beside it. Folding is what stops a single emoji in one summary
// from collapsing the entire day down to a count.
func TestSMSFoldsCharactersThatWouldCostASegment(t *testing.T) {
	d := fullDay(t)
	d.Events[1].Summary = "Standup — Café 🦷 “planning”"

	got := SMSRenderer{BaseURL: testBaseURL}.Render(d)

	if n := SMSSegments(got); n != 1 {
		t.Errorf("rendered %d segments, want 1:\n%q", n, got)
	}
	if !strings.Contains(got, "Standup - Caf") {
		t.Errorf("folding lost the summary rather than the typography:\n%q", got)
	}
	// Dentist follows Standup, so its presence proves folding kept the list
	// intact instead of the fitter dropping everything after the bad character.
	if !strings.Contains(got, "Dentist") {
		t.Errorf("one awkward summary cost the events after it:\n%q", got)
	}
}

// The fold must not mangle a summary it cannot represent at all: dropping
// every character would delete the event silently.
func TestSMSKeepsASummaryItCannotFold(t *testing.T) {
	if got := gsmFold("会議"); got != "会議" {
		t.Errorf("gsmFold(%q) = %q, want the original kept", "会議", got)
	}
}

// An all-day event's Start is a midnight boundary, not an instant, so a clock
// here is both wrong and the most expensive five characters in the message.
func TestSMSGivesAnAllDayEventNoClockTime(t *testing.T) {
	got := SMSRenderer{BaseURL: testBaseURL}.Render(fullDay(t))

	if strings.Contains(got, "00:00") {
		t.Errorf("all-day event rendered a clock time:\n%q", got)
	}
	if !strings.Contains(got, "Public holiday") {
		t.Errorf("all-day event missing entirely:\n%q", got)
	}
}

func TestSMSGolden(t *testing.T) {
	r := SMSRenderer{BaseURL: testBaseURL}

	golden(t, "sms_full_day.golden", r.Render(fullDay(t)))
	golden(t, "sms_empty_day.golden", r.Render(emptyDay(t)))
}

// Refusing loudly is the whole job: a nil error here would set notified_at on
// a digest that was never sent.
func TestSMSNotifierRefusesAndLogsThePayload(t *testing.T) {
	var logged bytes.Buffer
	n := &SMSNotifier{
		Renderer: SMSRenderer{BaseURL: testBaseURL},
		Log:      slog.New(slog.NewJSONHandler(&logged, nil)),
	}

	body, err := n.Send(t.Context(), json.RawMessage(`{"phone":"+61400000000"}`), fullDay(t))
	if !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("err = %v, want ErrNotImplemented", err)
	}
	if body != "" {
		t.Errorf("body = %q, want empty: nothing was delivered", body)
	}

	var record struct {
		Payload string `json:"payload"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(logged.Bytes()), &record); err != nil {
		t.Fatalf("decode log record: %v\n%s", err, logged.String())
	}

	var payload SMSWebhookPayload
	if err := json.Unmarshal([]byte(record.Payload), &payload); err != nil {
		t.Fatalf("logged payload is not the webhook contract: %v\n%s", err, record.Payload)
	}
	if payload.Phone != "+61400000000" {
		t.Errorf("payload phone = %q, want the target's", payload.Phone)
	}
	if payload.URL != testBaseURL+"/d/xK3fQ9mTn2vB7cLpR4wZ" {
		t.Errorf("payload url = %q", payload.URL)
	}
}

func TestSMSNotifierRejectsAnUnusableTarget(t *testing.T) {
	n := &SMSNotifier{}

	for _, target := range []string{`{}`, `{"phone":""}`, `"nope"`} {
		t.Run(target, func(t *testing.T) {
			_, err := n.Send(t.Context(), json.RawMessage(target), fullDay(t))
			if !errors.Is(err, ErrTargetConfig) {
				t.Errorf("err = %v, want ErrTargetConfig", err)
			}
		})
	}
}
