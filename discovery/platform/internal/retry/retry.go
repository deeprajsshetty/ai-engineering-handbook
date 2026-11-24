// Package retry runs idempotent operations with capped exponential backoff and
// full jitter. Only use it for operations that are safe to repeat, such as
// read-only provider API calls; never for writes without an idempotency key.
package retry

import (
	"context"
	"errors"
	"math/rand/v2"
	"time"
)

// Policy configures retries.
type Policy struct {
	MaxAttempts int           // total attempts including the first
	BaseDelay   time.Duration // delay before the second attempt (before jitter)
	MaxDelay    time.Duration // cap per wait
}

// Default is used for provider page fetches.
var Default = Policy{MaxAttempts: 4, BaseDelay: 200 * time.Millisecond, MaxDelay: 5 * time.Second}

// retryable marks errors that may succeed on retry.
type retryable struct {
	err        error
	retryAfter time.Duration
}

func (r retryable) Error() string { return r.err.Error() }
func (r retryable) Unwrap() error { return r.err }

// Retryable wraps err as retryable; retryAfter (may be 0) is a server hint.
func Retryable(err error, retryAfter time.Duration) error {
	if err == nil {
		return nil
	}
	return retryable{err: err, retryAfter: retryAfter}
}

// IsRetryable reports whether err was marked retryable.
func IsRetryable(err error) bool {
	var r retryable
	return errors.As(err, &r)
}

// Do calls fn until it succeeds, returns a non-retryable error, the attempts
// are exhausted or ctx is done. onRetry, if set, is called before each wait.
func Do(ctx context.Context, p Policy, fn func(ctx context.Context) error, onRetry func(attempt int, err error, wait time.Duration)) error {
	if p.MaxAttempts < 1 {
		p.MaxAttempts = 1
	}
	var err error
	for attempt := 1; ; attempt++ {
		if err = ctx.Err(); err != nil {
			return err
		}
		err = fn(ctx)
		if err == nil || !IsRetryable(err) || attempt >= p.MaxAttempts {
			return err
		}
		wait := Backoff(p, attempt)
		var r retryable
		if errors.As(err, &r) && r.retryAfter > wait {
			wait = min(r.retryAfter, p.MaxDelay)
		}
		if onRetry != nil {
			onRetry(attempt, err, wait)
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
}

// Backoff returns the full-jitter wait after the given attempt (1-based):
// uniform in [0, min(MaxDelay, BaseDelay*2^(attempt-1))].
func Backoff(p Policy, attempt int) time.Duration {
	ceil := p.BaseDelay << (attempt - 1)
	if ceil <= 0 || ceil > p.MaxDelay {
		ceil = p.MaxDelay
	}
	return time.Duration(rand.Int64N(int64(ceil) + 1))
}
