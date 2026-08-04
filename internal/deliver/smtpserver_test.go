package deliver

import (
	"bufio"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
)

// received is one message as the relay saw it.
type received struct {
	from string
	to   []string
	data string
}

// fakeRelay is enough SMTP to accept a message and remember it.
//
// A fake sender proves the notifier calls the transport; only a socket proves
// the transport produces a message — that both body parts are on the wire, in
// the right order, under the right headers. None of that is observable from
// inside go-mail.
type fakeRelay struct {
	addr string

	mu  sync.Mutex
	got []received
	// reject makes every RCPT TO fail, standing in for a relay refusing the
	// recipient.
	reject bool
}

func newFakeRelay(t *testing.T) *fakeRelay {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	r := &fakeRelay{addr: ln.Addr().String()}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go r.serve(conn)
		}
	}()
	return r
}

func (r *fakeRelay) hostPort(t *testing.T) (string, int) {
	t.Helper()
	host, port, err := net.SplitHostPort(r.addr)
	if err != nil {
		t.Fatalf("split relay address: %v", err)
	}
	var p int
	if _, err := fmt.Sscanf(port, "%d", &p); err != nil {
		t.Fatalf("parse relay port: %v", err)
	}
	return host, p
}

func (r *fakeRelay) messages() []received {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]received(nil), r.got...)
}

func (r *fakeRelay) serve(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	rw := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))

	say := func(format string, args ...any) {
		_, _ = fmt.Fprintf(rw, format+"\r\n", args...)
		_ = rw.Flush()
	}

	say("220 fake.relay ESMTP")

	var msg received
	for {
		line, err := rw.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.TrimRight(line, "\r\n")
		upper := strings.ToUpper(cmd)

		switch {
		case strings.HasPrefix(upper, "EHLO"):
			// No STARTTLS and no AUTH advertised: the opportunistic policy then
			// continues in plaintext, which is what makes this loop enough.
			say("250-fake.relay")
			say("250 SIZE 35882577")
		case strings.HasPrefix(upper, "HELO"):
			say("250 fake.relay")
		case strings.HasPrefix(upper, "MAIL FROM"):
			msg = received{from: addressIn(cmd)}
			say("250 2.1.0 OK")
		case strings.HasPrefix(upper, "RCPT TO"):
			if r.reject {
				say("550 5.1.1 no such user")
				continue
			}
			msg.to = append(msg.to, addressIn(cmd))
			say("250 2.1.5 OK")
		case upper == "DATA":
			say("354 End data with <CR><LF>.<CR><LF>")
			var body strings.Builder
			for {
				dataLine, err := rw.ReadString('\n')
				if err != nil {
					return
				}
				if strings.TrimRight(dataLine, "\r\n") == "." {
					break
				}
				body.WriteString(dataLine)
			}
			msg.data = body.String()
			r.mu.Lock()
			r.got = append(r.got, msg)
			r.mu.Unlock()
			say("250 2.0.0 OK")
		case upper == "QUIT":
			say("221 2.0.0 Bye")
			return
		case upper == "RSET" || upper == "NOOP":
			say("250 2.0.0 OK")
		default:
			say("500 5.5.1 unrecognised")
		}
	}
}

// addressIn pulls the address out of "MAIL FROM:<dan@example.com>".
func addressIn(cmd string) string {
	open := strings.Index(cmd, "<")
	close := strings.LastIndex(cmd, ">")
	if open < 0 || close < open {
		return ""
	}
	return cmd[open+1 : close]
}
