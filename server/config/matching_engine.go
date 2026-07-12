package config

import "time"

// MatchingServiceConfig controls the behavior of the MatchingService's tasklist
// management, task dispatch, and worker interaction logic.
type MatchingServiceConfig struct {
	// MaxTaskWriteBatchSize is the maximum number of tasks the writer goroutine
	// will batch into a single CreateTasks DB call. The writer drains its
	// appendCh non-blocking up to this limit.
	// Default: 100
	MaxTaskWriteBatchSize int `yaml:"maxTaskWriteBatchSize"`

	// MaxTaskReadBatchSize is the maximum number of tasks the reader goroutine
	// will request in a single GetTasks DB call. Caps the per-call response size.
	// Default: 100
	MaxTaskReadBatchSize int `yaml:"maxTaskReadBatchSize"`

	// MaxTaskDeleteBatchSize is the maximum number of tasks per GC DELETE call.
	// Also serves as the size threshold: GC fires when backlog >= this value.
	// Default: 100
	MaxTaskDeleteBatchSize int `yaml:"maxTaskDeleteBatchSize"`

	// MaxTimeBetweenTaskDeletes is the time threshold for GC. If no GC has
	// run within this duration and there is any backlog, GC fires regardless
	// of size threshold.
	// Default: 1m
	MaxTimeBetweenTaskDeletes time.Duration `yaml:"maxTimeBetweenTaskDeletes"`

	// UpdateAckInterval is how often the reader persists ack_level to the
	// tasklist metadata row (fenced write). Also drives periodic DB scan
	// for tasks (safety net for missed signals).
	// Default: 10s
	UpdateAckInterval time.Duration `yaml:"updateAckInterval"`

	// LongPollDefaultTimeout is the default duration for PollForRun and
	// PollForExternalEvents long-poll calls. Workers may override via
	// request-level timeout, but this is the server-side cap.
	// Default: 30s
	LongPollDefaultTimeout time.Duration `yaml:"longPollDefaultTimeout"`

	// LongPollSafetyBuffer is the time before the long-poll deadline at which
	// the server returns an empty response. This prevents consuming a task
	// too late to deliver the response.
	// Default: 5s
	LongPollSafetyBuffer time.Duration `yaml:"longPollSafetyBuffer"`

	// ForwarderMaxOutstandingTasks limits how many ForwardTask RPCs a
	// non-root partition can have in-flight to its root partition at once.
	// ForwardTask is called when a DispatchRun arrives at a non-root
	// partition and there's no local poller for sync match — the partition
	// forwards the task to root to try root's pollers before falling back
	// to async DB write. Low default (1) prevents a burst of dispatches
	// from flooding the root with concurrent forwarded RPCs.
	// Default: 1
	ForwarderMaxOutstandingTasks int `yaml:"forwarderMaxOutstandingTasks"`

	// ForwarderMaxOutstandingPolls limits how many ForwardPoll RPCs a
	// non-root partition can have in-flight to its root at once.
	// ForwardPoll is called when a PollForRun arrives at a non-root
	// partition that has no buffered tasks — it registers the poller at
	// the root so the poller can pick up tasks arriving at root. Low
	// default (1) avoids stacking too many forwarded long-polls on root.
	// Default: 1
	ForwarderMaxOutstandingPolls int `yaml:"forwarderMaxOutstandingPolls"`

	// ForwarderMaxRatePerSecond is a per-partition rate limit on all
	// forwarding RPCs (both ForwardTask and ForwardPoll combined). If
	// exceeded, the forwarder returns ErrForwarderSlowDown and the caller
	// falls back to local handling (async DB write for tasks, empty
	// return for polls). Prevents a hot non-root partition from
	// overwhelming root with forwarded requests.
	// Default: 10
	ForwarderMaxRatePerSecond int `yaml:"forwarderMaxRatePerSecond"`

	// MembershipChangeBufferSize is the channel buffer size for membership
	// change events subscription. The membership change loop reads from this
	// channel and proactively shuts down non-owned tasklists.
	// Default: 1000
	MembershipChangeBufferSize int `yaml:"membershipChangeBufferSize"`

	// OperationTimeout is the per-call timeout for individual operations in
	// the matching service: gRPC calls to the run service, DB calls, etc.
	// Default: 10s
	OperationTimeout time.Duration `yaml:"operationTimeout"`

	// TaskBufferSize is the capacity of the taskReader's in-memory FIFO
	// buffer. Tasks read from DB are buffered here before being dispatched
	// to polling workers. Tasks that fail delivery (ProcessAsyncMatch
	// transient error or worker disconnect) are pushed back into this
	// same buffer (blocking send); pendingSet keeps them tracked so the
	// watermark won't advance past them until they're successfully delivered.
	// Default: 100
	TaskBufferSize int `yaml:"taskBufferSize"`

	// TasklistOwnershipScanInterval is how often the registry checks each
	// owned tasklist's hash-ring ownership against the current cluster
	// membership. If ownership has shifted to another node, the manager
	// is gracefully stopped and removed from the registry. The
	// fenced-write path is the primary correctness defense; this scan is
	// the secondary mechanism that proactively releases tasklists during
	// rebalances so the new owner doesn't wait for a fence-fail to learn
	// it can take over.
	// Default: 30s
	TasklistOwnershipScanInterval time.Duration `yaml:"tasklistOwnershipScanInterval"`

	// StickyCleanupInterval is how often the StickyRegistry sweeps idle
	// per-worker sticky entries. Sticky entries are tiny (a channel + an
	// activity timestamp), but unbounded creation (e.g. CI generating
	// fresh worker IDs constantly) could otherwise leak memory in a
	// long-running matching process. Idle == lastActiveAt older than
	// StickyIdleTimeout.
	// Default: 5m
	StickyCleanupInterval time.Duration `yaml:"stickyCleanupInterval"`

	// StickyIdleTimeout determines when a sticky entry is considered idle
	// and eligible for sweep. Any DeliverExternalEvents or
	// PollForExternalEvents call refreshes the entry's lastActiveAt.
	// Worker-side reconnects after a transient network blip will simply
	// recreate the entry on next poll, so this can be aggressive.
	// Default: 30m
	StickyIdleTimeout time.Duration `yaml:"stickyIdleTimeout"`
}

func DefaultMatchingEngineConfig() MatchingServiceConfig {
	return MatchingServiceConfig{
		MaxTaskWriteBatchSize:         100,
		MaxTaskReadBatchSize:          100,
		MaxTaskDeleteBatchSize:        100,
		MaxTimeBetweenTaskDeletes:     1 * time.Minute,
		UpdateAckInterval:             10 * time.Second,
		LongPollDefaultTimeout:        30 * time.Second,
		LongPollSafetyBuffer:          5 * time.Second,
		ForwarderMaxOutstandingTasks:  1,
		ForwarderMaxOutstandingPolls:  1,
		ForwarderMaxRatePerSecond:     10,
		MembershipChangeBufferSize:    1000,
		OperationTimeout:              10 * time.Second,
		TaskBufferSize:                100,
		TasklistOwnershipScanInterval: 30 * time.Second,
		StickyCleanupInterval:         5 * time.Minute,
		StickyIdleTimeout:             30 * time.Minute,
	}
}
