package deliver

import (
	"context"
	"fmt"

	"github.com/wneessen/go-mail"
)

// SMTPSender delivers through a relay.
//
// Auth is configured only when a username is set, so an internal relay that
// accepts unauthenticated mail from inside the network works without inventing
// credentials for it. The TLS policy is opportunistic for the same reason: a
// relay offering STARTTLS gets it, one that does not still receives the digest.
type SMTPSender struct {
	Host     string
	Port     int
	Username string
	Password string
	// From is the envelope and header sender, EMAIL_FROM.
	From string
}

func (s *SMTPSender) Send(ctx context.Context, to, subject, text, html string) error {
	msg := mail.NewMsg()
	if err := msg.From(s.From); err != nil {
		return fmt.Errorf("deliver: smtp: from %q: %w", s.From, err)
	}
	if err := msg.To(to); err != nil {
		return fmt.Errorf("deliver: smtp: to %q: %w", to, err)
	}
	msg.Subject(subject)
	// Text first: multipart/alternative is ordered least-preferred to most, so
	// reversing these hands the plain-text part to every client that can render
	// HTML.
	msg.SetBodyString(mail.TypeTextPlain, text)
	msg.AddAlternativeString(mail.TypeTextHTML, html)

	opts := []mail.Option{
		mail.WithPort(s.Port),
		mail.WithTLSPortPolicy(mail.TLSOpportunistic),
	}
	if s.Username != "" {
		opts = append(opts,
			mail.WithSMTPAuth(mail.SMTPAuthAutoDiscover),
			mail.WithUsername(s.Username),
			mail.WithPassword(s.Password),
		)
	}

	client, err := mail.NewClient(s.Host, opts...)
	if err != nil {
		return fmt.Errorf("deliver: smtp: client: %w", err)
	}
	// WithContext so a relay that accepts the connection and then stops talking
	// is bounded by the job's deadline rather than holding a worker open.
	if err := client.DialAndSendWithContext(ctx, msg); err != nil {
		return fmt.Errorf("deliver: smtp: send to %q: %w", to, err)
	}
	return nil
}
