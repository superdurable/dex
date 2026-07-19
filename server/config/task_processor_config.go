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

	// ImmediateBatchReadLimit is max immediate tasks per RangeRead. Default: 1000.
	ImmediateBatchReadLimit int `yaml:"immediateBatchReadLimit"`
	// ImmediateMaxPollInterval is the idle poll fallback if notify is lost. Default: 1m.
	ImmediateMaxPollInterval time.Duration `yaml:"immediateMaxPollInterval"`
	// ImmediateDeleteInterval is how often to RangeDelete processed tasks. Default: 5s.
	ImmediateDeleteInterval time.Duration `yaml:"immediateDeleteInterval"`
	// ImmediateDeleteIntervalJitter spreads deletes across shards. Default: 2s.
	ImmediateDeleteIntervalJitter time.Duration `yaml:"immediateDeleteIntervalJitter"`

	// TimerBatchReadLimit is max timer tasks per RangeRead. Default: 1000.
	TimerBatchReadLimit int `yaml:"timerBatchReadLimit"`
	// TimerMinLookAheadDuration is how far ahead reads go (now + MinLookAhead).
	// Larger means fewer polls but more buffered tasks. Default: 1s.
	TimerMinLookAheadDuration time.Duration `yaml:"timerMinLookAheadDuration"`
	// TimerMaxLookAheadDuration is idle TimerGate sleep when the queue is empty.
	// Caps delay if a notify is lost. Default: 1m.
	TimerMaxLookAheadDuration time.Duration `yaml:"timerMaxLookAheadDuration"`
	// TimerDeleteInterval is how often to RangeDelete processed tasks. Default: 5s.
	TimerDeleteInterval time.Duration `yaml:"timerDeleteInterval"`
	// TimerDeleteIntervalJitter spreads deletes across shards. Default: 2s.
	TimerDeleteIntervalJitter time.Duration `yaml:"timerDeleteIntervalJitter"`

	// ShutdownGracePeriod control how long to wait for processors to
	// 1. commit the watermarks
	// 2. delete tasks above the watermark
	// Default: 10s
	ShutdownGracePeriod time.Duration `yaml:"shutdownGracePeriod"`
	// ShutdownDeleteBatchSize is the page size for the shutdown-path
	// DeleteByIDBatch calls. Tasks completed above the watermark are deleted
	// in pages of this size to avoid overloading MongoDB.
	ShutdownDeleteBatchSize int `yaml:"shutdownDeleteBatchSize"`
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

		ImmediateBatchReadLimit:       1000,
		ImmediateMaxPollInterval:      time.Minute,
		ImmediateDeleteInterval:       5 * time.Second,
		ImmediateDeleteIntervalJitter: 2 * time.Second,

		TimerBatchReadLimit:       1000,
		TimerMinLookAheadDuration: time.Second,
		TimerMaxLookAheadDuration: time.Minute,
		TimerDeleteInterval:       5 * time.Second,
		TimerDeleteIntervalJitter: 2 * time.Second,

		ShutdownGracePeriod:     10 * time.Second,
		ShutdownDeleteBatchSize: 1000,
	}
}
