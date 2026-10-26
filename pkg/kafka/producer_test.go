package kafka

import (
	"errors"
	"testing"

	"github.com/twmb/franz-go/pkg/kgo"
)

func TestAlignErrors_MapsByRecordNotByPosition(t *testing.T) {
	recs := []*kgo.Record{{Key: []byte("a")}, {Key: []byte("b")}, {Key: []byte("c")}}
	boom := errors.New("boom")
	// Results come back in completion order, and the last record never reports.
	results := kgo.ProduceResults{{Record: recs[1], Err: boom}, {Record: recs[0]}}

	errs := alignErrors(recs, results, "t")
	if errs[0] != nil {
		t.Errorf("errs[0] = %v, want nil", errs[0])
	}
	if !errors.Is(errs[1], boom) {
		t.Errorf("errs[1] = %v, want boom", errs[1])
	}
	if errs[2] == nil {
		t.Error("errs[2] = nil, want an error for a record without a result")
	}
}
