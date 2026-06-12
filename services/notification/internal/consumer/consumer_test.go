package consumer

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/fsociety/todo/pkg/events"
	"github.com/twmb/franz-go/pkg/kgo"
)

// ── fakes for scan pipeline ───────────────────────────────────────────────────

type fakeScanResultUpdater struct {
	calls []struct {
		id     string
		status string
	}
	err error
}

func (f *fakeScanResultUpdater) UpdateScanStatus(_ context.Context, id, scanStatus string) error {
	f.calls = append(f.calls, struct {
		id, status string
	}{id, scanStatus})
	return f.err
}

type fakeScanner struct {
	clean     bool
	virusName string
	err       error
}

func (f *fakeScanner) Scan(_ context.Context, _ io.Reader) (bool, string, error) {
	return f.clean, f.virusName, f.err
}

type fakeBlobGetter struct {
	data []byte
	err  error
}

func (f *fakeBlobGetter) Get(_ context.Context, _ string) (io.ReadCloser, error) {
	if f.err != nil {
		return nil, f.err
	}
	return io.NopCloser(bytes.NewReader(f.data)), nil
}

func fileUploadedRecord(attID, objectKey string) *kgo.Record {
	ev := events.FileEvent{
		EventType:    events.TypeFileUploaded,
		AttachmentID: attID,
		ObjectKey:    objectKey,
		UserID:       "u1",
	}
	payload, _ := json.Marshal(ev)
	return &kgo.Record{Topic: events.FileTopic, Value: payload}
}

// ── handleFileUploaded tests ──────────────────────────────────────────────────

func TestHandleFileUploaded_NilScanner_ApprovesImmediately(t *testing.T) {
	upd := &fakeScanResultUpdater{}
	h := &Handler{
		scanner:     nil,
		scanUpdater: upd,
		log:         slog.Default(),
	}
	if err := h.handleFileUploaded(context.Background(), fileUploadedRecord("att-1", "key/1")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(upd.calls) != 1 {
		t.Fatalf("want 1 UpdateScanStatus call, got %d", len(upd.calls))
	}
	if upd.calls[0].status != events.ScanStatusClean {
		t.Errorf("want status=%q, got %q", events.ScanStatusClean, upd.calls[0].status)
	}
}

func TestHandleFileUploaded_Clean(t *testing.T) {
	upd := &fakeScanResultUpdater{}
	h := &Handler{
		scanner:     &fakeScanner{clean: true},
		blob:        &fakeBlobGetter{data: []byte("safe")},
		scanUpdater: upd,
		log:         slog.Default(),
	}
	if err := h.handleFileUploaded(context.Background(), fileUploadedRecord("att-2", "key/2")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if upd.calls[0].status != events.ScanStatusClean {
		t.Errorf("want clean, got %q", upd.calls[0].status)
	}
}

func TestHandleFileUploaded_Infected(t *testing.T) {
	upd := &fakeScanResultUpdater{}
	h := &Handler{
		scanner:     &fakeScanner{clean: false, virusName: "Trojan.Generic"},
		blob:        &fakeBlobGetter{data: []byte("malware bytes")},
		scanUpdater: upd,
		log:         slog.Default(),
	}
	if err := h.handleFileUploaded(context.Background(), fileUploadedRecord("att-3", "key/3")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if upd.calls[0].status != events.ScanStatusQuarantined {
		t.Errorf("want quarantined, got %q", upd.calls[0].status)
	}
	if upd.calls[0].id != "att-3" {
		t.Errorf("want attachment_id=att-3, got %q", upd.calls[0].id)
	}
}

func TestHandleFileUploaded_IgnoresNonUploadedEvents(t *testing.T) {
	upd := &fakeScanResultUpdater{}
	h := &Handler{
		scanner:     &fakeScanner{clean: true},
		blob:        &fakeBlobGetter{data: []byte("data")},
		scanUpdater: upd,
		log:         slog.Default(),
	}
	ev := events.FileEvent{EventType: "file.deleted", AttachmentID: "att-4"}
	payload, _ := json.Marshal(ev)
	rec := &kgo.Record{Topic: events.FileTopic, Value: payload}
	if err := h.handleFileUploaded(context.Background(), rec); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(upd.calls) != 0 {
		t.Errorf("want 0 scan calls for non-uploaded event, got %d", len(upd.calls))
	}
}

// The three retryable paths must return a non-nil error so the Kafka record is
// redelivered (at-least-once delivery), and must not report a verdict.

func TestHandleFileUploaded_BlobError_Retryable(t *testing.T) {
	upd := &fakeScanResultUpdater{}
	h := &Handler{
		scanner:     &fakeScanner{clean: true},
		blob:        &fakeBlobGetter{err: io.ErrUnexpectedEOF},
		scanUpdater: upd,
		log:         slog.Default(),
	}
	if err := h.handleFileUploaded(context.Background(), fileUploadedRecord("att-b", "key/b")); err == nil {
		t.Fatal("want non-nil error on blob download failure (redeliver), got nil")
	}
	if len(upd.calls) != 0 {
		t.Errorf("want 0 scan-status calls when download fails, got %d", len(upd.calls))
	}
}

func TestHandleFileUploaded_ScannerError_Retryable(t *testing.T) {
	upd := &fakeScanResultUpdater{}
	h := &Handler{
		scanner:     &fakeScanner{err: io.ErrUnexpectedEOF},
		blob:        &fakeBlobGetter{data: []byte("data")},
		scanUpdater: upd,
		log:         slog.Default(),
	}
	if err := h.handleFileUploaded(context.Background(), fileUploadedRecord("att-s", "key/s")); err == nil {
		t.Fatal("want non-nil error on clamd scan failure (redeliver), got nil")
	}
	if len(upd.calls) != 0 {
		t.Errorf("want 0 scan-status calls when scan fails, got %d", len(upd.calls))
	}
}

func TestHandleFileUploaded_UpdaterError_Retryable(t *testing.T) {
	upd := &fakeScanResultUpdater{err: io.ErrUnexpectedEOF}
	h := &Handler{
		scanner:     &fakeScanner{clean: true},
		blob:        &fakeBlobGetter{data: []byte("safe")},
		scanUpdater: upd,
		log:         slog.Default(),
	}
	if err := h.handleFileUploaded(context.Background(), fileUploadedRecord("att-u", "key/u")); err == nil {
		t.Fatal("want non-nil error when UpdateScanStatus callback fails (redeliver), got nil")
	}
	if len(upd.calls) != 1 {
		t.Errorf("want 1 attempted scan-status call, got %d", len(upd.calls))
	}
}

func TestPickTaskCompletedTmpl_KnownLang(t *testing.T) {
	data := map[string]string{"Title": "Buy groceries", "Date": "2026-05-29 10:00"}
	for _, lang := range []string{"en", "zh"} {
		tmpl := pickTaskCompletedTmpl(lang)
		var buf strings.Builder
		if err := tmpl.Subject.Execute(&buf, data); err != nil {
			t.Errorf("lang=%s subject execute: %v", lang, err)
		}
		if buf.Len() == 0 {
			t.Errorf("lang=%s subject is empty", lang)
		}
	}
}

func TestPickTaskCompletedTmpl_UnknownFallsBackToEn(t *testing.T) {
	en := pickTaskCompletedTmpl("en")
	unknown := pickTaskCompletedTmpl("fr")
	data := map[string]string{"Title": "T", "Date": "D"}

	var enBuf, unknownBuf strings.Builder
	_ = en.Subject.Execute(&enBuf, data)
	_ = unknown.Subject.Execute(&unknownBuf, data)

	if enBuf.String() != unknownBuf.String() {
		t.Errorf("unknown lang did not fall back to en: en=%q unknown=%q", enBuf.String(), unknownBuf.String())
	}
}

func TestBuildPlanEmail_ZhSubjectContainsZH(t *testing.T) {
	ev := DailyPlanEvent{
		Summary:     "今天的计划",
		GeneratedAt: time.Date(2026, 5, 29, 0, 0, 0, 0, time.UTC),
		Language:    "zh",
	}
	subject, body := buildPlanEmail(ev)
	if !strings.Contains(subject, "2026-05-29") {
		t.Errorf("ZH subject missing date: %q", subject)
	}
	if !strings.Contains(body, "今天的计划") {
		t.Errorf("ZH body missing summary: %q", body)
	}
}

func TestBuildPlanEmail_UnknownLangFallsBackToEn(t *testing.T) {
	ev := DailyPlanEvent{
		Summary:     "Today's plan",
		GeneratedAt: time.Date(2026, 5, 29, 0, 0, 0, 0, time.UTC),
		Language:    "xx", // unknown
	}
	evEn := ev
	evEn.Language = "en"

	subject, _ := buildPlanEmail(ev)
	subjectEn, _ := buildPlanEmail(evEn)

	if subject != subjectEn {
		t.Errorf("unknown lang did not fall back: xx=%q en=%q", subject, subjectEn)
	}
}
