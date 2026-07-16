// Copyright (c) 2023-2026 Super Durable, Inc.
//
// This file is part of Dex
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program. If not, see <https://www.gnu.org/licenses/>.

package engine

import (
	"time"

	"github.com/xcherryio/xcherry/server/common/clock"
	"github.com/xcherryio/xcherry/server/common/log"
)

type (
	// TimerGate interface
	TimerGate interface {
		// FireChan return the signals channel of firing timers
		// after receiving an empty signal, caller should call Update to set up next one
		FireChan() <-chan struct{}
		// FireAfter checks whether the current timer will fire after the provided time
		FireAfter(checkTime time.Time) bool
		// InactiveOrFireAfter checks whether the current timer is inactive, or will fire after the provided time
		InactiveOrFireAfter(checkTime time.Time) bool
		// Update updates the TimerGate, return true if update is successful
		// success means TimerGate is idle, or TimerGate is set with a sooner time to fire the timer
		Update(nextTime time.Time) bool
		// Stop stops the timer if it's active
		Stop()
		// IsActive returns whether the timer is active(not fired yet)
		IsActive() bool
		// Close shutdown the TimerGate
		Close()
	}

	// LocalTimerGateImpl is a local timer implementation,
	// which basically is a wrapper of golang's timer
	LocalTimerGateImpl struct {
		// the channel which will be used to proxy the fired timer
		fireChan  chan struct{}
		closeChan chan struct{}

		timeSource clock.TimeSource

		// whether the timer is active(not fired yet)
		isActive bool
		// the actual timer which will fire
		timer *time.Timer
		// variable indicating when the above timer will fire
		nextWakeupTime time.Time
		logger         log.Logger
	}
)

// NewLocalTimerGate create a new timer gate instance
func NewLocalTimerGate(logger log.Logger) TimerGate {
	timer := &LocalTimerGateImpl{
		timer:          time.NewTimer(0),
		nextWakeupTime: time.Time{},
		fireChan:       make(chan struct{}, 1),
		closeChan:      make(chan struct{}),
		timeSource:     clock.NewRealTimeSource(),
		logger:         logger,
		isActive:       false,
	}

	if !timer.timer.Stop() {
		// the timer should be stopped when initialized
		// but drain it just in case it's not
		<-timer.timer.C
	}

	go func() {
		defer close(timer.fireChan)
		defer timer.timer.Stop()
	loop:
		for {
			select {
			case <-timer.timer.C:
				timer.isActive = false
				select {
				// when timer fires, send a signal to channel
				case timer.fireChan <- struct{}{}:
				default:
					// ignore if caller is not able to consume the previous signal
				}

			case <-timer.closeChan:
				// closed; cleanup and quit
				break loop
			}
		}
	}()

	return timer
}

func (tg *LocalTimerGateImpl) Stop() {
	tg.isActive = false
	tg.timer.Stop()
}

func (tg *LocalTimerGateImpl) IsActive() bool {
	return tg.isActive
}

func (tg *LocalTimerGateImpl) InactiveOrFireAfter(checkTime time.Time) bool {
	return !tg.isActive || tg.FireAfter(checkTime)
}

func (tg *LocalTimerGateImpl) FireChan() <-chan struct{} {
	return tg.fireChan
}

func (tg *LocalTimerGateImpl) FireAfter(checkTime time.Time) bool {
	return tg.nextWakeupTime.After(checkTime)
}

func (tg *LocalTimerGateImpl) Update(nextTime time.Time) bool {
	tg.isActive = true
	// NOTE: negative duration will make the timer fire immediately
	now := tg.timeSource.Now()

	if tg.timer.Stop() && tg.nextWakeupTime.Before(nextTime) {
		// Here stops the timer first then checking the nextWakeupTime. So that the timer will not be fired
		// when checking. This is useful when there are multiple updates happen concurrently, but we only
		// want to fire the timer once.

		// The nextWakeupTime being earlier than next time means that,
		// the old timer, before stopped, is active && next nextWakeupTime do not need to be updated
		// So reset it back to the previous wakeup time
		tg.timer.Reset(tg.nextWakeupTime.Sub(now))
		return false
	}

	// this means the timer, before stopped, is active && nextWakeupTime needs to be updated
	// or this means the timer, before stopped, is already fired / never active (when the tg.timer.Stop() returns false)
	tg.nextWakeupTime = nextTime
	tg.timer.Reset(nextTime.Sub(now))
	// Notifies caller that next notification is reset to fire at passed in 'next' visibility time
	return true
}

// Close shutdown the timer
func (tg *LocalTimerGateImpl) Close() {
	close(tg.closeChan)
}
