package metrics

import (
	"strconv"

	"github.com/superdurable/dex/server/common/errors"
	p "github.com/superdurable/dex/server/internal/persistence"
	"google.golang.org/grpc/status"
)

// all pre-defined tag keys and values

// NOTE: always use "_" and lower case for naming -- Prometheus doesn't allow "-" or ".".
// All the names will be lower-cased in metrics provider.

const (
	tagKeyApiName               tagKey = "api_name"
	tagKeyResponseCode          tagKey = "response_code"
	tagKeyPersistenceMethodName tagKey = "persistence_method_name"
	tagKeyErrorCategory         tagKey = "error_category"
	tagKeyErrorSubCategory      tagKey = "error_sub_category"
	tagKeyHttpStatusCode        tagKey = "http_status_code"
)

func TagApiNameFromProtoFullMethod(fullMethod string) Tag {
	return Tag{
		Key:   tagKeyApiName,
		Value: anyTagValue(fullMethod)}
}

func TagResponseCodeFromProtoError(err error) Tag {
	// NOTE: ignore checking okay because status will be unknown in that case
	status, _ := status.FromError(err)
	codeStr := status.Code().String()
	return Tag{
		Key:   tagKeyResponseCode,
		Value: anyTagValue(codeStr),
	}
}

func TagPersistenceMethodName(methodName string) Tag {
	return Tag{
		Key:   tagKeyPersistenceMethodName,
		Value: anyTagValue(methodName),
	}
}

func TagErrorCategoryFromCategorizedError(err errors.CategorizedError) Tag {
	return Tag{
		Key:   tagKeyErrorCategory,
		Value: anyTagValue(string(err.GetCategory())),
	}
}

func TagErrorCategory(category string) Tag {
	return Tag{
		Key:   tagKeyErrorCategory,
		Value: anyTagValue(category),
	}
}

func TagHttpStatusCodeFromCategorizedError(err errors.CategorizedError) Tag {
	return Tag{
		Key:   tagKeyHttpStatusCode,
		Value: anyTagValue(strconv.Itoa(err.GetHttpStatusCode())),
	}
}

// ============================================================================
// Task tags
// ============================================================================

// TaskQueueType distinguishes between immediate and timer task queues.
type TaskQueueType int8

const (
	TaskQueueImmediate TaskQueueType = 0
	TaskQueueTimer     TaskQueueType = 1
	TaskQueueOpsFIFO   TaskQueueType = 2
)

func (t TaskQueueType) String() string {
	switch t {
	case TaskQueueImmediate:
		return "immediate"
	case TaskQueueTimer:
		return "timer"
	case TaskQueueOpsFIFO:
		return "ops_fifo"
	default:
		return "unknown"
	}
}

// OpsFIFOTaskTargetType distinguishes which downstream store an OpsFIFO
// batch targeted: history or visibility. Used as the {type=...} label on
// ops_fifo_task_batch_executed and ops_fifo_task_batch_execution_duration.
type OpsFIFOTaskTargetType int8

const (
	OpsFIFOTaskTargetHistory    OpsFIFOTaskTargetType = 0
	OpsFIFOTaskTargetVisibility OpsFIFOTaskTargetType = 1
)

func (t OpsFIFOTaskTargetType) String() string {
	switch t {
	case OpsFIFOTaskTargetHistory:
		return "history"
	case OpsFIFOTaskTargetVisibility:
		return "visibility"
	default:
		return "unknown"
	}
}

const (
	tagKeyTaskQueueType     tagKey = "task_queue_type"
	tagKeyImmediateTaskType tagKey = "immediate_task_type"
	tagKeyTimerTaskType     tagKey = "timer_task_type"
	tagKeyOpsFIFOTaskTarget tagKey = "ops_fifo_task_target"
)

// TagTaskQueueType tags by queue kind: "immediate", "timer", or "ops_fifo".
func TagTaskQueueType(qt TaskQueueType) Tag {
	return Tag{Key: tagKeyTaskQueueType, Value: anyTagValue(qt.String())}
}

// TagOpsFIFOTaskTarget tags by OpsFIFO target store: "history" or "visibility".
func TagOpsFIFOTaskTarget(t OpsFIFOTaskTargetType) Tag {
	return Tag{Key: tagKeyOpsFIFOTaskTarget, Value: anyTagValue(t.String())}
}

// TagImmediateTaskType tags by specific immediate task type enum.
func TagImmediateTaskType(tt p.ImmediateTaskType) Tag {
	var name string
	switch tt {
	case p.ImmediateTaskRunInitialDispatch:
		name = "initial_dispatch"
	case p.ImmediateTaskRunResumeDispatch:
		name = "resume_dispatch"
	default:
		name = "unknown"
	}
	return Tag{Key: tagKeyImmediateTaskType, Value: anyTagValue(name)}
}

const (
	tagKeyNamespace      tagKey = "namespace"
	tagKeyErrorKind      tagKey = "error_kind"
	tagKeyPartitionRole  tagKey = "partition_role"
	tagKeyForkRunOutcome tagKey = "fork_run_outcome"
)

// TagPartitionRole tags a tasklist metric by partition tree position:
// "root" (partition 0) or "non_root" (a leaf partition that fans in).
func TagPartitionRole(isRoot bool) Tag {
	role := "non_root"
	if isRoot {
		role = "root"
	}
	return Tag{Key: tagKeyPartitionRole, Value: anyTagValue(role)}
}

// TagNamespace tags a metric by the namespace it was emitted under. Used by
// engine-level Counters/Latencies that touch namespaced run state. Not part
// of the gRPC tag family because gRPC metrics already key off api_name.
func TagNamespace(ns string) Tag {
	return Tag{Key: tagKeyNamespace, Value: anyTagValue(ns)}
}

// TagErrorKind tags ProcessStepsUnblocked failure metrics by error class
// ("cas_exhausted", "run_terminated", "invalid_request").
func TagErrorKind(kind string) Tag {
	return Tag{Key: tagKeyErrorKind, Value: anyTagValue(kind)}
}

// TagTimerTaskType tags by specific timer task type enum.
func TagTimerTaskType(tt p.TimerTaskType) Tag {
	var name string
	switch tt {
	case p.TimerTaskRunHeartbeat:
		name = "heartbeat"
	case p.TimerTaskStepWaitForTimer:
		name = "step_waitfor"
	default:
		name = "unknown"
	}
	return Tag{Key: tagKeyTimerTaskType, Value: anyTagValue(name)}
}

func TagErrorSubCategoryFromCategorizedError(err errors.CategorizedError) Tag {
	subCategory := err.GetSubCategory()
	if subCategory == "" && errors.CategoryHasSubCategories(err.GetCategory()) {
		subCategory = "unknown"
	}
	return Tag{
		Key:   tagKeyErrorSubCategory,
		Value: anyTagValue(subCategory),
	}
}
