package deliver

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func senderFor(t *testing.T, relay *fakeRelay) *SMTPSender {
	t.Helper()
	host, port := relay.hostPort(t)
	return &SMTPSender{Host: host, Port: port, From: "digest@lookout.wiki"}
}

// Both bodies have to survive the transport, in the order that makes the HTML
// the preferred part. A notifier test against a fake sender cannot see any of
// this — it is decided inside go-mail and only visible on the wire.
func TestSMTPSendsBothPartsAsMultipartAlternative(t *testing.T) {
	relay := newFakeRelay(t)
	_, text, html := renderEmail(t, EmailRenderer{BaseURL: testBaseURL}, fullDay(t))

	err := senderFor(t, relay).Send(t.Context(), "dan@example.com", "Dan — Wednesday, 5 August", text, html)
	if err != nil {
		t.Fatalf("send: %v", err)
	}

	msgs := relay.messages()
	if len(msgs) != 1 {
		t.Fatalf("relay received %d messages, want 1", len(msgs))
	}
	got := msgs[0]

	if got.from != "digest@lookout.wiki" {
		t.Errorf("envelope from = %q", got.from)
	}
	if len(got.to) != 1 || got.to[0] != "dan@example.com" {
		t.Errorf("envelope to = %v", got.to)
	}
	if !strings.Contains(got.data, "multipart/alternative") {
		t.Errorf("message is not multipart/alternative:\n%s", got.data)
	}

	plain := strings.Index(got.data, "text/plain")
	richer := strings.Index(got.data, "text/html")
	switch {
	case plain < 0:
		t.Error("no text/plain part on the wire")
	case richer < 0:
		t.Error("no text/html part on the wire")
	case plain > richer:
		// multipart/alternative is ordered least-preferred first, so a client
		// that can render HTML would show the plain text instead.
		t.Error("text/plain follows text/html, so the HTML part would be ignored")
	}
	if !strings.Contains(got.data, "Wednesday, 5 August") {
		t.Errorf("subject did not reach the wire:\n%s", got.data)
	}
}

// A relay that refuses the recipient must produce an error. Returning nil here
// would set notified_at on a digest that bounced.
func TestSMTPFailsWhenTheRelayRefusesTheRecipient(t *testing.T) {
	relay := newFakeRelay(t)
	relay.reject = true

	err := senderFor(t, relay).Send(t.Context(), "nobody@example.com", "s", "t", "<p>h</p>")
	if err == nil {
		t.Fatal("no error when the relay rejected the recipient")
	}
	if len(relay.messages()) != 0 {
		t.Error("relay recorded a message it rejected")
	}
}

// The worker's context has to bound the send, or a relay that accepts the
// connection and then goes quiet holds a worker until River's own job timeout.
func TestSMTPHonoursACancelledContext(t *testing.T) {
	relay := newFakeRelay(t)
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	// Nothing is listening here, so the dial blocks until the deadline.
	sender := senderFor(t, relay)
	sender.Host = "192.0.2.1" // TEST-NET-1: routable nowhere

	start := time.Now()
	err := sender.Send(ctx, "dan@example.com", "s", "t", "<p>h</p>")
	if err == nil {
		t.Fatal("no error against an unreachable relay")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("send took %v, want it bounded by the context", elapsed)
	}
}

func TestEmailNotifierRendersAndSendsToTheTargetAddress(t *testing.T) {
	relay := newFakeRelay(t)
	n := &EmailNotifier{
		Sender:   senderFor(t, relay),
		Renderer: EmailRenderer{BaseURL: testBaseURL},
	}

	body, err := n.Send(t.Context(), json.RawMessage(`{"address":"dan@example.com"}`), fullDay(t))
	if err != nil {
		t.Fatalf("send: %v", err)
	}

	msgs := relay.messages()
	if len(msgs) != 1 {
		t.Fatalf("relay received %d messages, want 1", len(msgs))
	}
	if msgs[0].to[0] != "dan@example.com" {
		t.Errorf("sent to %v, want the target's address", msgs[0].to)
	}
	if !strings.Contains(body, "Standup") {
		t.Errorf("returned body is not the rendered digest: %q", body)
	}
}

func TestEmailNotifierRejectsAnUnusableTarget(t *testing.T) {
	n := &EmailNotifier{Sender: &SMTPSender{}, Renderer: EmailRenderer{}}

	for _, target := range []string{`{}`, `{"address":""}`, `[1,2]`} {
		t.Run(target, func(t *testing.T) {
			_, err := n.Send(t.Context(), json.RawMessage(target), fullDay(t))
			if !errors.Is(err, ErrTargetConfig) {
				t.Errorf("err = %v, want ErrTargetConfig", err)
			}
		})
	}
}
