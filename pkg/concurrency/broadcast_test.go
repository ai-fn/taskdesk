package concurrency

import "testing"

func TestTrySend_OpenBuffer(t *testing.T) {
	ch := make(chan int, 1)
	if !TrySend(ch, 1) {
		t.Fatal("send into empty buffer should succeed")
	}
	if got := <-ch; got != 1 {
		t.Fatalf("got %d, want 1", got)
	}
}

func TestTrySend_FullBuffer(t *testing.T) {
	ch := make(chan int, 1)
	if !TrySend(ch, 1) {
		t.Fatal("first send should succeed")
	}
	if TrySend(ch, 2) {
		t.Fatal("send into full buffer should fail, not block")
	}
	// The first value is still intact; nothing was overwritten or lost.
	if got := <-ch; got != 1 {
		t.Fatalf("got %d, want 1", got)
	}
}

func TestTrySend_UnbufferedNoReceiver(t *testing.T) {
	ch := make(chan int)
	if TrySend(ch, 1) {
		t.Fatal("unbuffered send with no ready receiver should fail")
	}
}
