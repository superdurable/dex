// Copyright (c) 2017 Uber Technologies, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package tag

import (
	"fmt"
	"time"

	"github.com/superdurable/dex/gen/dexpb"
)

// LoggingCallAtKey is reserved tag
const LoggingCallAtKey = "logging-call-at"

// All logging tags are defined in this file.
// To help finding available tags, we recommend that all tags to be categorized and placed in the corresponding section.
// We currently have those categories:
//   0. Common tags that can't be categorized(or belong to more than one)
//   1. Workflow: these tags are information that are useful to our customer, like workflow-id/run-id/task-list/...
//   2. System : these tags are internal information which usually cannot be understood by our customers,

///////////////////  Common tags defined here ///////////////////

// Error returns tag for Error
func Error(err error) Tag {
	return newErrorTag("error", err)
}

// Interval returns the polling interval tag.
func Interval(interval time.Duration) Tag {
	return newDurationTag("interval", interval)
}

// Elapsed returns the elapsed duration tag.
func Elapsed(elapsed time.Duration) Tag {
	return newDurationTag("elapsed", elapsed)
}

// IndexCount returns the attribute index count tag.
func IndexCount(count int) Tag {
	return newInt("index-count", count)
}

// Requested returns requested attribute indexes.
func Requested(indexes map[string]dexpb.IndexType) Tag {
	return newObjectTag("requested", indexes)
}

// Missing returns missing attribute indexes.
func Missing(indexes map[string]dexpb.IndexType) Tag {
	return newObjectTag("missing", indexes)
}

// Indexes returns attribute indexes.
func Indexes(indexes map[string]dexpb.IndexType) Tag {
	return newObjectTag("indexes", indexes)
}

// Panic returns the recovered panic tag.
func Panic(recovered string) Tag {
	return newStringTag("panic", recovered)
}

// RequestID returns the service request ID tag.
func RequestID(requestID string) Tag {
	return newStringTag("request-id", requestID)
}

// HostID returns the service host ID tag.
func HostID(hostID string) Tag {
	return newStringTag("host-id", hostID)
}

// Bucket returns the object storage bucket tag.
func Bucket(bucket string) Tag {
	return newStringTag("bucket", bucket)
}

// Path returns the object storage path tag.
func Path(path string) Tag {
	return newStringTag("path", path)
}

// StoreID returns the blob store ID tag.
func StoreID(storeID string) Tag {
	return newStringTag("store-id", storeID)
}

// WorkflowPath returns the workflow object prefix tag.
func WorkflowPath(workflowPath string) Tag {
	return newStringTag("workflow-path", workflowPath)
}

// StepExecutionID returns the Step execution ID tag.
func StepExecutionID(stepExecutionID string) Tag {
	return newStringTag("step-execution-id", stepExecutionID)
}

// StepEventInputMethod returns the Step method snapshot tag.
func StepEventInputMethod(method string) Tag {
	return newStringTag("step-event-input-method", method)
}

// ResultMetadata returns storage response metadata.
func ResultMetadata(metadata string) Tag {
	return newStringTag("result-metadata", metadata)
}

// TotalDeleted returns the deleted object count tag.
func TotalDeleted(total int) Tag {
	return newInt("total-deleted", total)
}

// AttributeStore returns the Attribute Store name tag.
func AttributeStore(name string) Tag {
	return newStringTag("attribute-store", name)
}

// AttributeName returns the Attribute name tag.
func AttributeName(name string) Tag {
	return newStringTag("attribute-name", name)
}

// Timestamp returns tag for Timestamp
func Timestamp(timestamp time.Time) Tag {
	return newTimeTag("timestamp", timestamp)
}

///////////////////  Workflow tags defined here: ( wf is short for workflow) ///////////////////

// WorkflowAction returns tag for WorkflowAction
func workflowAction(action string) Tag {
	return newPredefinedStringTag("wf-action", action)
}

// general

// Service returns tag for Service
func Service(sv string) Tag {
	return newStringTag("service", sv)
}

// WorkflowError returns tag for WorkflowError
func WorkflowError(error error) Tag {
	return newErrorTag("wf-error", error)
}

// WorkflowTimeoutType returns tag for WorkflowTimeoutType
func WorkflowTimeoutType(timeoutType int64) Tag {
	return newInt64("wf-timeout-type", timeoutType)
}

// WorkflowID returns tag for WorkflowID
func WorkflowID(workflowID string) Tag {
	return newStringTag("wf-id", workflowID)
}

// StatusCode returns tag for StatusCode
func StatusCode(code int) Tag {
	return newInt("statusCode", code)
}

// SubStatus returns tag for SubStatus
func SubStatus(status string) Tag {
	return newStringTag("subStatus", status)
}

// WorkflowType returns tag for WorkflowType
func WorkflowType(wfType string) Tag {
	return newStringTag("wf-type", wfType)
}

// WorkflowState returns tag for WorkflowState
func WorkflowState(s int) Tag {
	return newInt("wf-state", s)
}

// WorkflowRunID returns tag for WorkflowRunID
func WorkflowRunID(runID string) Tag {
	return newStringTag("wf-run-id", runID)
}

// WorkflowResetBaseRunID returns tag for WorkflowResetBaseRunID
func WorkflowResetBaseRunID(runID string) Tag {
	return newStringTag("wf-reset-base-run-id", runID)
}

// WorkflowResetNewRunID returns tag for WorkflowResetNewRunID
func WorkflowResetNewRunID(runID string) Tag {
	return newStringTag("wf-reset-new-run-id", runID)
}

// WorkflowBinaryChecksum returns tag for WorkflowBinaryChecksum
func WorkflowBinaryChecksum(cs string) Tag {
	return newStringTag("wf-binary-checksum", cs)
}

// WorkflowActivityID returns tag for WorkflowActivityID
func WorkflowActivityID(id string) Tag {
	return newStringTag("wf-activity-id", id)
}

// OperationName returns tag for OperationName
func OperationName(operationName string) Tag {
	return newStringTag("operation-name", operationName)
}

// history event ID related

// WorkflowEventID returns tag for WorkflowEventID
func WorkflowEventID(eventID int64) Tag {
	return newInt64("wf-history-event-id", eventID)
}

// Address return tag for Address
func Address(ad string) Tag {
	return newStringTag("address", ad)
}

// Env return tag for runtime environment
func Env(env string) Tag {
	return newStringTag("env", env)
}

// Name returns tag for Name
func Name(k string) Tag {
	return newStringTag("name", k)
}

// ValueType returns tag for ValueType
func ValueType(v interface{}) Tag {
	return newStringTag("value-type", fmt.Sprintf("%T", v))
}

// DefaultValue returns tag for DefaultValue
func DefaultValue(v interface{}) Tag {
	return newObjectTag("default-value", v)
}

// Port returns tag for Port
func Port(p int) Tag {
	return newInt("port", p)
}

// Counter returns tag for Counter
func Counter(c int) Tag {
	return newInt("counter", c)
}

// Number returns tag for Number
func Number(n int64) Tag {
	return newInt64("number", n)
}

// NextNumber returns tag for NextNumber
func NextNumber(n int64) Tag {
	return newInt64("next-number", n)
}

// Bool returns tag for Bool
func Bool(b bool) Tag {
	return newBoolTag("bool", b)
}

// SysStackTrace returns tag for SysStackTrace
func SysStackTrace(stackTrace string) Tag {
	return newStringTag("sys-stack-trace", stackTrace)
}
