package config

import (
	"fmt"
	"time"

	"github.com/superdurable/dex/server/common/utils/backoff"
)

type TaskProcessorConfig struct {
	// Instance-level shared worker pool size. All shards share this pool.
	// Higher = more parallelism but more resource usage.
	NumWorkers int `yaml:"numWorkers"`

	// --- Immediate task reader ---

	// Max tasks to read per DB poll.
	ImmediateBatchReadLimit int `yaml:"immediateBatchReadLimit"`
	// How long to sleep when the task queue is empty before polling again.
	// It matters as a safety net for the case where UpdateRunWithNewTasks commits
	// the row but the client times out and signalTasks is never called
	ImmediatePollInterval time.Duration `yaml:"immediatePollInterval"`

	// --- Immediate task deleter ---

	// How often to range-delete processed tasks from DB (with jitter).
	ImmediateDeleteInterval       time.Duration `yaml:"immediateDeleteInterval"`
	ImmediateDeleteIntervalJitter time.Duration `yaml:"immediateDeleteIntervalJitter"`

	// --- Timer task reader ---

	// Max tasks to read per DB poll.
	TimerBatchReadLimit int `yaml:"timerBatchReadLimit"`
	// TimerMinLookAheadDuration controls how far ahead the timer reader polls:
	// it reads tasks with fire_at <= now + MinLookAhead. Larger values mean
	// fewer polls but more tasks buffered in memory.
	TimerMinLookAheadDuration time.Duration `yaml:"timerMinLookAheadDuration"`
	// TimerMaxLookAheadDuration is the extended look-ahead used when the queue
	// is empty (TimerGate pattern). The reader peeks one timer up to
	// now + MaxLookAhead to determine the next wakeup time, avoiding
	// unnecessary polls during idle periods. Should not be too large because
	// taskNotify is best-effort — if a notification is lost, the reader won't
	// wake up until this duration elapses.
	TimerMaxLookAheadDuration time.Duration `yaml:"timerMaxLookAheadDuration"`

	// --- Timer task deleter ---

	// How often to range-delete processed timer tasks from DB (with jitter).
	TimerDeleteInterval       time.Duration `yaml:"timerDeleteInterval"`
	TimerDeleteIntervalJitter time.Duration `yaml:"timerDeleteIntervalJitter"`

	// AttemptTimeout caps the context deadline for each individual task
	// processing attempt. Prevents a single slow handler (e.g., hanging gRPC
	// stream) from blocking a worker for the entire retry duration.
	// Should be <= ShardConfig.LeaseExpiryBuffer so that a single attempt
	// cannot outlive the lease safety window. Default: 4s.
	AttemptTimeout time.Duration `yaml:"attemptTimeout"`

	// ShutdownDeleteBatchSize is the page size for the shutdown-path
	// DeleteByIDBatch calls. Tasks completed above the watermark are deleted
	// in pages of this size to avoid overloading MongoDB.
	ShutdownDeleteBatchSize int `yaml:"shutdownDeleteBatchSize"`

	// ImmediateTaskRetryPolicy controls retry behavior for immediate tasks
	// (dispatch, channel message). Uses a short bounded retry because these
	// tasks are re-discoverable from the DB by the batch reader.
	ImmediateTaskRetryPolicy backoff.RetryPolicy `yaml:"immediateTaskRetryPolicy"`

	// TimerTaskRetryPolicy controls retry behavior for timer tasks (heartbeat,
	// durable timer). Uses a longer retry since timer tasks have unique fire
	// times and cannot be easily re-discovered.
	TimerTaskRetryPolicy backoff.RetryPolicy `yaml:"timerTaskRetryPolicy"`

	// --- OpsFIFO task reader / deleter ---
	//
	// The OpsFIFO outbox writes visibility + history rows that lag the run
	// state in the runs collection. The reader is INLINE (no worker pool):
	// every batch is read, executed, and committed by the same goroutine, so
	// FIFO is preserved per shard (and therefore per run, since a run is
	// always on a single shard).

	// OpsBatchReadLimit caps the number of OpsFIFOTaskRow rows pulled in a
	// single RangeReadOpsFIFOTasks call.
	OpsBatchReadLimit int `yaml:"opsBatchReadLimit"`

	// OpsBatchReadDelay is the deliberate debounce after a NotifyNewOpsFIFOTask
	// signal (or a non-empty previous batch) before issuing the next read.
	// Lets more rows accumulate so the reader can group them into a single
	// BatchInsertHistory / BatchUpsertVisibility call. Default 100ms.
	OpsBatchReadDelay time.Duration `yaml:"opsBatchReadDelay"`

	// OpsPollInterval is the fallback poll cadence when the reader is idle
	// (newOpsFIFOCh unset, last read empty). Only matters as a safety net for
	// the case where UpdateRunWithNewTasks commits the row but the client
	// times out and signalTasks is never called — see ops_batch_reader.go.
	// Observability writes can tolerate seconds of latency in that edge
	// case, so a much longer interval than the immediate-task path (5ms) is
	// fine. Default 30s.
	OpsPollInterval time.Duration `yaml:"opsPollInterval"`

	// OpsDeleteInterval is how often the OpsFIFO deleter runs
	// RangeDeleteOpsFIFOTasks up to the current committed offset (with jitter).
	OpsDeleteInterval       time.Duration `yaml:"opsDeleteInterval"`
	OpsDeleteIntervalJitter time.Duration `yaml:"opsDeleteIntervalJitter"`

	// OpsTaskRetryPolicy governs the indefinite-retry loop the OpsBatchReader
	// runs over a failing batch. TotalTimeout MUST be 0 (infinite) — see
	// ops_batch_reader.go for why FIFO can't DLQ-and-skip. The retry loop
	// is bounded only by shard-lease cancellation (the next owner resumes
	// from OpsFIFOTaskCommittedSeq).
	OpsTaskRetryPolicy backoff.RetryPolicy `yaml:"opsTaskRetryPolicy"`

	// OpsBatchStuckWarnEvery emits a warn-level log every N consecutive
	// retries on the same batch (so operators see the issue before lease
	// expiry). The `ops_fifo_task_batch_stuck` counter is bumped on EVERY failed
	// attempt independently — alert directly on the metric rate, the log
	// is just a human-readable signal that throttles itself.
	OpsBatchStuckWarnEvery int `yaml:"opsBatchStuckWarnEvery"`
}

func DefaultTaskProcessorConfig() TaskProcessorConfig {
	return TaskProcessorConfig{
		NumWorkers:                    1000,
		ImmediateBatchReadLimit:       1000,
		ImmediatePollInterval:         30 * time.Second,
		ImmediateDeleteInterval:       30 * time.Second,
		ImmediateDeleteIntervalJitter: 3 * time.Second,
		TimerBatchReadLimit:           1000,
		TimerMinLookAheadDuration:     1 * time.Second,
		TimerMaxLookAheadDuration:     60 * time.Second,
		TimerDeleteInterval:           30 * time.Second,
		TimerDeleteIntervalJitter:     3 * time.Second,
		AttemptTimeout:                4 * time.Second,
		ShutdownDeleteBatchSize:       1000,
		ImmediateTaskRetryPolicy: backoff.RetryPolicy{
			InitialInterval: 200 * time.Millisecond,
			MaximumInterval: 5 * time.Second,
			TotalTimeout:    30 * time.Minute,
		},
		TimerTaskRetryPolicy: backoff.RetryPolicy{
			InitialInterval: 100 * time.Millisecond,
			MaximumInterval: 5 * time.Second,
			TotalTimeout:    30 * time.Minute,
		},
		OpsBatchReadLimit:       1000,
		OpsBatchReadDelay:       100 * time.Millisecond,
		OpsPollInterval:         30 * time.Second,
		OpsDeleteInterval:       30 * time.Second,
		OpsDeleteIntervalJitter: 3 * time.Second,
		OpsTaskRetryPolicy: backoff.RetryPolicy{
			InitialInterval: 200 * time.Millisecond,
			MaximumInterval: 10 * time.Minute,
			TotalTimeout:    0, // infinite — FIFO can't DLQ-and-skip
		},
		OpsBatchStuckWarnEvery: 5,
	}
}

// Validate checks configuration constraints. leaseExpiryBuffer is from
// ShardConfig and used to validate AttemptTimeout.
func (c TaskProcessorConfig) Validate(leaseExpiryBuffer time.Duration) error {
	if c.AttemptTimeout > leaseExpiryBuffer {
		return fmt.Errorf("taskprocessor: AttemptTimeout (%v) must be <= ShardConfig.LeaseExpiryBuffer (%v)",
			c.AttemptTimeout, leaseExpiryBuffer)
	}
	return nil
}
