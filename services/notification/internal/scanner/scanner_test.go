package scanner_test

import (
	"context"
	"encoding/binary"
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
