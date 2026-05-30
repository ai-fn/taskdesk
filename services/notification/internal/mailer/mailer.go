// Package mailer sends email via SMTP (Mailpit in dev).
package mailer

import (
	"fmt"
	"net/smtp"
	"strings"
)

// Mailer sends plain-text email via SMTP.
// When user+pass are empty it connects unauthenticated (Mailpit / open relay).
// When both are set it uses smtp.PlainAuth (Gmail, SendGrid SMTP, etc.).
type Mailer struct {
	addr string // host:port
	from string
	user string
	pass string
}

func New(addr, from, user, pass string) *Mailer {
	return &Mailer{addr: addr, from: from, user: user, pass: pass}
}

// Send sends a plain-text email.
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

	var auth smtp.Auth
	if m.user != "" && m.pass != "" {
		host := m.addr[:strings.LastIndex(m.addr, ":")]
		auth = smtp.PlainAuth("", m.user, m.pass, host)
	}

	if err := smtp.SendMail(m.addr, auth, m.from, []string{to}, []byte(msg)); err != nil {
		return fmt.Errorf("smtp send: %w", err)
	}
	return nil
}
