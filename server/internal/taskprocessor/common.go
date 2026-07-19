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

package taskprocessor

import (
	"common-go/ids"
	"math/rand/v2"
	"time"
)

// TaskCompletion signals a task finished execution (immediate or timer).
type TaskCompletion struct {
	SortKey int64
	ID      ids.UID
}

// withJitter returns base plus a random amount in [0, jitter].
func withJitter(base, jitter time.Duration) time.Duration {
	if base <= 0 {
		return 0
	}
	if jitter <= 0 {
		return base
	}
	return base + time.Duration(rand.Int64N(int64(jitter)+1))
}
