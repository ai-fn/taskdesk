package scanner_test

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"

	"github.com/fsociety/todo/services/notification/internal/scanner"
)

// mockClamd starts a TCP listener that implements the INSTREAM protocol minimally:
// drain the command line + all frames until the zero-length terminator, then write response.
func mockClamd(t *testing.T, response string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		// Drain "nINSTREAM\n" command line (up to newline).
		// Read one byte at a time so we stop exactly at the newline and do
		// not consume bytes from the following length-prefixed frames when
		// the kernel coalesces the handshake with frame data into one segment.
		one := make([]byte, 1)
		for {
			n, err := conn.Read(one)
			if err != nil {
				return
			}
			if n > 0 && one[0] == '\n' {
				break
			}
		}

		// Drain frames until zero-length terminator.
		hdr := make([]byte, 4)
		for {
			if _, err := io.ReadFull(conn, hdr); err != nil {
				return
			}
			chunkLen := binary.BigEndian.Uint32(hdr)
			if chunkLen == 0 {
				break
			}
			if _, err := io.CopyN(io.Discard, conn, int64(chunkLen)); err != nil {
				return
			}
		}
		fmt.Fprint(conn, response+"\n")
	}()

	return ln.Addr().String()
}

func TestScan_Clean(t *testing.T) {
	addr := mockClamd(t, "stream: OK")
	s := scanner.New(addr)
	clean, vname, err := s.Scan(context.Background(), strings.NewReader("safe file content"))
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if !clean {
		t.Errorf("want clean=true, got false (virus=%q)", vname)
	}
	if vname != "" {
		t.Errorf("want empty virusName, got %q", vname)
	}
}

func TestScan_Infected(t *testing.T) {
	addr := mockClamd(t, "stream: Eicar-Signature FOUND")
	s := scanner.New(addr)
	clean, vname, err := s.Scan(context.Background(), strings.NewReader("X5O!P%@AP[4\\PZX54(P^)7CC)7}$EICAR"))
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if clean {
		t.Error("want clean=false for infected payload")
	}
	if vname != "Eicar-Signature" {
		t.Errorf("want virusName=%q, got %q", "Eicar-Signature", vname)
	}
}

func TestScan_ConnectRefused(t *testing.T) {
	s := scanner.New("127.0.0.1:1") // port 1 is not listening
	_, _, err := s.Scan(context.Background(), strings.NewReader("data"))
	if err == nil {
		t.Fatal("want error on unreachable addr, got nil")
	}
}

func TestScan_ClamdSizeLimit(t *testing.T) {
	addr := mockClamd(t, "INSTREAM size limit exceeded. ERROR")
	_, _, err := scanner.New(addr).Scan(context.Background(), strings.NewReader("data"))
	if !errors.Is(err, scanner.ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
}

func TestFromConfig_FailsClosedWithoutAddr(t *testing.T) {
	if _, err := scanner.FromConfig("", false); err == nil {
		t.Error("empty CLAMAV_ADDR without SCAN_DISABLED must be an error")
	}
	if s, err := scanner.FromConfig("", true); err != nil || s != nil {
		t.Errorf("explicitly disabled: got (%v, %v), want (nil, nil)", s, err)
	}
	if s, err := scanner.FromConfig("clamav:3310", false); err != nil || s == nil {
		t.Errorf("configured: got (%v, %v)", s, err)
	}
}

// clamd replies "size limit exceeded" and closes while the client is still sending.
func TestScan_ClamdClosesMidStream_IsTooLarge(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		buf := make([]byte, 64)
		_, _ = conn.Read(buf) // nINSTREAM + first frame start
		fmt.Fprint(conn, "INSTREAM size limit exceeded. ERROR\n")
		conn.Close()
	}()
	_, _, err = scanner.New(ln.Addr().String()).Scan(context.Background(), io.LimitReader(zeroReader{}, 64<<20))
	if !errors.Is(err, scanner.ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }
