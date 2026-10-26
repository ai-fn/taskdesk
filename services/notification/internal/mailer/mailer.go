// Package mailer sends email via SMTP (Mailpit in dev).
package mailer

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/smtp"
	"net/textproto"
	"strings"
	"time"
)

const (
	dialTimeout    = 5 * time.Second
	sessionTimeout = 10 * time.Second // whole SMTP conversation, dial included
)

// ErrRecipientRejected marks a permanent refusal of the address: retrying cannot help.
var ErrRecipientRejected = errors.New("recipient rejected")

// Mailer sends plain-text email via SMTP.
// When user+pass are empty it connects unauthenticated (Mailpit / open relay).
// When both are set it uses smtp.PlainAuth (Gmail, SendGrid SMTP, etc.).
type Mailer struct {
	addr    string // host:port
	from    string
	user    string
	pass    string
	timeout time.Duration
}

func New(addr, from, user, pass string) *Mailer {
	return &Mailer{addr: addr, from: from, user: user, pass: pass, timeout: sessionTimeout}
}

var headerSanitizer = strings.NewReplacer("\r", " ", "\n", " ")

// Send sends a plain-text email. It never outlives ctx or the session timeout.
func (m *Mailer) Send(ctx context.Context, to, subject, body string) error {
	if strings.ContainsAny(to, "\r\n") {
		return fmt.Errorf("%w: control characters in address", ErrRecipientRejected)
	}
	// Titles are user input. RFC 2047 for non-ASCII; fold between encoded-words to stay under the line limit.
	encSubject := strings.ReplaceAll(mime.QEncoding.Encode("utf-8", headerSanitizer.Replace(subject)), "?= =?", "?=\r\n =?")
	msg := strings.Join([]string{
		"From: " + m.from,
		"To: " + to,
		"Subject: " + encSubject,
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=utf-8",
		"",
		body,
	}, "\r\n")

	ctx, cancel := context.WithTimeout(ctx, m.timeout)
	defer cancel()

	host, _, err := net.SplitHostPort(m.addr)
	if err != nil {
		return fmt.Errorf("smtp addr: %w", err)
	}
	d := net.Dialer{Timeout: dialTimeout}
	conn, err := d.DialContext(ctx, "tcp", m.addr)
	if err != nil {
		return fmt.Errorf("smtp dial: %w", err)
	}
	// One absolute deadline bounds every read and write; closing on cancel covers shutdown.
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()

	c, err := smtp.NewClient(conn, host)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("smtp greeting: %w", err)
	}
	defer func() { _ = c.Close() }()

	if ok, _ := c.Extension("STARTTLS"); ok {
		if err := c.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}); err != nil {
			return fmt.Errorf("smtp starttls: %w", err)
		}
	}
	if m.user != "" && m.pass != "" {
		if err := c.Auth(smtp.PlainAuth("", m.user, m.pass, host)); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}
	if err := c.Mail(m.from); err != nil {
		return fmt.Errorf("smtp mail: %w", err)
	}
	if err := c.Rcpt(to); err != nil {
		if addressRefused(err) {
			return fmt.Errorf("smtp rcpt: %w: %w", ErrRecipientRejected, err)
		}
		return fmt.Errorf("smtp rcpt: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	if _, err := w.Write([]byte(msg)); err != nil {
		return fmt.Errorf("smtp write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp send: %w", err)
	}
	_ = c.Quit() // already accepted; a failed goodbye must not trigger a resend
	return nil
}

// addressRefused is true for replies about the mailbox itself; other 5xx (relay denied,
// policy, auth required) are our misconfiguration and must surface via retry/DLQ.
func addressRefused(err error) bool {
	var te *textproto.Error
	if !errors.As(err, &te) {
		return false
	}
	switch te.Code {
	case 501, 550, 551, 553:
		return true
	}
	return false
}
