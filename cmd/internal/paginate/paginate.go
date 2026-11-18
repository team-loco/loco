// Package paginate reads every page of a list RPC.
package paginate

import (
	"errors"
	"fmt"
)

var ErrRepeatedPageToken = errors.New("listing returned a repeated page token")

// All calls fetch with each page token until it returns an empty next
// token, so a client never reads only the first page of a list RPC.
func All[T any](fetch func(token string) ([]T, string, error)) ([]T, error) {
	var all []T
	var token string
	seen := make(map[string]struct{})
	for {
		items, next, err := fetch(token)
		if err != nil {
			return nil, err
		}
		all = append(all, items...)
		if next == "" {
			return all, nil
		}
		if _, duplicate := seen[next]; duplicate {
			return nil, fmt.Errorf("%w: %q", ErrRepeatedPageToken, next)
		}
		seen[next] = struct{}{}
		token = next
	}
}
