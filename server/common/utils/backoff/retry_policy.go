// Copyright (c) 2017 Uber Technologies, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package backoff

import (
	"math"
	"math/rand"
	"time"

	"github.com/superdurable/dex/server/common/utils/clock"
)

const (
	done time.Duration = -1

	defaultInitialInterval    = 1 * time.Second
	defaultBackoffCoefficient = 2.0
	defaultMaximumInterval    = 10 * time.Second
)

// RetryPolicy configures exponential backoff retry behavior.
//
// The delay before attempt N is computed as:
//
//	InitialInterval * BackoffCoefficient^N
//
// capped at MaximumInterval. A 20% jitter is applied to each delay to
// avoid thundering-herd effects.
//
// Zero-valued fields fall back to sensible defaults:
//   - InitialInterval:    1s
//   - BackoffCoefficient: 2.0
//   - MaximumInterval:    10s
//   - TotalTimeout:       0 (no timeout — retries bounded only by MaximumAttempts)
//   - MaximumAttempts:    0 (no limit — retries bounded only by TotalTimeout)
//
// Example — try up to 5 times with 100ms initial delay:
//
//	&backoff.RetryPolicy{
//	    InitialInterval: 100 * time.Millisecond,
//	    MaximumAttempts: 5,
//	}
//
// Example — retry for at most 1 hour with 30s max delay:
//
//	&backoff.RetryPolicy{
//	    InitialInterval: 100 * time.Millisecond,
//	    MaximumInterval: 30 * time.Second,
//	    TotalTimeout:    1 * time.Hour,
//	}
type RetryPolicy struct {
	// InitialInterval is the delay before the first retry.
	// Default: 1s.
	InitialInterval time.Duration

	// BackoffCoefficient is the multiplier applied after each consecutive
	// failure. For example, 2.0 doubles the delay each retry.
	// Default: 2.0.
	BackoffCoefficient float64

	// MaximumInterval caps the retry delay so it never exceeds this value,
	// regardless of the backoff coefficient.
	// Default: 10s.
	MaximumInterval time.Duration

	// TotalTimeout is the end-to-end deadline for the entire retry sequence,
	// measured from the start of the first attempt. Once elapsed the
	// operation fails permanently, even if MaximumAttempts has not been
	// reached. Zero means no total timeout.
	TotalTimeout time.Duration

	// MaximumAttempts is the maximum number of attempts including the first
	// execution. For example, MaximumAttempts=3 means 1 initial attempt + 2
	// retries = 3 total calls. Zero means unlimited (bounded only by
	// TotalTimeout).
	MaximumAttempts int
}

func (p *RetryPolicy) getInitialInterval() time.Duration {
	if p.InitialInterval != 0 {
		return p.InitialInterval
	}
	return defaultInitialInterval
}

func (p *RetryPolicy) getBackoffCoefficient() float64 {
	if p.BackoffCoefficient != 0 {
		return p.BackoffCoefficient
	}
	return defaultBackoffCoefficient
}

func (p *RetryPolicy) getMaximumInterval() time.Duration {
	if p.MaximumInterval != 0 {
		return p.MaximumInterval
	}
	return defaultMaximumInterval
}

// computeNextDelay returns the next delay interval.
// A return value of done (-1) signals that retries should stop.
func (p *RetryPolicy) computeNextDelay(elapsedTime time.Duration, numAttempts int) time.Duration {
	initInterval := p.getInitialInterval()
	coefficient := p.getBackoffCoefficient()
	maxInterval := p.getMaximumInterval()

	// Check to see if we ran out of maximum number of attempts.
	// numAttempts is 0-indexed retry count; MaximumAttempts includes the
	// first execution, so we allow MaximumAttempts-1 retries.
	if p.MaximumAttempts != 0 && numAttempts >= p.MaximumAttempts-1 {
		return done
	}

	// Stop retrying after total timeout is elapsed
	if p.TotalTimeout != 0 && elapsedTime > p.TotalTimeout {
		return done
	}

	nextInterval := float64(initInterval) * math.Pow(coefficient, float64(numAttempts))
	// Disallow retries if initialInterval is negative or nextInterval overflows
	if nextInterval <= 0 {
		return done
	}
	if maxInterval != 0 {
		nextInterval = math.Min(nextInterval, float64(maxInterval))
	}

	if p.TotalTimeout != 0 {
		remainingTime := float64(math.Max(0, float64(p.TotalTimeout-elapsedTime)))
		nextInterval = math.Min(remainingTime, nextInterval)
	}

	// Bail out if the next interval is smaller than initial retry interval
	nextDuration := time.Duration(nextInterval)
	if nextDuration < initInterval {
		return done
	}

	// add jitter to avoid global synchronization
	jitterPortion := int(0.2 * nextInterval)
	// Prevent overflow
	if jitterPortion < 1 {
		jitterPortion = 1
	}
	nextInterval = nextInterval*0.8 + float64(rand.Intn(jitterPortion))

	return time.Duration(nextInterval)
}

// retrier manages the state of a retry sequence: it tracks elapsed time and
// the current attempt count so that each call to nextBackOff returns the
// correct delay according to the policy.
type retrier struct {
	policy         *RetryPolicy
	clock          clock.TimeSource
	currentAttempt int
	startTime      time.Time
}

func newRetrier(policy *RetryPolicy, clk clock.TimeSource) *retrier {
	if policy == nil {
		panic("backoff: retry policy cannot be nil")
	}
	if clk == nil {
		panic("backoff: clock cannot be nil")
	}
	return &retrier{
		policy:    policy,
		clock:     clk,
		startTime: clk.Now(),
	}
}

func (r *retrier) reset() {
	r.startTime = r.clock.Now()
	r.currentAttempt = 0
}

// nextBackOff returns the next delay interval, or done (-1) when retries
// should stop.
func (r *retrier) nextBackOff() time.Duration {
	nextInterval := r.policy.computeNextDelay(r.getElapsedTime(), r.currentAttempt)
	r.currentAttempt++
	return nextInterval
}

func (r *retrier) getElapsedTime() time.Duration {
	return r.clock.Now().Sub(r.startTime)
}

// OwnershipStoreRetryPolicy returns the standard RetryPolicy for shard
// lease + tasklist range_id Claim* / Renew* operations (including the
// first attempt).
func OwnershipStoreRetryPolicy(maxAttempts int) *RetryPolicy {
	return &RetryPolicy{
		InitialInterval:    50 * time.Millisecond,
		MaximumInterval:    200 * time.Millisecond,
		BackoffCoefficient: 2,
		MaximumAttempts:    maxAttempts,
	}
}
