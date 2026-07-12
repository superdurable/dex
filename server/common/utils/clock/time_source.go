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

package clock

import (
	"context"
	"time"

	"github.com/jonboulle/clockwork"
)

type (
	// TimeSource provides an interface that packages can use instead of directly using
	// the [time] module, so that chronology-related behavior can be tested.
	TimeSource interface {
		After(d time.Duration) <-chan time.Time
		Sleep(d time.Duration)
		SleepWithContext(ctx context.Context, d time.Duration) error
		Now() time.Time
		Since(t time.Time) time.Duration
		NewTicker(d time.Duration) Ticker
		NewTimer(d time.Duration) Timer
		AfterFunc(d time.Duration, f func()) Timer
		ContextWithTimeout(context.Context, time.Duration) (context.Context, context.CancelFunc)
		ContextWithDeadline(context.Context, time.Time) (context.Context, context.CancelFunc)
	}

	// Ticker provides an interface which can be used instead of directly using
	// [time.Ticker]. The real-time ticker t provides ticks through t.C which
	// becomes t.Chan() to make this channel requirement definable in this
	// interface.
	Ticker interface {
		Chan() <-chan time.Time
		Reset(d time.Duration)
		Stop()
	}

	// Timer provides an interface which can be used instead of directly using
	// [time.Timer]. The real-time timer t provides events through t.C which becomes
	// t.Chan() to make this channel requirement definable in this interface.
	Timer interface {
		Chan() <-chan time.Time
		Reset(d time.Duration) bool
		Stop() bool
	}

	// clock serves real wall-clock time
	clock struct {
		clockwork.Clock
	}
)

// NewRealTimeSource returns a time source that servers
// real wall clock time
func NewRealTimeSource() TimeSource {
	return &clock{
		Clock: clockwork.NewRealClock(),
	}
}

func (r *clock) ContextWithTimeout(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, d)
}

func (r *clock) ContextWithDeadline(ctx context.Context, t time.Time) (context.Context, context.CancelFunc) {
	return context.WithDeadline(ctx, t)
}

func (r *clock) NewTicker(d time.Duration) Ticker {
	return r.Clock.NewTicker(d)
}

func (r *clock) NewTimer(d time.Duration) Timer {
	return r.Clock.NewTimer(d)
}

func (r *clock) AfterFunc(d time.Duration, f func()) Timer {
	return r.Clock.AfterFunc(d, f)
}

func (r *clock) SleepWithContext(ctx context.Context, duration time.Duration) error {
	select {
	case <-r.After(duration):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
