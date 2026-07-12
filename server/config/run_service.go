package config

import "time"

// RunServiceConfig controls the behavior of the RunsService
type RunServiceConfig struct {
	// MaxTransientErrorRetries is the maximum number of retries on transient
	// errors (CAS version mismatch, store timeouts, etc.) when updating a run.
	// These errors happen when concurrent operations modify the same run between
	// GetRun and UpdateRunWithNewTasks, or when the store has transient failures.
	// Default: 3 (up to 4 total attempts: 1 initial + 3 retries).
	MaxTransientErrorRetries int `yaml:"maxTransientErrorRetries"`

	// MatchingServiceAPITimeout is the per-call timeout for RPCs from
	// run engine to matching service (DeliverExternalChannelMessage).
	// Default: 10s
	MatchingServiceAPITimeout time.Duration `yaml:"matchingServiceAPITimeout"`

	// HeartbeatTimerDuration is the fire delay for the heartbeat timeout
	// timer created when a run transitions to Running. If the worker does
	// not call ProcessRecordHeartbeat before this timer fires, the run is
	// transitioned to WaitingForWorker.
	// Default: 30s
	HeartbeatTimerDuration time.Duration `yaml:"heartbeatTimerDuration"`

	// ExtEventDeliveryBufferSize is the per-run buffered channel size for the
	// best-effort external event delivery goroutine (extEventDispatcher).
	// When the channel is full, events are silently dropped — channel
	// re-delivered via WorkerCallResponse.stop_requested when the run is terminal.
	// Default: 256
	ExtEventDeliveryBufferSize int `yaml:"extEventDeliveryBufferSize"`

	// StepRetryLastErrorMaxBytes caps UTF-8 byte length per StepRetryState
	// last_error / last_error_stack_trace; oversize RPCs reject.
	StepRetryLastErrorMaxBytes int `yaml:"stepRetryLastErrorMaxBytes"`

	// WaitForHistoryMaxTimeout caps how long a single WaitForHistoryEvent RPC
	// blocks on the server
	// Default: 60s
	WaitForHistoryMaxTimeout time.Duration `yaml:"waitForHistoryMaxTimeout"`
}

func DefaultRunServiceConfig() RunServiceConfig {
	return RunServiceConfig{
		MaxTransientErrorRetries:   3,
		MatchingServiceAPITimeout:  10 * time.Second,
		HeartbeatTimerDuration:     30 * time.Second,
		ExtEventDeliveryBufferSize: 256,
		StepRetryLastErrorMaxBytes: 2048,
		WaitForHistoryMaxTimeout:   60 * time.Second,
	}
}
