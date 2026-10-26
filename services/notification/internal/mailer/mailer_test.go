package mailer

import (
	"bufio"
	"context"
	"errors"
	"mime"
	"net"
	"net/mail"
	"strings"
	"sync"
	"testing"
	"time"
)

// stubSMTP speaks just enough SMTP for one message; rcptReply overrides the RCPT answer.
type stubSMTP struct {
	addr      string
	rcptReply string
	data      chan string
	auth      chan string
}

// listenSilent accepts connections and never speaks; they close with the test.
func listenSilent(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var conns []net.Conn
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, c)
			mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range conns {
			_ = c.Close()
		}
	})
	return ln.Addr().String()
}

func startStub(t *testing.T, rcptReply string) *stubSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	s := &stubSMTP{addr: ln.Addr().String(), rcptReply: rcptReply, data: make(chan string, 1), auth: make(chan string, 1)}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(conn)
		}
	}()
	return s
}

func (s *stubSMTP) serve(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	r := bufio.NewReader(conn)
	say := func(line string) { _, _ = conn.Write([]byte(line + "\r\n")) }
	say("220 stub ready")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		switch cmd := strings.ToUpper(strings.TrimSpace(line)); {
		case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
			say("250-stub")
			say("250 AUTH PLAIN")
		case strings.HasPrefix(cmd, "AUTH PLAIN"):
			s.auth <- strings.TrimSpace(line)
			say("235 ok")
		case strings.HasPrefix(cmd, "MAIL"):
			say("250 ok")
		case strings.HasPrefix(cmd, "RCPT"):
			say(s.rcptReply)
		case cmd == "DATA":
			say("354 go")
			var sb strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil || l == ".\r\n" {
					break
				}
				sb.WriteString(l)
			}
			s.data <- sb.String()
			say("250 queued")
		case cmd == "QUIT":
			say("221 bye")
			return
		}
	}
}

func TestSend_Delivers(t *testing.T) {
	stub := startStub(t, "250 ok")
	m := New(stub.addr, "noreply@todo.local", "", "")
	if err := m.Send(context.Background(), "a@example.com", "hi", "body text"); err != nil {
		t.Fatal(err)
	}
	got := <-stub.data
	if !strings.Contains(got, "Subject: hi") || !strings.Contains(got, "body text") {
		t.Errorf("message = %q", got)
	}
}

func TestSend_HungServerFailsWithinDeadline(t *testing.T) {
	m := New(listenSilent(t), "noreply@todo.local", "", "")
	m.timeout = 200 * time.Millisecond
	start := time.Now()
	err := m.Send(context.Background(), "a@example.com", "s", "b")
	if err == nil {
		t.Fatal("Send succeeded against a hung server")
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("Send took %v, want ~200ms", d)
	}
	if errors.Is(err, ErrRecipientRejected) {
		t.Error("a timeout must stay retryable")
	}
}

func TestSend_ContextCancelAbortsSession(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	m := New(listenSilent(t), "noreply@todo.local", "", "")
	start := time.Now()
	if err := m.Send(ctx, "a@example.com", "s", "b"); err == nil {
		t.Fatal("Send succeeded after cancel")
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("cancel took %v to take effect", d)
	}
}

func TestSend_RecipientRejection(t *testing.T) {
	cases := []struct {
		name, reply string
		permanent   bool
	}{
		{"unknown mailbox is permanent", "550 no such user", true},
		{"reply echoing the address", "550 5.1.1 <a@example.com>: Recipient address rejected", true},
		{"4xx is transient", "451 try later", false},
		{"relay denied is our config", "554 relay access denied", false},
		{"auth required is our config", "530 authentication required", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub := startStub(t, tc.reply)
			err := New(stub.addr, "noreply@todo.local", "", "").Send(context.Background(), "a@example.com", "s", "b")
			if err == nil {
				t.Fatal("expected error")
			}
			if got := errors.Is(err, ErrRecipientRejected); got != tc.permanent {
				t.Errorf("ErrRecipientRejected = %v, want %v (err: %v)", got, tc.permanent, err)
			}
			if strings.Contains(err.Error(), "a@example.com") {
				t.Errorf("error leaks the recipient address: %v", err)
			}
		})
	}
}

func TestSend_RejectsHeaderInjection(t *testing.T) {
	stub := startStub(t, "250 ok")
	m := New(stub.addr, "noreply@todo.local", "", "")

	err := m.Send(context.Background(), "a@example.com\r\nBcc: x@evil.com", "s", "b")
	if !errors.Is(err, ErrRecipientRejected) {
		t.Errorf("CRLF in address: err = %v, want ErrRecipientRejected", err)
	}

	if err := m.Send(context.Background(), "a@example.com", "Task\r\nBcc: x@evil.com", "b"); err != nil {
		t.Fatal(err)
	}
	if got := <-stub.data; strings.Contains(got, "\r\nBcc:") {
		t.Errorf("subject injected a header: %q", got)
	}
}

func TestSend_AuthenticatesWhenCredentialsSet(t *testing.T) {
	stub := startStub(t, "250 ok")
	m := New(stub.addr, "noreply@todo.local", "user", "secret")
	if err := m.Send(context.Background(), "a@example.com", "s", "b"); err != nil {
		t.Fatal(err)
	}
	// AUTH PLAIN base64("\x00user\x00secret")
	if got := <-stub.auth; !strings.HasSuffix(got, "AHVzZXIAc2VjcmV0") {
		t.Errorf("auth line = %q", got)
	}

	stub = startStub(t, "250 ok")
	if err := New(stub.addr, "noreply@todo.local", "", "").Send(context.Background(), "a@example.com", "s", "b"); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-stub.auth:
		t.Errorf("authenticated without credentials: %q", got)
	default:
	}
}

func TestSend_EncodesNonASCIISubject(t *testing.T) {
	stub := startStub(t, "250 ok")
	m := New(stub.addr, "noreply@todo.local", "", "")
	subject := "[todo] 任务已完成：写周报 ✓"
	if err := m.Send(context.Background(), "a@example.com", subject, "b"); err != nil {
		t.Fatal(err)
	}
	got := <-stub.data
	header, _, _ := strings.Cut(got, "\r\n\r\n")
	if !strings.Contains(header, "=?utf-8?q?") {
		t.Fatalf("subject not RFC 2047 encoded: %q", header)
	}
	for _, l := range strings.Split(header, "\r\n") {
		for _, r := range l {
			if r > 127 {
				t.Fatalf("raw non-ASCII in header line %q", l)
			}
		}
	}
	msg, err := mail.ReadMessage(strings.NewReader(got))
	if err != nil {
		t.Fatal(err)
	}
	dec, err := new(mime.WordDecoder).DecodeHeader(msg.Header.Get("Subject"))
	if err != nil || dec != subject {
		t.Errorf("decoded subject = %q, %v; want %q", dec, err, subject)
	}
}

func TestSend_FoldsLongEncodedSubject(t *testing.T) {
	stub := startStub(t, "250 ok")
	m := New(stub.addr, "noreply@todo.local", "", "")
	subject := strings.Repeat("任务已完成", 20)
	if err := m.Send(context.Background(), "a@example.com", subject, "b"); err != nil {
		t.Fatal(err)
	}
	got := <-stub.data
	header, _, _ := strings.Cut(got, "\r\n\r\n")
	if !strings.Contains(header, "?=\r\n =?") {
		t.Errorf("long subject was not folded: %q", header)
	}
	for _, l := range strings.Split(header, "\r\n") {
		if len(l) > 998 { // RFC 5322 hard limit
			t.Errorf("header line of %d bytes: %q", len(l), l)
		}
	}
	msg, err := mail.ReadMessage(strings.NewReader(got))
	if err != nil {
		t.Fatal(err)
	}
	if dec, err := new(mime.WordDecoder).DecodeHeader(msg.Header.Get("Subject")); err != nil || dec != subject {
		t.Errorf("decoded subject = %q, %v", dec, err)
	}
}
