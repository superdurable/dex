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

package clock

import (
	"time"

	// clockwork is not currently used but it is useful to have the option to use this in testing code
	// this comment is needed to stop lint from complaining about this _ import
	_ "github.com/jonboulle/clockwork"
)

type (
	// TimeSource is an interface for any
	// entity that provides the current
	// time. Its primarily used to mock
	// out timesources in unit test
	TimeSource interface {
		Now() time.Time
	}
	// RealTimeSource serves real wall-clock time
	RealTimeSource struct{}

	// EventTimeSource serves fake controlled time
	EventTimeSource struct {
		now time.Time
	}
)

// NewRealTimeSource returns a time source that servers
// real wall clock time
func NewRealTimeSource() *RealTimeSource {
	return &RealTimeSource{}
}

// Now return the real current time
func (ts *RealTimeSource) Now() time.Time {
	return time.Now()
}

// NewEventTimeSource returns a time source that servers
// fake controlled time
func NewEventTimeSource() *EventTimeSource {
	return &EventTimeSource{}
}

// Now return the fake current time
func (ts *EventTimeSource) Now() time.Time {
	return ts.now
}

// Update update the fake current time
func (ts *EventTimeSource) Update(now time.Time) *EventTimeSource {
	ts.now = now
	return ts
}
