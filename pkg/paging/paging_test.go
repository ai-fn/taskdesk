package paging_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/fsociety/todo/pkg/paging"
)

func pages(m map[string][]int, next map[string]string) func(context.Context, string) ([]int, string, error) {
	return func(_ context.Context, token string) ([]int, string, error) { return m[token], next[token], nil }
}

func TestAll_DrainsEveryPageInOrder(t *testing.T) {
	got, err := paging.All(context.Background(), pages(
		map[string][]int{"": {1, 2}, "a": {3}, "b": {}},
		map[string]string{"": "a", "a": "b"},
	))
	if err != nil || !reflect.DeepEqual(got, []int{1, 2, 3}) {
		t.Errorf("All = %v, %v; want [1 2 3]", got, err)
	}
}

func TestAll_SinglePage(t *testing.T) {
	got, err := paging.All(context.Background(), pages(map[string][]int{"": {7}}, nil))
	if err != nil || !reflect.DeepEqual(got, []int{7}) {
		t.Errorf("All = %v, %v", got, err)
	}
}

func TestAll_FetchErrorIsReturned(t *testing.T) {
	boom := errors.New("boom")
	_, err := paging.All(context.Background(), func(context.Context, string) ([]int, string, error) { return nil, "", boom })
	if !errors.Is(err, boom) {
		t.Errorf("err = %v, want boom", err)
	}
}

func TestAll_RepeatedTokenFailsInsteadOfLooping(t *testing.T) {
	_, err := paging.All(context.Background(), func(context.Context, string) ([]int, string, error) { return []int{1}, "same", nil })
	if err == nil {
		t.Fatal("expected an error for a token that never advances")
	}
}
