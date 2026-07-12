// Copyright (c) 2017 Uber Technologies, Inc.
// Copyright (c) 2025 superdurable
// SPDX-License-Identifier: MIT

package tag

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/superdurable/dex/server/common/errors"
	"github.com/superdurable/dex/server/common/utils/ptr"
)

// LoggingCallAtKey is reserved tag
const LoggingCallAtKey = "_log-at"
const ErrorCallAtKey = "_err-at"

// ============ Common Tags ============

// Error returns tag for Error, with support for CategorizedError full chain
func Error(err error) Tag {
	var errStr string
	var errCallAt *string
	// Get formated error for logging to include inner error strings when available
	if err == nil {
		errStr = "nil"
	} else {
		if catErr, ok := err.(errors.CategorizedError); ok {
			errStr = catErr.GetFullError()
			errCallAt = ptr.Any(catErr.GetCallAtPath())
		} else {
			errStr = err.Error()
		}
	}

	t := Tag{attr: slog.String("error", errStr)}
	if errCallAt != nil {
		t.setExtra([]slog.Attr{slog.String(ErrorCallAtKey, *errCallAt)})
	}
	return t
}

// Key returns tag for Key
func Key(key string) Tag {
	return Tag{attr: slog.String("key", key)}
}

// Value returns tag for value
func Value(val any) Tag {
	return Tag{attr: slog.String("value", fmt.Sprintf("%v", val))}
}

// ID creates an ID tag
func ID(v string) Tag {
	return Tag{attr: slog.String("id", v)}
}

// Mode creates a mode tag
func Mode(mode string) Tag {
	return Tag{attr: slog.String("mode", mode)}
}

// StatusCode creates a status code tag
func StatusCode[T Integer](status T) Tag {
	return Tag{attr: slog.Int64("status_code", int64(status))}
}

// Address creates an address tag (for HTTP/network addresses)
func Address(addr string) Tag {
	return Tag{attr: slog.String("address", addr)}
}

// DatabaseType creates a database type tag
func DatabaseType(dbType string) Tag {
	return Tag{attr: slog.String("database_type", dbType)}
}

// Attempt creates an attempt number tag
func Attempt[T Integer](attempt T) Tag {
	return Tag{attr: slog.Int64("attempt", int64(attempt))}
}

// Duration creates a duration tag
func Duration(d time.Duration) Tag {
	return Tag{attr: slog.Duration("duration", d)}
}

// Count creates a count tag
func Count[T Integer](c T) Tag {
	return Tag{attr: slog.Int64("count", int64(c))}
}

// Version creates a version tag
func Version[T Integer](v T) Tag {
	return Tag{attr: slog.Int64("version", int64(v))}
}

// Shard creates a shard ID tag
func Shard[T Integer](id T) Tag {
	return Tag{attr: slog.Int64("shard_id", int64(id))}
}

// TaskID creates a task ID tag
func TaskID(id string) Tag {
	return Tag{attr: slog.String("task_id", id)}
}

// TaskType creates a task type tag
func TaskType[T Integer](t T) Tag {
	return Tag{attr: slog.Int64("task_type", int64(t))}
}

// RunID creates a run ID tag
func RunID(id string) Tag {
	return Tag{attr: slog.String("run_id", id)}
}

// TaskListName creates a tasklist name tag.
func TaskListName(name string) Tag {
	return Tag{attr: slog.String("task_list_name", name)}
}

// PartitionID creates a tasklist partition ID tag.
func PartitionID[T Integer](id T) Tag {
	return Tag{attr: slog.Int64("partition_id", int64(id))}
}

// AckLevel creates a tasklist ack level tag.
func AckLevel[T Integer](v T) Tag {
	return Tag{attr: slog.Int64("ack_level", int64(v))}
}

// WorkerID creates a worker ID tag.
func WorkerID(id string) Tag {
	return Tag{attr: slog.String("worker_id", id)}
}

// Source creates a source tag (e.g., where a join/connection came from)
func Source(s string) Tag {
	return Tag{attr: slog.String("source", s)}
}

// Provider creates a provider tag
func Provider(p string) Tag {
	return Tag{attr: slog.String("provider", p)}
}

// Namespace creates a namespace tag
func Namespace(ns string) Tag {
	return Tag{attr: slog.String("namespace", ns)}
}

// MemberId creates a member ID tag
func MemberId(id string) Tag {
	return Tag{attr: slog.String("member_id", id)}
}

// NodeName creates a node name tag
func NodeName(name string) Tag {
	return Tag{attr: slog.String("node_name", name)}
}

// NumMembers creates a current member count tag
func NumMembers[T Integer](n T) Tag {
	return Tag{attr: slog.Int64("num_members", int64(n))}
}

// MinMembers creates a min members tag
func MinMembers[T Integer](n T) Tag {
	return Tag{attr: slog.Int64("min_members", int64(n))}
}

// NumberOfShards creates a shard count tag
func NumberOfShards[T Integer](n T) Tag {
	return Tag{attr: slog.Int64("number_of_shards", int64(n))}
}

// Method creates a method name tag
func Method(m string) Tag {
	return Tag{attr: slog.String("method", m)}
}

// Operation creates an operation name tag
func Operation(op string) Tag {
	return Tag{attr: slog.String("operation", op)}
}

// Component creates a component name tag
func Component(c string) Tag {
	return Tag{attr: slog.String("component", c)}
}

// Service creates a service name tag
func Service(s string) Tag {
	return Tag{attr: slog.String("service", s)}
}

// Message creates a message tag
func Message(msg string) Tag {
	return Tag{attr: slog.String("message", msg)}
}

// JsonValue creates a JSON value tag
func JsonValue(v interface{}) Tag {
	bs, err := json.Marshal(v)
	str := string(bs)
	if err != nil {
		str = "failed to marshal to json"
	}
	return Tag{attr: slog.String("json_value", str)}
}

// Timestamp creates a timestamp tag
func Timestamp(t time.Time) Tag {
	return Tag{attr: slog.Time("timestamp", t)}
}

// APIName creates an API/method name tag (e.g., gRPC full method)
func APIName(name string) Tag {
	return Tag{attr: slog.String("api", name)}
}

// GRPCCode creates a gRPC status code tag
func GRPCCode(code string) Tag {
	return Tag{attr: slog.String("grpc_code", code)}
}

// ChannelName creates a channel name tag
func ChannelName(name string) Tag {
	return Tag{attr: slog.String("channel", name)}
}

// Surplus creates a surplus count tag
func Surplus[T Integer](n T) Tag {
	return Tag{attr: slog.Int64("surplus", int64(n))}
}

// SuccessCount creates a success count tag
func SuccessCount[T Integer](n T) Tag {
	return Tag{attr: slog.Int64("success", int64(n))}
}

// FailCount creates a failure count tag
func FailCount[T Integer](n T) Tag {
	return Tag{attr: slog.Int64("failed", int64(n))}
}

// RangeID creates a range ID tag
func RangeID[T Integer](id T) Tag {
	return Tag{attr: slog.Int64("range_id", int64(id))}
}

// MaxRetries creates a max retries tag
func MaxRetries[T Integer](n T) Tag {
	return Tag{attr: slog.Int64("max_retries", int64(n))}
}

// Watermark creates a watermark tag
func Watermark[T Integer](wm T) Tag {
	return Tag{attr: slog.Int64("watermark", int64(wm))}
}

// WatermarkSortKey creates a watermark sort key tag
func WatermarkSortKey[T Integer](wm T) Tag {
	return Tag{attr: slog.Int64("watermark_sort_key", int64(wm))}
}

// OwnedCount creates an owned count tag (e.g., shards or tasklist
// partitions currently held)
func OwnedCount[T Integer](n T) Tag {
	return Tag{attr: slog.Int64("owned", int64(n))}
}

// DesiredCount creates a desired count tag (e.g., target shard / tasklist
// partition count after rebalance)
func DesiredCount[T Integer](n T) Tag {
	return Tag{attr: slog.Int64("desired", int64(n))}
}

// ReleaseCount creates a release count tag (e.g., shards / tasklist
// partitions to give up)
func ReleaseCount[T Integer](n T) Tag {
	return Tag{attr: slog.Int64("to_release", int64(n))}
}

// ClaimCount creates a claim count tag (e.g., shards / tasklist
// partitions to acquire)
func ClaimCount[T Integer](n T) Tag {
	return Tag{attr: slog.Int64("to_claim", int64(n))}
}

// FromStatus creates a from-status tag for status transitions
func FromStatus(s string) Tag {
	return Tag{attr: slog.String("from_status", s)}
}

// ToStatus creates a to-status tag for status transitions
func ToStatus(s string) Tag {
	return Tag{attr: slog.String("to_status", s)}
}

// Reason creates a reason tag
func Reason(r string) Tag {
	return Tag{attr: slog.String("reason", r)}
}

// CriticalCodeBug creates a critical code bug tag for errors
func CriticalCodeBug() Tag {
	return Tag{attr: slog.Bool("_critical-code-bug-err", true)}
}

// ============ Benchmark Tags ============

// NumSteps creates a num_steps tag for benchmark step count
func NumSteps[T Integer](n T) Tag {
	return Tag{attr: slog.Int64("num_steps", int64(n))}
}

// StateSize creates a state_size tag for benchmark state payload size
func StateSize[T Integer](n T) Tag {
	return Tag{attr: slog.Int64("state_size", int64(n))}
}

// RunStatus creates a run_status tag for the numeric run status code
func RunStatus[T Integer](status T) Tag {
	return Tag{attr: slog.Int64("run_status", int64(status))}
}

// StatusName creates a status_name tag for the human-readable run status
func StatusName(name string) Tag {
	return Tag{attr: slog.String("status_name", name)}
}

// ============ WaitFor / Channel / Unblock Tags ============

// UnblockedCount is the number of StepUnblocked entries in a request.
func UnblockedCount[T Integer](n T) Tag {
	return Tag{attr: slog.Int64("unblocked_count", int64(n))}
}

// ConsumedCount is the per-channel consumed_count from a ChannelConditionResult.
func ConsumedCount[T Integer](n T) Tag {
	return Tag{attr: slog.Int64("consumed_count", int64(n))}
}

// ExternalChannelMessageID tags the engine-assigned external_channel_message_id
// for a delivery / ack.
func ExternalChannelMessageID[T Integer](id T) Tag {
	return Tag{attr: slog.Int64("external_channel_message_id", int64(id))}
}

// StepExeID tags a single step_exe_id, e.g. the target of a sibling
// cancellation that the engine could not find.
func StepExeID(id string) Tag {
	return Tag{attr: slog.String("step_exe_id", id)}
}

// ByStepExeID tags the step_exe_id whose decision triggered an
// engine-level effect (e.g. the cancelling step in a sibling-cancel
// commit).
func ByStepExeID(id string) Tag {
	return Tag{attr: slog.String("by_step_exe_id", id)}
}

// StepExeIDs tags a JSON list of step_exe_ids (for log lines that act
// on multiple ids at once, e.g. canceled_step_executions).
func StepExeIDs(ids []string) Tag {
	bs, err := json.Marshal(ids)
	str := string(bs)
	if err != nil {
		str = "failed to marshal to json"
	}
	return Tag{attr: slog.String("step_exe_ids", str)}
}
