// Package enginetest provides a canned-result Querier for engine unit tests.
package enginetest

import (
	"context"
	"fmt"
	"strings"

	"github.com/deeprajsshetty/observability-platform/platform/internal/dbengine"
)

// Fake answers queries by the first registered fragment the SQL contains.
// Queries that match nothing fail, so tests notice unexpected SQL.
type Fake struct {
	Answers []Answer
	Seen    []string
}

// Answer is one canned result.
type Answer struct {
	Contains string
	Rows     []dbengine.Row
	Err      error
}

// Query implements dbengine.Querier.
func (f *Fake) Query(_ context.Context, q string, _ ...any) ([]dbengine.Row, error) {
	f.Seen = append(f.Seen, q)
	for _, a := range f.Answers {
		if strings.Contains(q, a.Contains) {
			return a.Rows, a.Err
		}
	}
	return nil, fmt.Errorf("enginetest: unexpected query: %.80s", q)
}
