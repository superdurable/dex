package backoff

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/superdurable/dex/server/common/errors"
)

func TestDo_SucceedsOnFirstAttempt(t *testing.T) {
	r := NewRetry(
		WithRetryPolicy(&RetryPolicy{
			InitialInterval: time.Millisecond,
			MaximumAttempts: 3,
		}),
		WithRetryableError(AlwaysRetry),
		WithClock(newFakeClock()),
	)

	calls := 0
	err := r.Do(context.Background(), func(ctx context.Context) error {
		calls++
		return nil
	})
	if err != nil {
		t.Errorf("got error %v, want nil", err)
	}
	if calls != 1 {
		t.Errorf("called %d times, want 1", calls)
	}
}

func TestDo_RetriesUntilSuccess(t *testing.T) {
	r := NewRetry(
		WithRetryPolicy(&RetryPolicy{
			InitialInterval: time.Millisecond,
			MaximumAttempts: 5,
		}),
		WithRetryableError(AlwaysRetry),
		WithClock(newFakeClock()),
	)

	calls := 0
	err := r.Do(context.Background(), func(ctx context.Context) error {
		calls++
		if calls < 3 {
			return fmt.Errorf("fail #%d", calls)
		}
		return nil
	})
	if err != nil {
		t.Errorf("got error %v, want nil", err)
	}
	if calls != 3 {
		t.Errorf("called %d times, want 3", calls)
	}
}

func TestDo_StopsAfterMaxAttempts(t *testing.T) {
	r := NewRetry(
		WithRetryPolicy(&RetryPolicy{
			InitialInterval: time.Millisecond,
			MaximumAttempts: 3,
		}),
		WithRetryableError(AlwaysRetry),
		WithClock(newFakeClock()),
	)

	calls := 0
	err := r.Do(context.Background(), func(ctx context.Context) error {
		calls++
		return fmt.Errorf("fail #%d", calls)
	})
	if err == nil {
		t.Error("got nil, want an error")
	}
	// MaximumAttempts=3 includes the first call → 3 total
	if calls != 3 {
		t.Errorf("called %d times, want 3", calls)
	}
}

func TestDo_NonRetryableErrorStopsImmediately(t *testing.T) {
	r := NewRetry(
		WithRetryPolicy(&RetryPolicy{
			InitialInterval: time.Millisecond,
			MaximumAttempts: 10,
		}),
		WithRetryableError(func(err error) bool {
			return false
		}),
		WithClock(newFakeClock()),
	)

	calls := 0
	err := r.Do(context.Background(), func(ctx context.Context) error {
		calls++
		return fmt.Errorf("not retryable")
	})
	if err == nil {
		t.Error("got nil, want an error")
	}
	if calls != 1 {
		t.Errorf("called %d times, want 1", calls)
	}
}

func TestDo_DefaultPolicyDoesNotRetry(t *testing.T) {
	// Default isRetryable returns false for all errors.
	r := NewRetry(WithClock(newFakeClock()))

	calls := 0
	err := r.Do(context.Background(), func(ctx context.Context) error {
		calls++
		return fmt.Errorf("fail")
	})
	if err == nil {
		t.Error("got nil, want an error")
	}
	if calls != 1 {
		t.Errorf("called %d times, want 1 (default does not retry)", calls)
	}
}

func TestDo_RespectsContextCancellation(t *testing.T) {
	r := NewRetry(
		WithRetryPolicy(&RetryPolicy{
			InitialInterval: time.Millisecond,
		}),
		WithRetryableError(AlwaysRetry),
		WithClock(newFakeClock()),
	)

	ctx, cancel := context.WithCancel(context.Background())

	calls := 0
	err := r.Do(ctx, func(ctx context.Context) error {
		calls++
		if calls >= 2 {
			cancel()
		}
		return fmt.Errorf("fail #%d", calls)
	})
	if err == nil {
		t.Error("got nil, want an error")
	}
	// Should have stopped reasonably soon after context was cancelled at call 2.
	// With a fake clock the backoff sleeps resolve instantly, so a few extra
	// iterations can slip through before ctx.Err() is observed.
	if calls > 10 {
		t.Errorf("called %d times, expected <=10 (context cancelled after 2)", calls)
	}
}

func TestDoCategorized_RetriesRetriableError(t *testing.T) {
	r := NewRetry(
		WithRetryPolicy(&RetryPolicy{
			InitialInterval: time.Millisecond,
			MaximumAttempts: 5,
		}),
		WithClock(newFakeClock()),
	)

	calls := 0
	err := r.DoCategorized(context.Background(), func(ctx context.Context) errors.CategorizedError {
		calls++
		if calls < 3 {
			// Timeout errors are retriable
			return errors.NewTimeoutError(fmt.Sprintf("timeout #%d", calls), nil)
		}
		return nil
	})
	if err != nil {
		t.Errorf("got error %v, want nil", err)
	}
	if calls != 3 {
		t.Errorf("called %d times, want 3", calls)
	}
}

func TestDoCategorized_NonRetriableErrorStopsImmediately(t *testing.T) {
	r := NewRetry(
		WithRetryPolicy(&RetryPolicy{
			InitialInterval: time.Millisecond,
			MaximumAttempts: 10,
		}),
		WithClock(newFakeClock()),
	)

	calls := 0
	err := r.DoCategorized(context.Background(), func(ctx context.Context) errors.CategorizedError {
		calls++
		// InvalidInput is not retriable
		return errors.NewInvalidInputError("bad input", nil)
	})
	if err == nil {
		t.Error("got nil, want an error")
	}
	if calls != 1 {
		t.Errorf("called %d times, want 1", calls)
	}
}

func TestDoCategorized_StopsAfterMaxAttempts(t *testing.T) {
	r := NewRetry(
		WithRetryPolicy(&RetryPolicy{
			InitialInterval: time.Millisecond,
			MaximumAttempts: 3,
		}),
		WithClock(newFakeClock()),
	)

	calls := 0
	err := r.DoCategorized(context.Background(), func(ctx context.Context) errors.CategorizedError {
		calls++
		return errors.NewTimeoutError("timeout", nil)
	})
	if err == nil {
		t.Error("got nil, want an error")
	}
	// MaximumAttempts=3 includes the first call → 3 total
	if calls != 3 {
		t.Errorf("called %d times, want 3", calls)
	}
}

func TestAlwaysRetry_ReturnsTrue(t *testing.T) {
	if !AlwaysRetry(fmt.Errorf("any error")) {
		t.Error("AlwaysRetry should return true for all errors")
	}
	if !AlwaysRetry(nil) {
		t.Error("AlwaysRetry should return true even for nil")
	}
}
