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

package config

import (
	"time"

	"github.com/superdurable/dex/server/internal/backoff"
)

type TaskProcessorConfig struct {
	// NumWorkers is the fixed worker-pool size for task execution. Default: 1000.
	NumWorkers int `yaml:"numWorkers"`
	// HandleAttemptTimeout is the per-attempt handler deadline. Default: 4s.
	HandleAttemptTimeout time.Duration       `yaml:"handleAttemptTimeout"`
	HandleRetryPolicy    backoff.RetryPolicy `yaml:"handleRetryPolicy"`
}

func DefaultTaskProcessorConfig() TaskProcessorConfig {
	return TaskProcessorConfig{
		NumWorkers:           1000,
		HandleAttemptTimeout: 4 * time.Second,
		HandleRetryPolicy: backoff.RetryPolicy{
			InitialInterval:    1 * time.Second,
			MaximumInterval:    30 * time.Second,
			BackoffCoefficient: 2.0,
			TotalTimeout:       30 * time.Minute,
		},
	}
}
