package store_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/fsociety/todo/services/todo/internal/domain"
	"github.com/fsociety/todo/services/todo/internal/store"
)

// A→B and B→A racing must not both commit: the pair would block each other forever.
func TestAddDependency_OppositeEdgesRacingNeverFormACycle(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	const rounds = 40 // the race is probabilistic; many independent pairs make it reliable
	for i := range rounds {
		a, b := fmt.Sprintf("a%02d", i), fmt.Sprintf("b%02d", i)
		seed(t, s, "u", a, domain.StatusTodo, "")
		seed(t, s, "u", b, domain.StatusTodo, "")

		start := make(chan struct{})
		errs := make([]error, 2)
		var wg sync.WaitGroup
		for k, edge := range [][2]string{{a, b}, {b, a}} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				errs[k] = s.AddDependency(ctx, "u", edge[0], edge[1])
			}()
		}
		close(start)
		wg.Wait()

		ok := 0
		for _, err := range errs {
			switch {
			case err == nil:
				ok++
			case !errors.Is(err, store.ErrCycleDetected):
				t.Fatalf("round %d: unexpected error %v", i, err)
			}
		}
		if ok != 1 {
			t.Fatalf("round %d: %d of the opposite edges committed (errs %v), want exactly 1", i, ok, errs)
		}
	}
}
