// Package mailer sends email via SMTP (Mailpit in dev).
package mailer

import (
	"fmt"
	"net/smtp"
	"strings"
)

// Mailer sends plain-text email via SMTP.
type Mailer struct {
	addr string // host:port
	from string
}

func New(addr, from string) *Mailer {
	return &Mailer{addr: addr, from: from}
}

// Send sends a plain-text email. to is a comma-separated list if needed.
func (m *Mailer) Send(to, subject, body string) error {
	msg := strings.Join([]string{
		"From: " + m.from,
		"To: " + to,
		"Subject: " + subject,
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=utf-8",
		"",
		body,
	}, "\r\n")

	// No auth in dev — Mailpit accepts unauthenticated connections.
	if err := smtp.SendMail(m.addr, nil, m.from, []string{to}, []byte(msg)); err != nil {
		return fmt.Errorf("smtp send: %w", err)
	}
	return nil
}
