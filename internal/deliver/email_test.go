package deliver

import (
	"strings"
	"testing"

	"github.com/danielmichaels/calendar-digest/internal/digest"
)

func renderEmail(t *testing.T, r EmailRenderer, d digest.Digest) (subject, text, html string) {
	t.Helper()
	subject, text, html, err := r.Render(d)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	return subject, text, html
}

func TestEmailRendersBothParts(t *testing.T) {
	r := EmailRenderer{BaseURL: testBaseURL}

	subject, text, html := renderEmail(t, r, fullDay(t))

	golden(t, "email_full_day.subject.golden", subject)
	golden(t, "email_full_day.txt.golden", text)
	golden(t, "email_full_day.html.golden", html)
}

func TestEmailConfirmsAnEmptyDay(t *testing.T) {
	r := EmailRenderer{BaseURL: testBaseURL}

	subject, text, html := renderEmail(t, r, emptyDay(t))

	golden(t, "email_empty_day.subject.golden", subject)
	golden(t, "email_empty_day.txt.golden", text)
	golden(t, "email_empty_day.html.golden", html)
}

// The two parts of a multipart/alternative message are the same message. A
// client that renders the text part must not be told less than one rendering
// the HTML, which is the failure mode that goes unnoticed for years.
func TestBothEmailPartsCarryTheSameDetail(t *testing.T) {
	_, text, html := renderEmail(t, EmailRenderer{BaseURL: testBaseURL}, fullDay(t))

	for _, want := range []string{
		"Dentist",
		"12 Smith St, Brisbane",
		"Bring the referral letter.",
		"https://meet.google.com/abc-defg-hij",
		"Sam",
		"reception@dental.example",
		"tentative",
		"All day",
		"09:00–09:15",
		testBaseURL + "/d/xK3fQ9mTn2vB7cLpR4wZ",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("text part is missing %q", want)
		}
		if !strings.Contains(html, want) {
			t.Errorf("HTML part is missing %q", want)
		}
	}
}

// A calendar description is arbitrary text from a third party, and the HTML
// part is the one place it is interpolated into markup.
func TestEmailEscapesEventTextInTheHTMLPart(t *testing.T) {
	d := fullDay(t)
	d.Events[1].Summary = `Dentist <script>alert("x")</script>`

	_, _, html := renderEmail(t, EmailRenderer{BaseURL: testBaseURL}, d)

	if strings.Contains(html, "<script>") {
		t.Errorf("event summary reached the HTML part unescaped:\n%s", html)
	}
}
