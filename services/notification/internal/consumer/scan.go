package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/fsociety/todo/pkg/events"
	"github.com/fsociety/todo/services/notification/internal/scanner"
	"github.com/twmb/franz-go/pkg/kgo"
)

// ScannerClient abstracts the ClamAV scanner so tests can inject fakes.
type ScannerClient interface {
	Scan(ctx context.Context, r io.Reader) (clean bool, virusName string, err error)
}

// BlobGetter downloads a single object from object storage by key.
type BlobGetter interface {
	Get(ctx context.Context, key string) (io.ReadCloser, error)
}

// ScanResultUpdater reports a scan outcome back to todo-service.
type ScanResultUpdater interface {
	UpdateScanStatus(ctx context.Context, attachmentID, scanStatus string) error
}

const (
	scanBaseTimeout = 5 * time.Minute
	// Assumed worst-case throughput when sizing the per-object scan deadline.
	scanMinBytesPerSec = 8 << 20
)

// ScanTopics returns the topics of the malware-scan pipeline, consumed in their own group.
func ScanTopics() []string { return []string{events.FileTopic} }

// WithScanLimit quarantines, unscanned, any object above max bytes (clamd cannot take larger streams).
func (h *Handler) WithScanLimit(max int64) *Handler {
	h.scanMaxBytes = max
	return h
}

// scanDeadline grows with object size so a big upload is not cut off by a fixed timeout.
func scanDeadline(size int64) time.Duration {
	return scanBaseTimeout + time.Duration(size/scanMinBytesPerSec)*time.Second
}

// ── file events (malware scan pipeline) ──────────────────────────────────────

func (h *Handler) handleFileUploaded(ctx context.Context, r *kgo.Record) error {
	var ev events.FileEvent
	if err := json.Unmarshal(r.Value, &ev); err != nil {
		h.log.Error("decode file event", "err", err)
		return nil // poison pill — skip
	}
	if ev.EventType != events.TypeFileUploaded {
		return nil
	}

	// Scanner not configured (SCAN_DISABLED, dev only): approve immediately.
	if h.scanner == nil {
		return h.setScanStatus(ctx, ev.AttachmentID, events.ScanStatusClean)
	}

	// Fail closed: an object clamd cannot take is never approved unscanned.
	if h.scanMaxBytes > 0 && ev.SizeBytes > h.scanMaxBytes {
		h.log.Warn("object too large to scan; quarantining",
			"attachment_id", ev.AttachmentID, "size", ev.SizeBytes, "limit", h.scanMaxBytes)
		return h.setScanStatus(ctx, ev.AttachmentID, events.ScanStatusQuarantined)
	}

	// A configured scanner with no blob getter is a wiring error; fail loudly
	// rather than nil-panic the consumer goroutine. Same discipline as the
	// nil-scanner guard above.
	if h.blob == nil {
		h.log.Error("scanner configured but blob getter is nil; cannot download for scan",
			"attachment_id", ev.AttachmentID)
		return fmt.Errorf("blob getter not configured")
	}

	body, err := h.blob.Get(ctx, ev.ObjectKey)
	if err != nil {
		h.log.Error("download for scan", "attachment_id", ev.AttachmentID, "err", err)
		return err // retryable
	}
	defer func() { _ = body.Close() }()

	scanCtx, cancel := context.WithTimeout(ctx, scanDeadline(ev.SizeBytes))
	defer cancel()
	clean, virusName, err := h.scanner.Scan(scanCtx, body)
	if errors.Is(err, scanner.ErrTooLarge) {
		h.log.Warn("clamd refused object as too large; quarantining", "attachment_id", ev.AttachmentID, "size", ev.SizeBytes)
		return h.setScanStatus(ctx, ev.AttachmentID, events.ScanStatusQuarantined)
	}
	if err != nil {
		h.log.Error("clamd scan failed", "attachment_id", ev.AttachmentID, "err", err)
		return err // retryable — consumer will redeliver
	}

	scanStatus := events.ScanStatusClean
	if !clean {
		scanStatus = events.ScanStatusQuarantined
		h.log.Warn("malware detected",
			"attachment_id", ev.AttachmentID,
			"virus", virusName,
			"user_id", ev.UserID,
		)
	}
	return h.setScanStatus(ctx, ev.AttachmentID, scanStatus)
}

// setScanStatus reports the verdict to todo-service. A failure here returns an
// error so the Kafka record is redelivered. Note: redelivery re-runs the whole
// handler, re-downloading and re-scanning the object. That is wasteful during a
// todo-service outage but acceptable — scans are idempotent (no duplicate DB
// side effects: the same verdict is written again), and outages are expected to
// be brief. If scan amplification becomes a problem, persist the verdict locally
// (side-table or DLQ) so retries only re-attempt the callback, not the scan.
func (h *Handler) setScanStatus(ctx context.Context, attachmentID, scanStatus string) error {
	if err := h.scanUpdater.UpdateScanStatus(ctx, attachmentID, scanStatus); err != nil {
		h.log.Error("update scan status", "attachment_id", attachmentID, "status", scanStatus, "err", err)
		return err
	}
	h.log.Info("scan complete", "attachment_id", attachmentID, "status", scanStatus)
	return nil
}
