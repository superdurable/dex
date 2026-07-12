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
	"sync"
	"time"

	"github.com/superdurable/dex/server/common/utils/clock"
)

// concurrentRetrier provides client-side throttling for outgoing requests.
// It tracks consecutive failures and sleeps according to a backoff policy
// before allowing the next attempt, preventing a burst of retries from
// overwhelming a recovering downstream service.
//
// Usage:
//
//	cr := backoff.NewConcurrentRetrier(&backoff.RetryPolicy{
//	    InitialInterval: 500 * time.Millisecond,
//	    MaximumInterval: 30 * time.Second,
//	})
//	cr.Throttle()  // sleeps if there have been recent failures
//	err := callDownstream()
//	if err != nil {
//	    cr.Failed()
//	} else {
//	    cr.Succeeded()
//	}
type concurrentRetrier struct {
	sync.Mutex
	ret          *retrier
	failureCount int64
}

// NewConcurrentRetrier creates a concurrentRetrier backed by the given policy.
func NewConcurrentRetrier(retryPolicy *RetryPolicy) *concurrentRetrier {
	return &concurrentRetrier{
		ret: newRetrier(retryPolicy, clock.NewRealTimeSource()),
	}
}

// Throttle sleeps if there have been failures since the last Succeeded call.
// The sleep duration follows the configured backoff policy.
func (c *concurrentRetrier) Throttle() {
	c.throttleInternal()
}

func (c *concurrentRetrier) throttleInternal() time.Duration {
	next := done

	// Check if we have failure count.
	failureCount := c.failureCount
	if failureCount > 0 {
		defer c.Unlock()
		c.Lock()
		if c.failureCount > 0 {
			next = c.ret.nextBackOff()
		}
	}

	if next != done {
		time.Sleep(next)
	}

	return next
}

// Succeeded resets the failure counter and backoff state.
func (c *concurrentRetrier) Succeeded() {
	defer c.Unlock()
	c.Lock()
	c.failureCount = 0
	c.ret.reset()
}

// Failed increments the consecutive failure counter.
func (c *concurrentRetrier) Failed() {
	defer c.Unlock()
	c.Lock()
	c.failureCount++
}
