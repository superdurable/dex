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
	"github.com/xcherryio/apis/goapi/xcapi"
	"github.com/xcherryio/xcherry/server/common/ptr"
)

// Default: infinite retry with 1 second initial interval, 120 seconds max interval, and 2 backoff factor,
var defaultWorkerTaskBackoffRetryPolicy = xcapi.RetryPolicy{
	InitialIntervalSeconds:         ptr.Any(int32(1)),
	BackoffCoefficient:             ptr.Any(float32(2)),
	MaximumIntervalSeconds:         ptr.Any(int32(120)),
	MaximumAttempts:                ptr.Any(int32(0)),
	MaximumAttemptsDurationSeconds: ptr.Any(int32(0)),
}

const DEFAULT_WAIT_FOR_TIMEOUT_MAX int32 = 30

const WaitForProcessCompletionResultStop string = "STOP"
