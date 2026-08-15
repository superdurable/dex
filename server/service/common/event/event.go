// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package event

import "github.com/superdurable/dex/gen/dexpb"

type EventType int

const (
	EventTypeUnspecified EventType = iota
	EventTypeFlowStart
	EventTypeFlowComplete
	EventTypeFlowFail
	EventTypeFlowCancel
	EventTypeWaitForAttemptFail
	EventTypeWaitForAttemptSucc
	EventTypeExecuteAttemptFail
	EventTypeExecuteAttemptSucc
	EventTypeRPCExecution
)

// Event is a lightweight server-side observability hook payload (not an IDL type).
type Event struct {
	FlowId             string
	RunId              string
	FlowType           string
	StepType           string
	StepExecutionId    string
	RpcName            string
	EventType          EventType
	StartTimestampInMs int64
	Attributes         []*dexpb.KV
}

// HandleEventFunc must be lightweight, reliable, and fast (<1s).
type HandleEventFunc func(event Event)

var Handle HandleEventFunc = DefaultHandleEventFunc

func SetHandleEventFunc(handler HandleEventFunc) {
	Handle = handler
}

func DefaultHandleEventFunc(event Event) {
	// Noop by default
}
