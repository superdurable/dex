package dex

import (
	"os"
	"time"
)

// WorkerOptions configures Worker behavior.
type WorkerOptions struct {
	// TaskListName names the tasklist this worker polls. Runs started with
	// RunOptions.TaskListName matching this value are dispatched to this
	// worker via MatchingService.PollForRun. Empty defaults to
	// DefaultTaskListName — the same default applied by StartRun when
	// RunOptions.TaskListName is empty.
	TaskListName string

	// RunConcurrency caps how many runs this worker may execute in
	// parallel. Once at capacity, the PollForRun goroutines block
	// before issuing the next long-poll, applying back-pressure
	// upstream — the server sees no available worker for this tasklist
	// and queues the run until a slot frees. A run "occupies a slot"
	// from the moment its PollForRun returns until the runMain
	// goroutine exits (run completes, fails, is released, or worker
	// shuts down). Default: 100.
	RunConcurrency int

	// ConcurrentRunPollers is the number of goroutines long-polling
	// MatchingService.PollForRun concurrently. Each poller acquires a
	// RunConcurrency slot before issuing its long-poll, hands the
	// slot to the spawned runMain on receipt, and immediately loops
	// to acquire the next slot. Setting this above RunConcurrency has
	// no effect (the extra pollers stay parked on the slot semaphore);
	// setting it well below RunConcurrency limits how many fresh
	// dispatches can be picked up in parallel after a burst of run
	// completions. Default: 10.
	ConcurrentRunPollers int

	// ConcurrentExternalEventPollers is the number of goroutines
	// long-polling MatchingService.PollForExternalEvents on the
	// sticky tasklist (keyed by WorkerID). The server's sticky
	// registry routes each pushed event to one outstanding poll;
	// running >1 poller smooths over the reconnect gap when a poll
	// returns and the next has not yet been issued, so an external
	// channel publish or stop request lands within tens of
	// milliseconds even mid-rotation. Default: 2.
	ConcurrentExternalEventPollers int

	// HostID identifies the host/pod this worker runs on. Embedded in the
	// generated WorkerID for debuggability. Default: os.Getenv("HOSTNAME").
	HostID string

	// HeartbeatInterval is how often runMain calls
	// RunsService.ProcessRecordHeartbeat. The server's heartbeat timer
	// duration MUST be > 2x this interval so a single missed heartbeat
	// does not trigger transition to WaitingForWorker. Default: 8s
	// (server default heartbeat timeout is 24s).
	HeartbeatInterval time.Duration

	// Logger for worker events. Default: slog-backed logger.
	Logger Logger

	// PollErrorMaxBackoff caps exponential backoff after repeated poll
	// errors (starts at 100ms, doubles each failure). Default: 2m.
	PollErrorMaxBackoff time.Duration

	// RunInboxBufferSize bounds the per-run extChMsgInbox channel between the
	// sticky external-events router (PollForExternalEvents push +
	// WorkerCallResponse catch-up) and runMain. Each entry is a small
	// proto. On overflow, the producer drops the event and logs an
	// Error (the next WorkerCallResponse catch-up reconciles, but
	// drops indicate the extChMsgInbox is undersized for this run's external-
	// event volume). Default: 500.
	RunInboxBufferSize int

	// StepRetryLastErrorMaxBytes caps UTF-8 byte length per retry field
	// before reporting to the server. Default: 2048.
	StepRetryLastErrorMaxBytes int

	// default to 60s
	LongPollRPCTimeout time.Duration
	// default to 10s
	RegularRPCTimeout time.Duration
}

var defaultStepRetryLastErrorMaxBytes = 2048

func (o WorkerOptions) taskListName() string {
	if o.TaskListName != "" {
		return o.TaskListName
	}
	return DefaultTaskListName
}

func (o WorkerOptions) runConcurrency() int {
	if o.RunConcurrency > 0 {
		return o.RunConcurrency
	}
	return 100
}

func (o WorkerOptions) concurrentRunPollers() int {
	if o.ConcurrentRunPollers > 0 {
		return o.ConcurrentRunPollers
	}
	return 10
}

func (o WorkerOptions) concurrentExternalEventPollers() int {
	if o.ConcurrentExternalEventPollers > 0 {
		return o.ConcurrentExternalEventPollers
	}
	return 2
}

func (o WorkerOptions) hostID() string {
	if o.HostID != "" {
		return o.HostID
	}
	return os.Getenv("HOSTNAME")
}

func (o WorkerOptions) logger() Logger {
	if o.Logger != nil {
		return o.Logger
	}
	return NewDefaultLogger()
}

func (o WorkerOptions) heartbeatInterval() time.Duration {
	if o.HeartbeatInterval > 0 {
		return o.HeartbeatInterval
	}
	return 8 * time.Second
}

func (o WorkerOptions) pollErrorMaxBackoff() time.Duration {
	if o.PollErrorMaxBackoff > 0 {
		return o.PollErrorMaxBackoff
	}
	return 2 * time.Minute
}

func (o WorkerOptions) runInboxBufferSize() int {
	if o.RunInboxBufferSize > 0 {
		return o.RunInboxBufferSize
	}
	return 500
}

func (o WorkerOptions) stepRetryLastErrorMaxBytes() int {
	if o.StepRetryLastErrorMaxBytes > 0 {
		return o.StepRetryLastErrorMaxBytes
	}
	return defaultStepRetryLastErrorMaxBytes
}

func (o WorkerOptions) longPollRPCTimeout() time.Duration {
	if o.LongPollRPCTimeout > 0 {
		return o.LongPollRPCTimeout
	}
	return 60 * time.Second
}

// TODO apply to Client
func (o WorkerOptions) regularRPCTimeout() time.Duration {
	if o.RegularRPCTimeout > 0 {
		return o.RegularRPCTimeout
	}
	return 10 * time.Second
}
