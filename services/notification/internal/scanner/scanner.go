// Package scanner streams bytes to a ClamAV daemon and reports the result.
package scanner

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"
)

const (
	chunkSize   = 4096
	dialTimeout = 10 * time.Second
	scanTimeout = 5 * time.Minute
)

// ErrTooLarge means clamd refused the stream because it exceeds its StreamMaxLength.
var ErrTooLarge = errors.New("object exceeds clamd StreamMaxLength")

// FromConfig builds the scanner for addr. An empty addr is only allowed when scanning is explicitly
// disabled (dev); otherwise unscanned files would be approved silently, so it is an error.
func FromConfig(addr string, disabled bool) (*Scanner, error) {
	switch {
	case addr != "":
		return New(addr), nil
	case disabled:
		return nil, nil
	}
	return nil, errors.New("CLAMAV_ADDR is not set; set SCAN_DISABLED=true to approve files without scanning (dev only)")
}

// Scanner sends data to a running clamd over the INSTREAM TCP protocol.
type Scanner struct {
	addr string
}

// New returns a Scanner that connects to clamd at addr (e.g. "clamav:3310").
func New(addr string) *Scanner {
	return &Scanner{addr: addr}
}

// Scan streams r to clamd.
// Returns clean=true when no virus is found.
// When a virus is detected, clean=false and virusName is populated.
func (s *Scanner) Scan(ctx context.Context, r io.Reader) (clean bool, virusName string, err error) {
	// The caller's deadline wins so it can scale with object size; scanTimeout is only the fallback.
	deadline := time.Now().Add(scanTimeout)
	if d, ok := ctx.Deadline(); ok {
		deadline = d
	}

	conn, err := net.DialTimeout("tcp", s.addr, dialTimeout)
	if err != nil {
		return false, "", fmt.Errorf("connect to clamd at %s: %w", s.addr, err)
	}
	defer conn.Close()
	conn.SetDeadline(deadline) //nolint:errcheck

	if _, err := fmt.Fprint(conn, "nINSTREAM\n"); err != nil {
		return false, "", fmt.Errorf("clamd handshake: %w", err)
	}

	buf := make([]byte, chunkSize)
	var hdr [4]byte
	for {
		n, readErr := r.Read(buf)
		if n > 0 {
			binary.BigEndian.PutUint32(hdr[:], uint32(n))
			if _, err := conn.Write(hdr[:]); err != nil {
				return false, "", writeFailure(conn, "clamd write chunk header", err)
			}
			if _, err := conn.Write(buf[:n]); err != nil {
				return false, "", writeFailure(conn, "clamd write chunk body", err)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return false, "", fmt.Errorf("read source: %w", readErr)
		}
	}

	// Zero-length chunk signals end of stream.
	if _, err := conn.Write([]byte{0, 0, 0, 0}); err != nil {
		return false, "", fmt.Errorf("clamd write terminator: %w", err)
	}

	sc := bufio.NewScanner(conn)
	if !sc.Scan() {
		if e := sc.Err(); e != nil {
			return false, "", fmt.Errorf("clamd read response: %w", e)
		}
		return false, "", fmt.Errorf("clamd closed without response")
	}
	line := strings.TrimSpace(sc.Text())

	switch {
	case strings.HasSuffix(line, " OK"):
		return true, "", nil
	case strings.HasSuffix(line, " FOUND"):
		// "stream: <VirusName> FOUND"
		parts := strings.SplitN(line, ": ", 2)
		name := ""
		if len(parts) == 2 {
			name = strings.TrimSuffix(parts[1], " FOUND")
		}
		return false, name, nil
	case strings.Contains(line, "size limit exceeded"):
		return false, "", ErrTooLarge
	default:
		return false, "", fmt.Errorf("clamd unexpected response: %q", line)
	}
}

// writeFailure wraps a write error. clamd closes the connection after replying "size limit exceeded",
// so the reply is read back to tell that permanent refusal apart from a transient network error.
func writeFailure(conn net.Conn, what string, err error) error {
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if line, rerr := bufio.NewReader(conn).ReadString('\n'); rerr == nil && strings.Contains(line, "size limit exceeded") {
		return ErrTooLarge
	}
	return fmt.Errorf("%s: %w", what, err)
}
