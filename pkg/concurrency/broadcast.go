package concurrency

// TrySend attempts a non-blocking send of v on ch and reports whether it
// succeeded. It returns false when the channel's buffer is full (or the channel
// is unbuffered and no receiver is ready), without ever blocking the caller.
//
// This is the slow-consumer signal for fan-out hubs: a full buffer means the
// receiver is not keeping up, and the caller can evict it instead of blocking
// the broadcaster. Sending on a closed channel still panics, as with any send —
// callers must own the channel's lifecycle.
func TrySend[T any](ch chan<- T, v T) bool {
	select {
	case ch <- v:
		return true
	default:
		return false
	}
}
