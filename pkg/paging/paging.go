// Package paging drains token-paginated listings.
package paging

import (
	"context"
	"fmt"
)

// All calls fetch until it returns an empty next token. A repeated token is an error, as it
// would loop forever.
func All[T any](ctx context.Context, fetch func(ctx context.Context, token string) (items []T, next string, err error)) ([]T, error) {
	var out []T
	token := ""
	for {
		items, next, err := fetch(ctx, token)
		if err != nil {
			return nil, err
		}
		out = append(out, items...)
		if next == "" {
			return out, nil
		}
		if next == token {
			return nil, fmt.Errorf("paging: token %q repeated", next)
		}
		token = next
	}
}
