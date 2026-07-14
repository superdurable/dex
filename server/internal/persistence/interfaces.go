package persistence

import (
	"context"
	"fmt"
	"time"

	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
	"github.com/superdurable/dex/server/common/errors"
	"github.com/superdurable/dex/server/common/utils/ids"
	"github.com/superdurable/dex/server/common/utils/ptr"
	"go.mongodb.org/mongo-driver/bson"
	"google.golang.org/protobuf/proto"
)

// ============================================================================
// Enums -- all stored as int32 in MongoDB to minimize document/index size.
// Go code uses typed constants for readability.
// ============================================================================

type RunStatus int32

const (
	RunStatusInvalid                      RunStatus = -1 // sentinel: no valid status (0 is Pending)
	RunStatusPending                      RunStatus = 0  // just created, not yet dispatched
	RunStatusWaitingForWorker             RunStatus = 1  // dispatched but no worker yet
	RunStatusRunning                      RunStatus = 2  // worker is holding the run
	RunStatusAllStepsWaitingForConditions RunStatus = 3  // all steps are waiting for conditions
	RunStatusCompleted                    RunStatus = 4  // run completed successfully
	RunStatusFailed                       RunStatus = 5  // run failed
)

func (s RunStatus) IsTerminal() bool {
	return s == RunStatusCompleted || s == RunStatusFailed
}

func (s RunStatus) Name() string {
	switch s {
	case RunStatusInvalid:
		return "invalid"
	case RunStatusPending:
		return "pending"
	case RunStatusWaitingForWorker:
		return "waiting_for_worker"
	case RunStatusRunning:
		return "running"
	case RunStatusAllStepsWaitingForConditions:
		return "all_steps_waiting_for_conditions"
	case RunStatusCompleted:
		return "completed"
	case RunStatusFailed:
		return "failed"
	default:
		return fmt.Sprintf("unknown_%d", s)
	}
}

type StepExecutionStatus int32

const (
	StepExeStatusInvokingWaitFor     StepExecutionStatus = 0
	StepExeStatusWaitingForCondition StepExecutionStatus = 1
	StepExeStatusInvokingExecute     StepExecutionStatus = 2
)

type ImmediateTaskType int32

const (
	ImmediateTaskRunInitialDispatch ImmediateTaskType = 0
	ImmediateTaskRunResumeDispatch  ImmediateTaskType = 1
)

type TimerTaskType int32

const (
	TimerTaskRunHeartbeat     TimerTaskType = 0
	TimerTaskStepWaitForTimer TimerTaskType = 1
)

// OpsFIFOTaskType identifies which observability target a single OpsFIFO
// task writes to. The OpsFIFO batch executor splits a read batch by this
// enum and dispatches each half to its independent store.
type OpsFIFOTaskType int32

const (
	OpsFIFOTaskHistoryWrite    OpsFIFOTaskType = 0
	OpsFIFOTaskVisibilityWrite OpsFIFOTaskType = 1
)

type RowType int32

const (
	RowTypeRun           RowType = 1
	RowTypeImmediateTask RowType = 2
	RowTypeTimerTask     RowType = 3
	// RowTypeOpsFIFOTask is the per-shard FIFO observability outbox row that
	// the OpsFIFO batch reader drains in order. Lives in the runs collection
	// alongside the run + immediate + timer rows so the unified pk_idx
	// (shard_id, row_type, namespace, sort_key, id) covers it without a
	// new index. See docs/ops-fifo-queue-design.md.
	RowTypeOpsFIFOTask RowType = 4
)

// ============================================================================
// Value (stored in state_map, unconsumed_channel_messages, step inputs)
//
// Primitives are stored inline. EncodedObjects may be stored as blob refs
// internally (server decides based on size). The API proto uses Value
// everywhere; blob_ref is a server-internal concept.
// ============================================================================

type ValueType int32

const (
	ValueTypeInt     ValueType = 0
	ValueTypeDouble  ValueType = 1
	ValueTypeBool    ValueType = 2
	ValueTypeNull    ValueType = 3
	ValueTypeBlobRef ValueType = 4 // references a blob in BlobStore
)

// Value is the persistence representation. EncodedObjects are NEVER stored
// inline -- they are always written to BlobStore first and referenced here
// via blob_ref. The RunEngine handles the conversion before writing.
type Value struct {
	Type      ValueType  `bson:"type"`
	IntVal    *int64     `bson:"int_val,omitempty"`
	DoubleVal *float64   `bson:"double_val,omitempty"`
	BoolVal   *bool      `bson:"bool_val,omitempty"`
	BlobID    ids.BlobID `bson:"blob_id,omitempty"` // for ValueTypeBlobRef
}

// ============================================================================
// Wait Conditions
// ============================================================================

type WaitType int32

const (
	WaitTypeAnyOf WaitType = 0
	WaitTypeAllOf WaitType = 1
)

type TimerCondition struct {
	FireAtUnixMs int64 `bson:"fire_at_unix_ms"`
}

type ChannelCondition struct {
	ChannelName string `bson:"channel_name"`
	Min         int32  `bson:"min"`
	Max         int32  `bson:"max"`
}

type SingleCondition struct {
	Timer   *TimerCondition   `bson:"timer,omitempty"`
	Channel *ChannelCondition `bson:"channel,omitempty"`
}

type WaitForCondition struct {
	Type       WaitType          `bson:"type"`
	Conditions []SingleCondition `bson:"conditions"`
}

// ConditionResult captures the per-condition outcome of an evaluated
// WaitForCondition. Persisted on ActiveStepExecution when a step transitions
// from WAITING_FOR_CONDITION to INVOKING_EXECUTE so the worker (on resume
// after a possible re-dispatch) sees what fired without recomputing.
type ConditionResult struct {
	Timer   *TimerConditionResult   `bson:"timer,omitempty"`
	Channel *ChannelConditionResult `bson:"channel,omitempty"`
}

type TimerConditionResult struct {
	Fired        bool  `bson:"fired"`
	FireAtUnixMs int64 `bson:"fire_at_unix_ms"`
}

type ChannelConditionResult struct {
	ChannelName   string `bson:"channel_name"`
	Satisfied     bool   `bson:"satisfied"`
	ConsumedCount int32  `bson:"consumed_count"` // reserved count, not front-pop on promote
}

// Validate checks that the condition is well-formed before persistence.
// SDK must always wrap conditions in AnyOf or AllOf with at least one condition.
func (w *WaitForCondition) Validate() errors.CategorizedError {
	if w == nil || len(w.Conditions) == 0 {
		return errors.NewInvalidInputError(
			"invalid WaitForCondition: must have at least one condition wrapped in AnyOf or AllOf", nil)
	}
	if w.Type != WaitTypeAnyOf && w.Type != WaitTypeAllOf {
		return errors.NewInvalidInputError(
			fmt.Sprintf("invalid WaitForCondition: unknown WaitType %d, must be AnyOf or AllOf", w.Type), nil)
	}
	timerCount := 0
	for _, c := range w.Conditions {
		if c.Timer != nil {
			timerCount++
		}
	}
	if timerCount > 1 {
		return errors.NewInvalidInputError(
			fmt.Sprintf("invalid WaitForCondition: at most one TimerCondition allowed, got %d", timerCount), nil)
	}
	return nil
}

// ============================================================================
// Run Row
// ============================================================================

// RetryState captures the worker's retry progress for a step method.
// Persisted as part of ActiveStepExecution; never cleared individually — removed
// only when the entire ActiveStepExecution is deleted on step completion.
type RetryState struct {
	FirstAttemptTime    time.Time `bson:"first_attempt_time"`
	CurrentAttempts     int32     `bson:"current_attempts"`
	LastError           string    `bson:"last_error"`
	LastErrorStackTrace string    `bson:"last_error_stack_trace,omitempty"`
}

type ActiveStepExecution struct {
	Input            Value               `bson:"input"`
	Status           StepExecutionStatus `bson:"status"`
	WaitForCondition *WaitForCondition   `bson:"wait_for_condition,omitempty"`
	// ConditionResults is set when Status == INVOKING_EXECUTE AND the step
	// was previously WAITING_FOR_CONDITION (i.e. on resume). Mirrors the
	// proto's ActiveStepExecution.condition_results. Worker reads it to
	// know which conditions fired without re-evaluating locally.
	ConditionResults  []ConditionResult `bson:"condition_results,omitempty"`
	WaitForRetryState *RetryState       `bson:"wait_for_retry_state,omitempty"`
	ExecuteRetryState *RetryState       `bson:"execute_retry_state,omitempty"`
	// FromStepExeID is the parent step_exe_id that spawned this step via
	// NextSteps. Empty for starting steps (those created by StartRun).
	// Persisted so the StepExecuteCompleted history event written when
	// THIS step finishes carries the correct provenance for graph
	// rendering even if the worker reporting the completion was
	// re-dispatched and lost in-memory parent linkage.
	//
	// Engine MUST preserve this on every status transition (e.g. when
	// rebuilding ActiveStepExecutions to flip INVOKING_WAIT_FOR ->
	// WAITING_FOR_CONDITION or to apply a sibling unblock). Tests for
	// the parent-edge regression live in
	// server/internal/integration/sdke2e/sdk_e2e_dynamic_channel_test.go.
	FromStepExeID string `bson:"from_step_exe_id,omitempty"`
	// WaitForMethodExeID is allocated once when the step enters WaitFor phase.
	WaitForMethodExeID int64 `bson:"wait_for_method_exe_id,omitempty"`
	// ExecuteMethodExeID is allocated on WAITING -> INVOKING_EXECUTE promote.
	ExecuteMethodExeID int64 `bson:"execute_method_exe_id,omitempty"`
}

type RunRow struct {
	ShardID   int32   `bson:"shard_id"`
	RowType   RowType `bson:"row_type"`
	Namespace string  `bson:"namespace"`
	SortKey   int64   `bson:"sort_key"`
	ID        string  `bson:"id"`

	FlowType     string    `bson:"flow_type"`
	TaskListName string    `bson:"task_list_name"`
	Status       RunStatus `bson:"status"`
	Version      int64     `bson:"version"`
	WorkerID     string    `bson:"worker_id"`

	StateMap                  map[string]Value               `bson:"state_map"`
	UnconsumedChannelMessages map[string][]ChannelMessage    `bson:"unconsumed_channel_messages"`
	StepExeIDCounters         map[string]int32               `bson:"step_exe_id_counters"`
	ActiveStepExecutions      map[string]ActiveStepExecution `bson:"active_step_executions"`

	WorkerRequestCounter          int64 `bson:"worker_request_counter"`
	ExternalChannelMessageCounter int64 `bson:"external_channel_message_counter"`
	// StepMethodExeCounter is a run-global monotonic counter for waitForMethodExeID
	// and executeMethodExeID allocation.
	StepMethodExeCounter int64 `bson:"step_method_exe_counter"`

	LastHeartbeatTime time.Time  `bson:"last_heartbeat_time"`
	HeartbeatTimerID  ids.TaskID `bson:"heartbeat_timer_id"`

	ActiveDurableTimerID ids.TaskID `bson:"active_durable_timer_id"`
	DurableTimerFireAt   int64      `bson:"durable_timer_fire_at"`

	// LastHistoryEventID is the highest event_id allocated for this run by
	// the engine when it enqueues HistoryWrite OpsTasks. Bumped under the
	// same CAS that commits the run state change, so history events are
	// gap-free for any successfully committed run state. The OpsFIFO batch
	// writer uses the value stamped on each task as-is (no $inc round trip).
	LastHistoryEventID int64 `bson:"last_history_event_id"`

	CreatedAt time.Time `bson:"created_at"`
	UpdatedAt time.Time `bson:"updated_at"`
}

// RunRowUpdate is a partial update applied via UpdateRunWithNewTasks.
// nil/zero fields are not modified.
type RunRowUpdate struct {
	Status                        *RunStatus
	WorkerID                      *string
	StateMap                      map[string]Value                // fields to upsert (delta)
	ReplaceUnconsumedChannels     map[string][]ChannelMessage     // channels to replace entirely ($set)
	StepExeIDCounters             map[string]int32                // counters to set
	ActiveStepExecutions          map[string]*ActiveStepExecution // nil value = delete key
	WorkerRequestCounter          *int64
	ExternalChannelMessageCounter *int64
	StepMethodExeCounter          *int64
	LastHeartbeatTime             *time.Time
	HeartbeatTimerID              *ids.TaskID
	ActiveDurableTimerID          *ids.TaskID
	DurableTimerFireAt            *int64
	LastHistoryEventID            *int64
	// Full-map replace fields (used by ForkRun; nil = not modified).
	ReplaceStateMap              *map[string]Value
	ReplaceActiveStepExecutions  *map[string]ActiveStepExecution
	ReplaceStepExeIDCounters     *map[string]int32
	ReplaceAllUnconsumedChannels *map[string][]ChannelMessage
}

func (ru *RunRowUpdate) AllocateStepMethodExeCounter(fromRunRow int64) int64 {
	if ru.StepMethodExeCounter == nil {
		ru.StepMethodExeCounter = ptr.Any(fromRunRow)
	}
	ru.StepMethodExeCounter = ptr.Any(*ru.StepMethodExeCounter + 1)
	return *ru.StepMethodExeCounter
}
func (ru *RunRowUpdate) SetStepMethodCounterIfGreater(fromRunRow, input int64) bool {
	if ru.StepMethodExeCounter == nil {
		ru.StepMethodExeCounter = ptr.Any(fromRunRow)
	}
	if input > *ru.StepMethodExeCounter {
		ru.StepMethodExeCounter = ptr.Any(input)
		return true
	}
	return false
}

// ============================================================================
// Immediate Task Row
// ============================================================================

type ImmediateTaskInfo struct {
	RunID              string `bson:"run_id"`
	Namespace          string `bson:"namespace"`
	TaskListName       string `bson:"task_list_name"`
	DurableTimerFireAt int64  `bson:"durable_timer_fire_at,omitempty"`
}

type ImmediateTaskRow struct {
	ShardID   int32             `bson:"shard_id"`
	RowType   RowType           `bson:"row_type"`
	Namespace string            `bson:"namespace"`
	SortKey   int64             `bson:"sort_key"`
	ID        ids.TaskID        `bson:"id"`
	TaskType  ImmediateTaskType `bson:"task_type"`
	TaskInfo  ImmediateTaskInfo `bson:"task_info"`
	CreatedAt time.Time         `bson:"created_at"`
}

// ============================================================================
// Timer Task Row
// ============================================================================

type TimerTaskInfo struct {
	RunID     string `bson:"run_id"`
	Namespace string `bson:"namespace"`

	// For TimerTaskStepWaitForTimer: debug-only. Records which step triggered
	// timer creation, but the timer may fire and be used by a different step
	// due to lazy timer reuse.
	CreatedByStepExeID string `bson:"created_by_step_exe_id,omitempty"`
}

type TimerTaskRow struct {
	ShardID   int32         `bson:"shard_id"`
	RowType   RowType       `bson:"row_type"`
	Namespace string        `bson:"namespace"`
	SortKey   int64         `bson:"sort_key"`
	ID        ids.TaskID    `bson:"id"`
	TaskType  TimerTaskType `bson:"task_type"`
	TaskInfo  TimerTaskInfo `bson:"task_info"`
	CreatedAt time.Time     `bson:"created_at"`
}

// ============================================================================
// OpsFIFO Task Row (observability outbox)
// ============================================================================

// OpsFIFOTaskRow is a single FIFO observability task: either a HistoryWrite
// (carrying the HistoryEvent the OpsFIFO writer will insert) or a
// VisibilityWrite (carrying the VisibilityEntry to upsert). Sharded with the
// run state in the runs collection (RowType=4) so the same per-shard sequence
// machinery serializes them with the rest of the outbox.
//
// SortKey is the per-shard OpsFIFO TaskSeq (RangeID<<32 | LocalSeq), allocated
// under a per-shard OpsFIFO seq lock that is INDEPENDENT from the immediate
// task seq lock (so the two queues don't contend). The OpsFIFO batch reader
// scans by afterSeq using the same monotonic-visibility guarantee that the
// immediate batch reader relies on.
//
// HistoryPayload and VisibilityPayload are mutually exclusive: exactly one is
// non-nil, selected by TaskType.
type OpsFIFOTaskRow struct {
	ShardID   int32           `bson:"shard_id"`
	RowType   RowType         `bson:"row_type"`
	Namespace string          `bson:"namespace"`
	SortKey   int64           `bson:"sort_key"`
	ID        ids.TaskID      `bson:"id"`
	TaskType  OpsFIFOTaskType `bson:"task_type"`

	// HistoryPayload is set when TaskType == OpsFIFOTaskHistoryWrite.
	HistoryPayload *HistoryEvent `bson:"history_payload,omitempty"`
	// VisibilityPayload is set when TaskType == OpsFIFOTaskVisibilityWrite.
	VisibilityPayload *VisibilityEntry `bson:"visibility_payload,omitempty"`

	CreatedAt time.Time `bson:"created_at"`
}

// TaskRow is a union type for CreateRunWithTasks / UpdateRunWithNewTasks.
// Exactly one of Immediate / Timer / OpsFIFO is non-nil.
type TaskRow struct {
	Immediate *ImmediateTaskRow
	Timer     *TimerTaskRow
	OpsFIFO   *OpsFIFOTaskRow
}

// ============================================================================
// Blob
// ============================================================================

type BlobEntry struct {
	BlobID   ids.BlobID
	Encoding string
	Payload  []byte
}

// ============================================================================
// Shard
// ============================================================================

// ShardMetadata stores committed task offsets in the shard document.
// Updated atomically with lease renewal so the next shard owner can resume
// batch reading from the correct position.
type ShardMetadata struct {
	// RangeID is incremented on each ClaimShard. Used as the upper 32 bits of
	// immediate task SortKey (TaskSeq = RangeID<<32 | LocalSeq) to guarantee
	// monotonically increasing task IDs across shard ownership changes.
	RangeID int32 `bson:"range_id" json:"range_id"`

	// ImmediateTaskCommittedSeq is the watermark up to which immediate tasks
	// have been committed (processed and safe to delete). The next owner
	// starts reading from this offset.
	ImmediateTaskCommittedSeq int64 `bson:"immediate_task_committed_seq" json:"immediate_task_committed_seq"`

	// TimerTaskCommittedSortKey + TimerTaskCommittedID form the compound
	// watermark for timer tasks. Timer tasks use (SortKey, ID) as their
	// ordering key since multiple timers can share the same SortKey.
	TimerTaskCommittedSortKey int64      `bson:"timer_task_committed_sort_key" json:"timer_task_committed_sort_key"`
	TimerTaskCommittedID      ids.TaskID `bson:"timer_task_committed_id" json:"timer_task_committed_id"`

	// OpsFIFOTaskCommittedSeq is the watermark up to which OpsFIFO tasks
	// have been processed (and are safe to delete from the runs collection).
	// The next shard owner's OpsFIFO reader starts from this offset. The
	// OpsFIFO deleter advances this monotonically because the inline batch
	// executor completes whole batches atomically — no out-of-order
	// completions to track. See docs/ops-fifo-queue-design.md.
	OpsFIFOTaskCommittedSeq int64 `bson:"ops_fifo_task_committed_seq" json:"ops_fifo_task_committed_seq"`
}

type Shard struct {
	ShardID        int32
	Version        int64
	MemberID       string
	ClaimedAt      time.Time
	LeaseExpiresAt time.Time
	ReleasedAt     *time.Time
	Metadata       ShardMetadata
}

// ============================================================================
// Tasklist
// ============================================================================

// TasklistMetadata is the owner row for a tasklist partition.
// One row per (namespace, tasklist_name, partition_id) in the tasklist_metadata collection.
type TasklistMetadata struct {
	Namespace     string
	TasklistName  string
	PartitionID   int32
	RangeID       int32 // fencing token, incremented on each ClaimTasklist
	AckLevel      int64 // watermark: all task_ids <= ack_level have been processed
	OwnerMemberID string
	OwnerAddress  string // matching gRPC address for routing
	ClaimedAt     time.Time
}

// TasklistTaskRow is a single dispatch task in the tasklist_tasks collection.
type TasklistTaskRow struct {
	Namespace    string
	TasklistName string
	PartitionID  int32
	TaskID       int64 // (int64(rangeID) << 32) | int64(localSeq)
	RunID        string
	ShardID      int32
	CreatedAt    time.Time
}

// ChannelMessage pairs a monotonically increasing ID with a Value.
// Used in RunRow.UnconsumedChannelMessages for catch-up tracking.
// ID is run-scoped (= ExternalChannelMessageCounter at assignment time).
type ChannelMessage struct {
	ID    int64 `bson:"id"`
	Value Value `bson:"value"`
}

// ============================================================================
// Store Interfaces
// ============================================================================

// ShardReleaseEntry identifies a shard to release with its expected version.
type ShardReleaseEntry struct {
	ShardID         int32
	ExpectedVersion int64
}

// ShardStore manages shard ownership.
type ShardStore interface {
	// ClaimShard claims a shard, incrementing RangeID in metadata. Returns the
	// shard with updated RangeID so the caller can initialize TaskSeq generation.
	ClaimShard(ctx context.Context, shardID int32, memberID string, leaseDuration time.Duration) (*Shard, errors.CategorizedError)
	// RenewShardLease renews the lease and atomically persists committed task
	// offsets from metadata. This piggybacks offset commits on the lease renewal
	// to save a separate DB round-trip.
	RenewShardLease(ctx context.Context, shardID int32, memberID string, expectedVersion int64, leaseDuration time.Duration, metadata *ShardMetadata) (leaseExpiresAt time.Time, _ errors.CategorizedError)
	ReleaseShard(ctx context.Context, shardID int32, memberID string, expectedVersion int64) errors.CategorizedError
	BatchReleaseShards(ctx context.Context, memberID string, entries []ShardReleaseEntry) errors.CategorizedError
	Close() error
}

// ReadPreference selects which replica node serves a read.
//
// Note: even when SecondaryPreferred is selected, all writes still go to the
// primary, so any read-then-CAS path that uses a secondary may see a stale
// Version and trigger a conflict + retry loop bounded by replica lag.
// Only use SecondaryPreferred for truly read-only call sites (visibility
// APIs such as engine.GetRun); never for read-then-CAS paths (StartRun,
// StopRun, ProcessStep*, Heartbeat, HandleRunDispatchResult, etc.) which
// would otherwise spin in CAS retries.
type ReadPreference int

const (
	// ReadPrefDefault routes the read to the primary (matches the global
	// client default and preserves CAS semantics).
	ReadPrefDefault ReadPreference = 0
	// ReadPrefSecondaryPreferred prefers a secondary; falls back to the
	// primary if no secondary is available.
	ReadPrefSecondaryPreferred ReadPreference = 1
)

// GetRunOptions tunes a single GetRun call.
type GetRunOptions struct {
	ReadPreference ReadPreference
}

// RunStore manages run rows, immediate tasks, and timer tasks (all in the runs collection).
type RunStore interface {
	// CreateRunWithTasks atomically inserts a run_row and one or more task rows.
	CreateRunWithTasks(ctx context.Context, run *RunRow, tasks []TaskRow) errors.CategorizedError

	// GetRun reads the current state of a run.
	// opts.ReadPreference controls the routing of the read; the zero value
	// (ReadPrefDefault) preserves the historical primary-only behavior.
	GetRun(ctx context.Context, shardID int32, namespace, runID string, opts GetRunOptions) (*RunRow, errors.CategorizedError)

	// UpdateRunWithNewTasks does a CAS update on the run_row (version check)
	// and atomically inserts new task rows. Returns CASError on version mismatch.
	UpdateRunWithNewTasks(ctx context.Context, shardID int32, namespace, runID string,
		expectedVersion int64, update *RunRowUpdate, newTasks []TaskRow) errors.CategorizedError

	// Task range reading — ordered by (sort_key, id) using the pk_idx index.
	// Immediate tasks: sort_key is TaskSeq (RangeID<<32 | LocalSeq), afterSeq-based cursor.
	// Timer tasks: sort_key is fire_at_unix_ms, compound (afterSortKey, afterID) cursor.
	// OpsFIFO tasks: sort_key is per-shard OpsFIFO TaskSeq, afterSeq-based cursor (same shape as immediate).
	RangeReadImmediateTasks(ctx context.Context, shardID int32, afterSeq int64, limit int) ([]*ImmediateTaskRow, errors.CategorizedError)
	RangeReadTimerTasks(ctx context.Context, shardID int32, sortKeyUpTo int64, afterSortKey int64, afterID ids.TaskID, limit int) ([]*TimerTaskRow, errors.CategorizedError)
	RangeReadOpsFIFOTasks(ctx context.Context, shardID int32, afterSeq int64, limit int) ([]*OpsFIFOTaskRow, errors.CategorizedError)

	// Task range deletion — deletes all tasks up to the given watermark.
	// Immediate: deletes sort_key <= upToSeq (inclusive, watermark is min-1).
	// Timer: deletes (sort_key, id) < (upToSortKey, upToID) (exclusive, watermark is min of pending).
	// OpsFIFO: deletes sort_key <= upToSeq (inclusive, watermark is the highest seq of the
	//      most recently completed batch — see ops_batch_deleter for why this can be
	//      a simple atomic int64 rather than a min-of-pending).
	RangeDeleteImmediateTasks(ctx context.Context, shardID int32, upToSeq int64) errors.CategorizedError
	RangeDeleteTimerTasks(ctx context.Context, shardID int32, upToSortKey int64, upToID ids.TaskID) errors.CategorizedError
	RangeDeleteOpsFIFOTasks(ctx context.Context, shardID int32, upToSeq int64) errors.CategorizedError

	// Task deletion by ID batch — used only during shutdown to clean up tasks
	// that completed above the watermark but haven't been range-deleted yet.
	DeleteImmediateTasksByIDBatch(ctx context.Context, shardID int32, taskIDs []ids.TaskID) errors.CategorizedError
	DeleteTimerTasksByIDBatch(ctx context.Context, shardID int32, taskIDs []ids.TaskID) errors.CategorizedError
	DeleteOpsFIFOTasksByIDBatch(ctx context.Context, shardID int32, taskIDs []ids.TaskID) errors.CategorizedError

	// DeleteAll removes all rows (runs + tasks) from the runs collection.
	// Test-only: used to ensure clean state between tests.
	DeleteAll(ctx context.Context) error

	Close() error
}

// BlobStore manages large value storage.
type BlobStore interface {
	BatchInsertBlobs(ctx context.Context, shardID int32, namespace, runID string, blobs []BlobEntry) errors.CategorizedError
	BatchGetBlobs(ctx context.Context, shardID int32, namespace, runID string, blobIDs []ids.BlobID) ([]BlobEntry, errors.CategorizedError)
	Close() error
}

// ============================================================================
// Visibility Store
// ============================================================================

// VisibilityEntry is one row in the visibility collection. The shard key is
// `namespace`; the PK is (namespace, run_id). StartTime is immutable after the
// first write (the upsert uses $setOnInsert); UpdatedAt moves forward on
// every status change. For terminal statuses, UpdatedAt doubles as the end
// time, which is why there is no separate EndTime field — see
// docs/visibility-store-design.md.
type VisibilityEntry struct {
	Namespace    string
	RunID        string
	FlowType     string
	TaskListName string
	Status       RunStatus
	StartTime    time.Time
	UpdatedAt    time.Time
}

// ListRunsOrderBy selects which compound index serves a ListRuns
// query. Both supported orderings are descending so the most recent runs
// appear first.
type ListRunsOrderBy int32

const (
	// ListByStartTimeDesc orders by visibility.start_time DESC and uses the
	// (namespace, flow_type, status, start_time) compound index.
	ListByStartTimeDesc ListRunsOrderBy = 0
	// ListByUpdatedAtDesc orders by visibility.updated_at DESC and uses the
	// (namespace, flow_type, status, updated_at) compound index. For terminal
	// statuses this is effectively "by end time"; for active statuses it
	// surfaces "recently active" runs.
	ListByUpdatedAtDesc ListRunsOrderBy = 1
)

// ListRunsQuery describes a single page of a ListRuns call.
//
// Namespace is required (every supported index is namespace-prefixed and
// the visibility collection is sharded by namespace).
//
// FlowType and Status are optional filters:
//   - FlowType == "" means "any flow type" (no filter applied).
//   - Status  == nil means "any status" (no filter applied).
//
// When either is omitted, the supported (namespace, flow_type, status, ...)
// compound indexes lose their prefix match and the visibility query falls
// back to a namespace-shard scan. Acceptable for typical ops volumes;
// adding (namespace, status, ...) and (namespace, ...) indexes is a
// follow-up if/when scale demands.
type ListRunsQuery struct {
	Namespace string
	FlowType  string
	Status    *RunStatus
	OrderBy   ListRunsOrderBy
	// Limit caps the page size. Callers should also cap; the store enforces
	// an upper bound (1000) defensively.
	Limit int
	// PageToken is an opaque cursor returned by a previous page (empty for
	// the first page). The encoding is <unix_millis>:<run_id> — produced by
	// the store; callers should not parse it.
	PageToken string
}

// ListRunsResult is one page plus a cursor for the next page (empty when
// the result set is exhausted).
type ListRunsResult struct {
	Entries       []VisibilityEntry
	NextPageToken string
}

// ============================================================================
// History Store
// ============================================================================

// HistoryEvent is one append-only record in the history collection. Sharded
// by run_id; PK is (run_id, event_id). EventID is monotonically allocated
// per-run on the run row (RunRow.LastHistoryEventID) under the same CAS that
// commits the run state change, so there are no gaps within a successful run
// and replays of the same OpsFIFO batch produce the same EventIDs.
//
// Payload is a strongly-typed sum-type — exactly one of its variant pointers
// is non-nil. The active variant discriminates the event type; the OpsService
// handler maps the active variant to the matching pb.HistoryEvent oneof
// directly (no manual type+bytes plumbing).
type HistoryEvent struct {
	Namespace    string
	RunID        string
	EventID      int64
	OccurredAtMs int64
	// WorkerID is the worker host_id that produced the underlying event, when
	// known. Empty for events originated by API callers (StartRun, StopRun,
	// PublishToChannel) and for engine-internal events (timer fires, async
	// match transitions). Best-effort.
	WorkerID string
	Payload  HistoryEventPayload
}

// HistoryEventPayload is the discriminated union of every history event
// variant. Exactly one pointer field must be set; HistoryStore validates
// this on insert. We use a sum-type struct (rather than a Go interface
// implemented by the pb-generated types) so the field is visibly exhaustive
// and Go's type system guarantees no third-party type can sneak in.
//
// The concrete variant types are the proto-generated Go structs in
// dexpb — by reusing them we avoid mirroring every nested proto field
// (Value, NextStep, ConditionResult, etc.) into the persistence package.
type HistoryEventPayload struct {
	RunStart             *pb.HistoryRunStartPayload
	RunStop              *pb.HistoryRunStopPayload
	StepExecuteCompleted *pb.HistoryStepExecuteCompletedPayload
	StepWaitForCompleted *pb.HistoryStepWaitForCompletedPayload
	ChannelPublish       *pb.HistoryChannelPublishPayload
	StepsUnblocked       *pb.HistoryStepsUnblockedPayload
	RunFork              *pb.HistoryRunForkPayload
}

// Validate returns an error if more or fewer than one variant is set.
// Mongo HistoryStore calls this before insert so a malformed event is
// rejected at the store boundary instead of producing a silent-no-op
// document.
func (p HistoryEventPayload) Validate() error {
	n := 0
	for _, set := range []bool{
		p.RunStart != nil,
		p.RunStop != nil,
		p.StepExecuteCompleted != nil,
		p.StepWaitForCompleted != nil,
		p.ChannelPublish != nil,
		p.StepsUnblocked != nil,
		p.RunFork != nil,
	} {
		if set {
			n++
		}
	}
	if n != 1 {
		return fmt.Errorf("HistoryEventPayload must have exactly one variant set, got %d", n)
	}
	return nil
}

// MarshalBSON serializes HistoryEventPayload as a BSON sub-document containing
// the variant's stable type name plus its proto-marshaled bytes:
//
//	{"type": "channel_publish", "data": <binary>}
//
// This is required because the embedded pb.History*Payload messages contain
// pb.Value with a `Kind isValue_Kind` oneof interface that the default BSON
// codec cannot round-trip (interface fields don't preserve their concrete
// type on decode, so the variant — including EncodedObject /
// EncodedObjectBlobIdInternalOnly — would be silently dropped). Proto's
// own marshal/unmarshal preserves oneofs correctly.
//
// Used wherever HistoryEventPayload appears as a struct field encoded by
// the default BSON codec — chiefly inside OpsFIFOTaskRow.HistoryPayload,
// which the OpsFIFO writer/reader pipeline persists in the runs collection.
// Mongo HistoryStore has its own marshal path (see marshalHistoryPayload)
// and does not rely on this method.
func (p HistoryEventPayload) MarshalBSON() ([]byte, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	var msg proto.Message
	switch {
	case p.RunStart != nil:
		msg = p.RunStart
	case p.RunStop != nil:
		msg = p.RunStop
	case p.StepExecuteCompleted != nil:
		msg = p.StepExecuteCompleted
	case p.StepWaitForCompleted != nil:
		msg = p.StepWaitForCompleted
	case p.ChannelPublish != nil:
		msg = p.ChannelPublish
	case p.StepsUnblocked != nil:
		msg = p.StepsUnblocked
	case p.RunFork != nil:
		msg = p.RunFork
	}
	data, err := proto.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("HistoryEventPayload.MarshalBSON: proto.Marshal: %w", err)
	}
	return bson.Marshal(bson.M{"type": p.TypeName(), "data": data})
}

// UnmarshalBSON is the inverse of MarshalBSON: routes "data" bytes into
// the variant pointer named by "type". Unknown type names produce an
// error rather than silently dropping the payload.
func (p *HistoryEventPayload) UnmarshalBSON(raw []byte) error {
	var doc struct {
		Type string `bson:"type"`
		Data []byte `bson:"data"`
	}
	if err := bson.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("HistoryEventPayload.UnmarshalBSON: %w", err)
	}
	*p = HistoryEventPayload{}
	switch doc.Type {
	case "run_start":
		out := &pb.HistoryRunStartPayload{}
		if err := proto.Unmarshal(doc.Data, out); err != nil {
			return fmt.Errorf("HistoryEventPayload.UnmarshalBSON run_start: %w", err)
		}
		p.RunStart = out
	case "run_stop":
		out := &pb.HistoryRunStopPayload{}
		if err := proto.Unmarshal(doc.Data, out); err != nil {
			return fmt.Errorf("HistoryEventPayload.UnmarshalBSON run_stop: %w", err)
		}
		p.RunStop = out
	case "step_execute_completed":
		out := &pb.HistoryStepExecuteCompletedPayload{}
		if err := proto.Unmarshal(doc.Data, out); err != nil {
			return fmt.Errorf("HistoryEventPayload.UnmarshalBSON step_execute_completed: %w", err)
		}
		p.StepExecuteCompleted = out
	case "step_wait_for_completed":
		out := &pb.HistoryStepWaitForCompletedPayload{}
		if err := proto.Unmarshal(doc.Data, out); err != nil {
			return fmt.Errorf("HistoryEventPayload.UnmarshalBSON step_wait_for_completed: %w", err)
		}
		p.StepWaitForCompleted = out
	case "channel_publish":
		out := &pb.HistoryChannelPublishPayload{}
		if err := proto.Unmarshal(doc.Data, out); err != nil {
			return fmt.Errorf("HistoryEventPayload.UnmarshalBSON channel_publish: %w", err)
		}
		p.ChannelPublish = out
	case "steps_unblocked":
		out := &pb.HistoryStepsUnblockedPayload{}
		if err := proto.Unmarshal(doc.Data, out); err != nil {
			return fmt.Errorf("HistoryEventPayload.UnmarshalBSON steps_unblocked: %w", err)
		}
		p.StepsUnblocked = out
	case "run_fork":
		out := &pb.HistoryRunForkPayload{}
		if err := proto.Unmarshal(doc.Data, out); err != nil {
			return fmt.Errorf("HistoryEventPayload.UnmarshalBSON run_fork: %w", err)
		}
		p.RunFork = out
	default:
		return fmt.Errorf("HistoryEventPayload.UnmarshalBSON: unknown type %q", doc.Type)
	}
	return nil
}

// TypeName returns a stable string discriminator used as the BSON
// `payload_type` field by the mongo HistoryStore. Kept stable across
// releases — never rename existing entries.
func (p HistoryEventPayload) TypeName() string {
	switch {
	case p.RunStart != nil:
		return "run_start"
	case p.RunStop != nil:
		return "run_stop"
	case p.StepExecuteCompleted != nil:
		return "step_execute_completed"
	case p.StepWaitForCompleted != nil:
		return "step_wait_for_completed"
	case p.ChannelPublish != nil:
		return "channel_publish"
	case p.StepsUnblocked != nil:
		return "steps_unblocked"
	case p.RunFork != nil:
		return "run_fork"
	default:
		return ""
	}
}

// HistoryStore is the read+write surface for the history collection. Writes
// come exclusively from the OpsFIFO batch executor; reads are served by
// OpsService.GetHistoryEvents.
type HistoryStore interface {
	// BatchInsertHistory inserts every event. Uses ordered=false +
	// continue-on-error semantics so a duplicate (run_id, event_id) — which
	// can happen when the OpsFIFO retries the same batch after a partial
	// failure — is treated as a no-op rather than a hard error.
	BatchInsertHistory(ctx context.Context, events []HistoryEvent) errors.CategorizedError

	// GetHistoryEvents returns events for the run with EventID > afterID,
	// ordered by EventID ASC, capped by limit (defaulted and clamped to
	// 1000 inside the store). Caller is responsible for hydrating any blob
	// refs inside payloads via BlobStore.BatchGetBlobs.
	GetHistoryEvents(ctx context.Context, namespace, runID string, afterID int64, limit int) ([]HistoryEvent, errors.CategorizedError)

	// GetLatestEvent returns the highest-EventID event for the run with its
	// payload decoded (NOT blob-hydrated), or (nil, nil) if none exist. Backs
	// WaitForHistoryEvent's authoritative read: the caller reads the tip from
	// EventID and detects a closed run from Payload.RunStop.
	GetLatestEvent(ctx context.Context, namespace, runID string) (*HistoryEvent, errors.CategorizedError)

	// DeleteAll removes every history row. Test-only.
	DeleteAll(ctx context.Context) error

	Close() error
}

// VisibilityStore provides the read+write surface for the run visibility
// collection. Writes happen exclusively from the OpsFIFO batch executor
// (BatchUpsertVisibility); reads are served by OpsService.ListRuns.
type VisibilityStore interface {
	// BatchUpsertVisibility upserts every entry by (namespace, run_id).
	// `start_time` is set only on insert ($setOnInsert); all other fields
	// are overwritten. Idempotent: replaying the same batch is a no-op
	// because the upserts converge on the latest state.
	BatchUpsertVisibility(ctx context.Context, entries []VisibilityEntry) errors.CategorizedError

	// ListRuns returns one page of runs matching the query. Returns an
	// error if Namespace is empty (every supported index is namespace-
	// prefixed). FlowType and Status are optional filters: empty string /
	// nil pointer mean "any" (no filter applied) — see ListRunsQuery
	// for the perf trade-off.
	ListRuns(ctx context.Context, q ListRunsQuery) (*ListRunsResult, errors.CategorizedError)

	// DeleteAll removes every visibility row. Test-only.
	DeleteAll(ctx context.Context) error

	Close() error
}

// TasklistStore manages tasklist ownership (metadata) and the task dispatch queue.
// Ownership uses Cadence-style fencing (no lease/renew):
//   - ClaimTasklist: unconditional CAS increment of range_id
//   - CreateTasks / UpdateTasklistMetadata: fenced writes (WHERE range_id = :expected)
//   - Read/Delete paths do NOT fence — new owner must drain old owner's tasks
type TasklistStore interface {
	// ClaimTasklist atomically claims ownership of a tasklist partition by
	// incrementing the range_id fencing token (upsert). Any member can claim
	// at any time — previous owner detected as stale on its next fenced write.
	ClaimTasklist(ctx context.Context, namespace, tasklistName string, partitionID int32, memberID, matchingAddress string) (*TasklistMetadata, errors.CategorizedError)

	// UpdateTasklistMetadata performs a fenced update of ack_level on the
	// metadata row. Returns OwnerVersionMismatchError if range_id doesn't match.
	UpdateTasklistMetadata(ctx context.Context, namespace, tasklistName string, partitionID int32, rangeID int32, ackLevel int64) errors.CategorizedError

	// CreateTasks batch-inserts task rows in a single transaction that also
	// verifies range_id on the metadata row (fence). Fence failure rolls back
	// the entire transaction and returns a fencing error.
	CreateTasks(ctx context.Context, namespace, tasklistName string, partitionID int32, rangeID int32, tasks []*TasklistTaskRow) errors.CategorizedError

	// GetTasks reads task rows with task_id in (readLevel, maxReadLevel],
	// ordered by task_id ASC, limited to batchSize. No fence — any owner
	// (including a new one draining old tasks) can read.
	GetTasks(ctx context.Context, namespace, tasklistName string, partitionID int32, readLevel, maxReadLevel int64, batchSize int) ([]*TasklistTaskRow, errors.CategorizedError)

	// DeleteTasksLessThan deletes task rows with task_id <= ackLevel.
	// Returns the number of rows actually
	// deleted. The `limit` arg is a hint only and MAY be ignored by the
	// implementation;
	DeleteTasksLessThan(ctx context.Context, namespace, tasklistName string, partitionID int32, ackLevel int64, limit int) (int, errors.CategorizedError)

	// DeleteTasksByIDBatch deletes task rows by exact task_id list.
	// Used during shutdown to clean up completed-above-watermark tasks.
	// No fence.
	DeleteTasksByIDBatch(ctx context.Context, namespace, tasklistName string, partitionID int32, taskIDs []int64) errors.CategorizedError

	// GetTasklistMetadata reads the metadata row for a tasklist partition.
	GetTasklistMetadata(ctx context.Context, namespace, tasklistName string, partitionID int32) (*TasklistMetadata, errors.CategorizedError)

	Close() error
}

// ============================================================================
// Dead Letter Queue
// ============================================================================

// DLQEntry captures a failed task with full diagnostic context.
// Written to the task_dlq collection when a task exhausts retries.
// Covers both immediate tasks and timer tasks.
type DLQEntry struct {
	ShardID       int32
	TaskID        ids.TaskID
	QueueType     RowType // RowTypeImmediateTask or RowTypeTimerTask
	TaskType      int32   // ImmediateTaskType or TimerTaskType (stored as int32)
	RunID         string
	Namespace     string
	TaskListName  string
	SortKey       int64
	Error         string
	ErrorCategory string
	CreatedAt     time.Time // when original task was created
	DLQAt         time.Time // when written to DLQ
	MemberID      string    // which instance wrote the DLQ entry
}

// DLQStore writes failed tasks to a dead letter queue for operator visibility.
type DLQStore interface {
	// WriteDLQ inserts a failed task entry into the DLQ collection.
	// Best-effort: callers should log but not retry on failure.
	WriteDLQ(ctx context.Context, entry *DLQEntry) errors.CategorizedError
	Close() error
}
