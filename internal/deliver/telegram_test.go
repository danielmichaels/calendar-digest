package deliver

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielmichaels/calendar-digest/internal/notify"
)

func TestTelegramRendersATimelineAndLink(t *testing.T) {
	r := TelegramRenderer{BaseURL: testBaseURL}

	golden(t, "telegram_full_day.golden", r.Render(fullDay(t)))
}

// Grill Q5: an empty day still sends, so silence always means something is
// broken rather than possibly meaning a free day.
func TestTelegramConfirmsAnEmptyDay(t *testing.T) {
	r := TelegramRenderer{BaseURL: testBaseURL}

	golden(t, "telegram_empty_day.golden", r.Render(emptyDay(t)))
}

// An all-day event's Start and End are midnight boundaries in the recipient's
// zone, not instants. Rendered with a clock they read "00:00–00:00", which a
// test asserting only that the output is non-empty would never notice.
func TestAllDayEventsNeverRenderAsATime(t *testing.T) {
	got := TelegramRenderer{BaseURL: testBaseURL}.Render(fullDay(t))

	line := lineContaining(t, got, "Public holiday")
	if !strings.HasPrefix(line, "All day") {
		t.Errorf("all-day event rendered as %q, want it to lead with All day", line)
	}
	if strings.Contains(line, "00:00") {
		t.Errorf("all-day event rendered a clock time: %q", line)
	}
}

// The offset travels with the timestamp through the snapshot, so a renderer
// needs no *time.Location to print the recipient's wall clock. If that ever
// stopped being true these times would silently shift to UTC.
func TestTimesAreTheRecipientsWallClock(t *testing.T) {
	got := TelegramRenderer{BaseURL: testBaseURL}.Render(fullDay(t))

	if line := lineContaining(t, got, "Standup"); !strings.HasPrefix(line, "09:00–09:15") {
		t.Errorf("Standup rendered as %q, want 09:00–09:15 Brisbane time", line)
	}
}

// BASE_URL is optional at boot, so this is a state a real deployment reaches.
// No link is honest; "/d/xK3f" is a link nothing can follow.
func TestNoBaseURLOmitsTheLinkRatherThanBreakingIt(t *testing.T) {
	got := TelegramRenderer{}.Render(fullDay(t))

	if strings.Contains(got, "/d/") {
		t.Errorf("rendered a link with no BASE_URL:\n%s", got)
	}
	if !strings.Contains(got, "Standup") {
		t.Errorf("dropped the events along with the link:\n%s", got)
	}
	if strings.HasSuffix(got, "\n\n") {
		t.Errorf("left the blank line where the link would have been:\n%q", got)
	}
}

func TestTelegramNotifierSendsTheRenderedBodyToTheTargetChat(t *testing.T) {
	var gotBody map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(srv.Close)

	n := &TelegramNotifier{
		Bot:      &notify.Telegram{Token: "123:abc", BaseURL: srv.URL, HTTP: srv.Client()},
		Renderer: TelegramRenderer{BaseURL: testBaseURL},
	}

	body, err := n.Send(t.Context(), json.RawMessage(`{"chat_id":"9911"}`), fullDay(t))
	if err != nil {
		t.Fatalf("send: %v", err)
	}

	if gotBody["chat_id"] != "9911" {
		t.Errorf("chat_id = %q, want the target's", gotBody["chat_id"])
	}
	// The body returned is what SendWorker logs as what the recipient got, so
	// it has to be the text that was actually transmitted.
	if gotBody["text"] != body {
		t.Errorf("returned body is not the text sent:\nsent: %q\ngot:  %q", gotBody["text"], body)
	}
	if !strings.Contains(body, "Standup") {
		t.Errorf("body is not the rendered digest: %q", body)
	}
}

// A nil error is what sets notified_at, so a rejected send must never produce
// one — that would record a digest as delivered that nobody received.
func TestTelegramNotifierFailsWhenTelegramRejectsTheSend(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"ok":false,"description":"chat not found"}`))
	}))
	t.Cleanup(srv.Close)

	n := &TelegramNotifier{
		Bot:      &notify.Telegram{Token: "123:abc", BaseURL: srv.URL, HTTP: srv.Client()},
		Renderer: TelegramRenderer{BaseURL: testBaseURL},
	}

	if _, err := n.Send(t.Context(), json.RawMessage(`{"chat_id":"9911"}`), fullDay(t)); err == nil {
		t.Fatal("no error for a rejected send")
	}
}

// A target with no chat_id cannot be fixed by retrying it for seventeen hours;
// it is a row that needs editing, and the error says so.
func TestTelegramNotifierRejectsAnUnusableTarget(t *testing.T) {
	n := &TelegramNotifier{Bot: &notify.Telegram{}, Renderer: TelegramRenderer{}}

	for _, target := range []string{`{}`, `{"chat_id":""}`, `not json`} {
		t.Run(target, func(t *testing.T) {
			_, err := n.Send(t.Context(), json.RawMessage(target), fullDay(t))
			if !errors.Is(err, ErrTargetConfig) {
				t.Errorf("err = %v, want ErrTargetConfig", err)
			}
		})
	}
}

func lineContaining(t *testing.T, body, want string) string {
	t.Helper()
	for _, line := range strings.Split(body, "\n") {
		if strings.Contains(line, want) {
			return line
		}
	}
	t.Fatalf("no line containing %q in:\n%s", want, body)
	return ""
}
