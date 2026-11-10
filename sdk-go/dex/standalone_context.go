// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package dex

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"sync"
	"time"

	"github.com/superdurable/dex/sdk-go/gen/dexpb"
)

// StandaloneContextOptions supplies the invocation metadata and hooks of a standalone Context.
//
// Each metadata field is returned unchanged by the Context method of the same name. The zero value
// of a metadata field is returned as is; NewStandaloneContext fills in no defaults. Attempt is the
// only required field.
//
// The hooks observe the calls that a Worker would stage durably. Dex persists nothing for a
// standalone Context, so a caller that needs heartbeat checkpoints, step-execution locals, or events
// to survive the attempt stores them from these hooks. A hook receives the Go value that the handler
// passed, after the SDK has validated and encoded it. Hooks run on the calling goroutine, one at a
// time, in call order. A hook must not call RecordHeartbeat, SetStepExecutionLocal, or RecordEvent
// on the same Context; such a call deadlocks.
//
// After the parent context is done, these methods return an error that wraps the parent's error,
// and they do not call the hook. This check narrows, but cannot close, the race between ending an
// attempt and a write from that attempt that is already in progress. A hook that persists values
// must therefore write conditionally on Attempt: it must never let a value from an older attempt
// replace a value from a newer attempt of the same logical call.
//
// See NewStandaloneContext for a complete example.
//
// Since 1.41.0.
type StandaloneContextOptions struct {
	// FlowID is returned by Context.FlowID. Empty is allowed and returned as empty.
	// Choose a fixed value when code derives idempotency keys from it.
	FlowID string
	// RunID is returned by Context.RunID. Empty is allowed and returned as empty.
	RunID string
	// FlowStartedAt is returned by Context.FlowStartedAt. The zero time is returned unchanged.
	FlowStartedAt time.Time
	// StepExecutionID is returned by Context.StepExecutionID. Empty is allowed and returned as empty.
	// Keep it stable across the attempts of one logical call when code derives idempotency keys
	// from FlowID and StepExecutionID.
	StepExecutionID string
	// FromStepExecutionID is returned by Context.FromStepExecutionID. Empty means no predecessor.
	FromStepExecutionID string
	// RecoveryError is returned by Context.RecoveryError. Nil means no recovery failure.
	// The Context returns this pointer itself, so do not modify the pointed-to value afterwards.
	RecoveryError *RecoveryErrorInfo
	// FirstAttemptAt is returned by Context.FirstAttemptAt. The zero time is returned unchanged.
	// Persist it with the attempt number so every attempt of one logical call reports the same time.
	FirstAttemptAt time.Time
	// Attempt is the one-based attempt number returned by Context.Attempt. It is required:
	// NewStandaloneContext rejects a value below 1. Increase it by exactly one for each retry of
	// the same logical call, because code such as a send fence compares it with the checkpoint.
	Attempt int32

	// LastHeartbeatValue is the checkpoint that Context.GetLastHeartbeatValue decodes. Nil,
	// including a typed nil, means no checkpoint, so GetLastHeartbeatValue reports false.
	// NewStandaloneContext encodes it the way RecordHeartbeat does and returns a ValueMappingError
	// when encoding fails.
	//
	// Supply the value that OnRecordHeartbeat received during the previous attempt, or a value of
	// the same Go type. After a JSON round trip, decode the JSON into the original Go type of the
	// recorded value, such as the checkpoint struct, int32, or []byte, and supply that value.
	// Do not decode it into any: JSON numbers become float64 and byte slices become base64
	// strings, so decoding into the original type later fails. Never supply the encoded JSON as
	// json.RawMessage or []byte: a byte slice is encoded as raw bytes, not as JSON.
	LastHeartbeatValue any
	// StepExecutionLocals are the step-execution locals that Context.GetStepExecutionLocal decodes.
	// Nil or empty means no locals. Keys must not be empty. NewStandaloneContext encodes each value
	// and returns a ValueMappingError when encoding fails. The JSON round-trip advice of
	// LastHeartbeatValue applies to each value.
	StepExecutionLocals map[string]any

	// OnRecordHeartbeat is called by Context.RecordHeartbeat after the value encodes successfully
	// and while the parent context is not done. A nil or typed nil heartbeat, which clears the
	// checkpoint, is passed as nil. RecordHeartbeat returns only after the hook returns, so the hook
	// can write the checkpoint durably first. A hook error is wrapped and returned by
	// RecordHeartbeat. Nil validates and encodes heartbeats but keeps nothing.
	//
	// A hook that persists the checkpoint must write it only while Attempt is still the current
	// attempt of the logical call, for example with a compare-and-set on the stored attempt
	// number. Otherwise a late write from an ended attempt can replace a newer checkpoint.
	OnRecordHeartbeat func(value any) error
	// OnSetStepExecutionLocal is called by Context.SetStepExecutionLocal after the key is validated,
	// the value encodes successfully, and while the parent context is not done. A hook error is
	// wrapped and returned by SetStepExecutionLocal. Nil validates and encodes locals but keeps
	// nothing. A persisting hook must write conditionally on Attempt, like OnRecordHeartbeat.
	OnSetStepExecutionLocal func(key string, value any) error
	// OnRecordEvent is called by Context.RecordEvent after the name is validated as non-empty and
	// unique within this Context, the value encodes successfully, and while the parent context is
	// not done. A hook error is wrapped and returned by RecordEvent, and the name stays unused. Nil
	// validates and encodes events but keeps nothing.
	OnRecordEvent func(name string, value any) error
}

// NewStandaloneContext creates a Context for running Step code outside a Worker, for example in a
// tool server or a test.
//
// The returned Context represents one handler attempt. It carries the cancellation, deadline, and
// values of parent. Its metadata methods return the values from options. Use it to call a Step
// method, or a function that takes a Context, without a Dex Server.
//
// Heartbeat checkpoints follow Worker semantics. GetLastHeartbeatValue decodes
// options.LastHeartbeatValue, the checkpoint as of the attempt start. RecordHeartbeat does not change
// what GetLastHeartbeatValue returns in the same Context; it calls options.OnRecordHeartbeat. To run
// the next attempt, create a new Context with Attempt increased by one and LastHeartbeatValue set to
// the last value the hook received.
//
// Step-execution locals follow the same pattern. GetStepExecutionLocal reads only
// options.StepExecutionLocals. SetStepExecutionLocal calls options.OnSetStepExecutionLocal and does
// not change what GetStepExecutionLocal returns. Unlike a Worker, a standalone Context allows both
// methods in any handler method. RecordEvent rejects an empty or repeated name and calls
// options.OnRecordEvent. Without the hooks, heartbeats, locals, and events are validated and then
// discarded. Dex stores nothing durably for a standalone Context. After parent is done,
// RecordHeartbeat, SetStepExecutionLocal, and RecordEvent return an error that wraps parent's error
// and do not call the hooks. See StandaloneContextOptions for why persisting hooks must still write
// conditionally on Attempt.
//
// Methods that need a Worker invocation return fixed results. HasTimerFired, HasTimerFiredByIndex,
// and WaitForMethodFailed return false. Attribute, Channel, Stream, BufferedTextStream, SubFlowResult,
// and SubFlowID operations need a Worker invocation. Those that return an error return an invalid
// invocation context error. The size and instance key methods, such as Channel.Size and
// AttributeMap.AllInstanceKeys, panic, as they do for any Context not created by a Worker.
//
// The returned Context is safe for concurrent use. NewStandaloneContext panics when parent is nil. It
// returns an error when options.Attempt is below 1, when a step-execution local key is empty, or
// when LastHeartbeatValue or a local value cannot be encoded; encoding failures are
// ValueMappingError values.
//
//	package invoicetool
//
//	import (
//		"context"
//		"encoding/json"
//		"time"
//
//		"github.com/superdurable/dex/sdk-go/dex"
//	)
//
//	// SendCheckpoint is the heartbeat checkpoint of SendInvoiceStep.
//	type SendCheckpoint struct {
//		State   string
//		Attempt int32
//	}
//
//	// SendInvoiceStep sends one invoice and skips the send when an earlier attempt finished it.
//	type SendInvoiceStep struct {
//		dex.StepDefaultsNoWaitFor[string]
//	}
//
//	func (SendInvoiceStep) Execute(ctx dex.Context, invoiceID string) (*dex.StepDecision, error) {
//		var previous SendCheckpoint
//		found, err := ctx.GetLastHeartbeatValue(&previous)
//		if err != nil {
//			return nil, err
//		}
//		if found && previous.State == "sent" {
//			return dex.DeadEnd(), nil
//		}
//		if err := ctx.RecordHeartbeat(SendCheckpoint{State: "claimed", Attempt: ctx.Attempt()}); err != nil {
//			return nil, err
//		}
//		// Send invoiceID to the billing provider here.
//		if err := ctx.RecordHeartbeat(SendCheckpoint{State: "sent", Attempt: ctx.Attempt()}); err != nil {
//			return nil, err
//		}
//		return dex.DeadEnd(), nil
//	}
//
//	// CallRecord is the state a tool server keeps for one logical call between attempts.
//	type CallRecord struct {
//		RequestKey     string
//		Attempt        int32
//		FirstAttemptAt time.Time
//		CheckpointJSON []byte
//	}
//
//	// CallStore persists call records.
//	type CallStore interface {
//		// SaveAttempt durably stores record before the attempt runs.
//		SaveAttempt(record CallRecord) error
//		// SaveCheckpoint durably stores checkpointJSON only if attempt is still the stored attempt.
//		SaveCheckpoint(requestKey string, attempt int32, checkpointJSON []byte) error
//	}
//
//	// RunSendInvoiceAttempt runs the next attempt of SendInvoiceStep outside a Worker.
//	func RunSendInvoiceAttempt(
//		parent context.Context,
//		store CallStore,
//		record CallRecord,
//		invoiceID string,
//	) (*dex.StepDecision, error) {
//		record.Attempt++
//		if record.Attempt == 1 {
//			record.FirstAttemptAt = time.Now()
//		}
//		if err := store.SaveAttempt(record); err != nil {
//			return nil, err
//		}
//		var lastHeartbeatValue any
//		if record.CheckpointJSON != nil {
//			var checkpoint SendCheckpoint
//			if err := json.Unmarshal(record.CheckpointJSON, &checkpoint); err != nil {
//				return nil, err
//			}
//			lastHeartbeatValue = checkpoint
//		}
//		attemptContext, cancel := context.WithTimeout(parent, time.Minute)
//		defer cancel()
//		ctx, err := dex.NewStandaloneContext(attemptContext, dex.StandaloneContextOptions{
//			FlowID:             "invoice-tool-server",
//			StepExecutionID:    record.RequestKey,
//			FirstAttemptAt:     record.FirstAttemptAt,
//			Attempt:            record.Attempt,
//			LastHeartbeatValue: lastHeartbeatValue,
//			OnRecordHeartbeat: func(value any) error {
//				var checkpointJSON []byte
//				if value != nil {
//					encoded, err := json.Marshal(value)
//					if err != nil {
//						return err
//					}
//					checkpointJSON = encoded
//				}
//				return store.SaveCheckpoint(record.RequestKey, record.Attempt, checkpointJSON)
//			},
//		})
//		if err != nil {
//			return nil, err
//		}
//		return SendInvoiceStep{}.Execute(ctx, invoiceID)
//	}
//
// Since 1.41.0.
func NewStandaloneContext(
	parent context.Context,
	options StandaloneContextOptions,
) (Context, error) {
	if parent == nil {
		panic("dex: NewStandaloneContext requires a non-nil parent context")
	}
	if options.Attempt < 1 {
		return nil, fmt.Errorf("dex: standalone context attempt must be at least 1, got %d", options.Attempt)
	}
	lastHeartbeatValue, err := encodeStandaloneHeartbeatValue(options.LastHeartbeatValue)
	if err != nil {
		return nil, err
	}
	locals, err := encodeStandaloneStepExecutionLocals(options.StepExecutionLocals)
	if err != nil {
		return nil, err
	}
	return &standaloneContextImpl{
		Context:                 parent,
		flowID:                  options.FlowID,
		runID:                   options.RunID,
		flowStartedAt:           options.FlowStartedAt,
		stepExecutionID:         options.StepExecutionID,
		fromStepExecutionID:     options.FromStepExecutionID,
		recoveryError:           options.RecoveryError,
		firstAttemptAt:          options.FirstAttemptAt,
		attempt:                 options.Attempt,
		lastHeartbeatValue:      lastHeartbeatValue,
		locals:                  locals,
		onRecordHeartbeat:       options.OnRecordHeartbeat,
		onSetStepExecutionLocal: options.OnSetStepExecutionLocal,
		onRecordEvent:           options.OnRecordEvent,
		recordedEventNames:      make(map[string]struct{}),
	}, nil
}

// standaloneContextImpl is the Context returned by NewStandaloneContext.
type standaloneContextImpl struct {
	context.Context

	flowID              string
	runID               string
	flowStartedAt       time.Time
	stepExecutionID     string
	fromStepExecutionID string
	recoveryError       *RecoveryErrorInfo
	firstAttemptAt      time.Time
	attempt             int32
	lastHeartbeatValue  *dexpb.Value
	locals              map[string]*dexpb.Value

	onRecordHeartbeat       func(value any) error
	onSetStepExecutionLocal func(key string, value any) error
	onRecordEvent           func(name string, value any) error

	// stagingMu serializes hooks and guards recordedEventNames.
	stagingMu          sync.Mutex
	recordedEventNames map[string]struct{}
}

var _ Context = (*standaloneContextImpl)(nil)

func encodeStandaloneHeartbeatValue(value any) (*dexpb.Value, error) {
	if value == nil || isNilValue(reflect.ValueOf(value)) {
		return nil, nil
	}
	return encodeValue(value)
}

func encodeStandaloneStepExecutionLocals(locals map[string]any) (map[string]*dexpb.Value, error) {
	keys := make([]string, 0, len(locals))
	for key := range locals {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	encodedLocals := make(map[string]*dexpb.Value, len(locals))
	for _, key := range keys {
		if key == "" {
			return nil, fmt.Errorf("dex: step-execution local key must not be empty")
		}
		encoded, err := encodeValue(locals[key])
		if err != nil {
			return nil, fmt.Errorf("dex: step-execution local %q: %w", key, err)
		}
		encodedLocals[key] = encoded
	}
	return encodedLocals, nil
}

func (standalone *standaloneContextImpl) FlowID() string {
	return standalone.flowID
}

func (standalone *standaloneContextImpl) RunID() string {
	return standalone.runID
}

func (standalone *standaloneContextImpl) FlowStartedAt() time.Time {
	return standalone.flowStartedAt
}

func (standalone *standaloneContextImpl) StepExecutionID() string {
	return standalone.stepExecutionID
}

func (standalone *standaloneContextImpl) FromStepExecutionID() string {
	return standalone.fromStepExecutionID
}

func (standalone *standaloneContextImpl) RecoveryError() *RecoveryErrorInfo {
	return standalone.recoveryError
}

func (standalone *standaloneContextImpl) FirstAttemptAt() time.Time {
	return standalone.firstAttemptAt
}

func (standalone *standaloneContextImpl) Attempt() int32 {
	return standalone.attempt
}

func (*standaloneContextImpl) HasTimerFired() bool {
	return false
}

func (*standaloneContextImpl) HasTimerFiredByIndex(int) bool {
	return false
}

func (*standaloneContextImpl) WaitForMethodFailed() bool {
	return false
}

func (standalone *standaloneContextImpl) RecordHeartbeat(value any) error {
	if value != nil && isNilValue(reflect.ValueOf(value)) {
		value = nil
	}
	if value != nil {
		if _, err := encodeValue(value); err != nil {
			return err
		}
	}
	standalone.stagingMu.Lock()
	defer standalone.stagingMu.Unlock()
	if err := standalone.requireParentNotDone(); err != nil {
		return err
	}
	if standalone.onRecordHeartbeat == nil {
		return nil
	}
	if err := standalone.onRecordHeartbeat(value); err != nil {
		return fmt.Errorf("dex: standalone context heartbeat hook: %w", err)
	}
	return nil
}

func (standalone *standaloneContextImpl) GetLastHeartbeatValue(valuePtr any) (bool, error) {
	if _, err := decodeTarget(valuePtr); err != nil {
		return false, err
	}
	if standalone.lastHeartbeatValue == nil {
		return false, nil
	}
	if err := decodeValue(standalone.lastHeartbeatValue, valuePtr); err != nil {
		return false, err
	}
	return true, nil
}

func (standalone *standaloneContextImpl) SetStepExecutionLocal(key string, value any) error {
	if key == "" {
		return fmt.Errorf("dex: step-execution local key must not be empty")
	}
	if _, err := encodeValue(value); err != nil {
		return err
	}
	standalone.stagingMu.Lock()
	defer standalone.stagingMu.Unlock()
	if err := standalone.requireParentNotDone(); err != nil {
		return err
	}
	if standalone.onSetStepExecutionLocal == nil {
		return nil
	}
	if err := standalone.onSetStepExecutionLocal(key, value); err != nil {
		return fmt.Errorf("dex: standalone context step-execution local hook for %q: %w", key, err)
	}
	return nil
}

func (standalone *standaloneContextImpl) GetStepExecutionLocal(key string, valuePtr any) (bool, error) {
	if key == "" {
		return false, fmt.Errorf("dex: step-execution local key must not be empty")
	}
	if _, err := decodeTarget(valuePtr); err != nil {
		return false, err
	}
	value, found := standalone.locals[key]
	if !found {
		return false, nil
	}
	if err := decodeValue(value, valuePtr); err != nil {
		return false, err
	}
	return true, nil
}

func (standalone *standaloneContextImpl) RecordEvent(name string, value any) error {
	if name == "" {
		return fmt.Errorf("dex: event name must not be empty")
	}
	if standalone.hasRecordedEvent(name) {
		return fmt.Errorf("dex: event %q was already recorded", name)
	}
	if _, err := encodeValue(value); err != nil {
		return err
	}
	standalone.stagingMu.Lock()
	defer standalone.stagingMu.Unlock()
	if err := standalone.requireParentNotDone(); err != nil {
		return err
	}
	// Recheck: another call may have recorded the name while this one encoded.
	if _, found := standalone.recordedEventNames[name]; found {
		return fmt.Errorf("dex: event %q was already recorded", name)
	}
	if standalone.onRecordEvent != nil {
		if err := standalone.onRecordEvent(name, value); err != nil {
			return fmt.Errorf("dex: standalone context event hook for %q: %w", name, err)
		}
	}
	standalone.recordedEventNames[name] = struct{}{}
	return nil
}

func (standalone *standaloneContextImpl) hasRecordedEvent(name string) bool {
	standalone.stagingMu.Lock()
	defer standalone.stagingMu.Unlock()
	_, found := standalone.recordedEventNames[name]
	return found
}

// requireParentNotDone rejects writes from an attempt whose parent context has ended.
// The caller must hold stagingMu.
func (standalone *standaloneContextImpl) requireParentNotDone() error {
	if err := standalone.Err(); err != nil {
		return fmt.Errorf("dex: standalone context attempt has ended: %w", err)
	}
	return nil
}
