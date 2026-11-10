// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package connectorsmcp

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sync"
	"time"

	"github.com/superdurable/dex/sdk-go/dex"
)

// localStepContext is the dex.Context one MCP tool call runs a connector operation with.
// Its Step execution ID is the call's idempotency key, so the connector Call ID is stable per key.
type localStepContext struct {
	context.Context
	flowID          string
	stepExecutionID string
	startedAt       time.Time
	attempt         int32
	// previousHeartbeat is the last checkpoint of an earlier attempt, or nil.
	previousHeartbeat json.RawMessage
	// persistHeartbeat stores a checkpoint durably before RecordHeartbeat returns.
	persistHeartbeat func(json.RawMessage) error

	mu                    sync.Mutex
	lastHeartbeat         json.RawMessage
	stepExecutionLocals   map[string]json.RawMessage
	hasRecordedHeartbeats bool
}

func newLocalStepContext(
	ctx context.Context,
	flowID string,
	stepExecutionID string,
	attempt int32,
	previousHeartbeat json.RawMessage,
	persistHeartbeat func(json.RawMessage) error,
) *localStepContext {
	if persistHeartbeat == nil {
		panic("local Step context heartbeat persistence must not be nil")
	}
	return &localStepContext{
		Context:             ctx,
		flowID:              flowID,
		stepExecutionID:     stepExecutionID,
		startedAt:           time.Now().UTC(),
		attempt:             attempt,
		previousHeartbeat:   previousHeartbeat,
		persistHeartbeat:    persistHeartbeat,
		stepExecutionLocals: make(map[string]json.RawMessage),
	}
}

func (ctx *localStepContext) FlowID() string                        { return ctx.flowID }
func (ctx *localStepContext) RunID() string                         { return ctx.flowID }
func (ctx *localStepContext) FlowStartedAt() time.Time              { return ctx.startedAt }
func (ctx *localStepContext) StepExecutionID() string               { return ctx.stepExecutionID }
func (ctx *localStepContext) FromStepExecutionID() string           { return "" }
func (ctx *localStepContext) RecoveryError() *dex.RecoveryErrorInfo { return nil }
func (ctx *localStepContext) FirstAttemptAt() time.Time             { return ctx.startedAt }
func (ctx *localStepContext) Attempt() int32                        { return ctx.attempt }
func (ctx *localStepContext) HasTimerFired() bool                   { return false }
func (ctx *localStepContext) HasTimerFiredByIndex(int) bool         { return false }
func (ctx *localStepContext) WaitForMethodFailed() bool             { return false }
func (ctx *localStepContext) RecordEvent(string, any) error         { return nil }

// RecordHeartbeat stores value as this attempt's checkpoint; a nil value clears it.
func (ctx *localStepContext) RecordHeartbeat(value any) error {
	var encoded json.RawMessage
	if !isNilHeartbeat(value) {
		var err error
		if encoded, err = json.Marshal(value); err != nil {
			return fmt.Errorf("encode heartbeat checkpoint: %w", err)
		}
	}
	ctx.mu.Lock()
	defer ctx.mu.Unlock()
	if err := ctx.persistHeartbeat(encoded); err != nil {
		return err
	}
	ctx.lastHeartbeat = encoded
	ctx.hasRecordedHeartbeats = true
	return nil
}

// GetLastHeartbeatValue decodes the checkpoint an earlier attempt recorded, as Dex supplies it to a retry.
func (ctx *localStepContext) GetLastHeartbeatValue(valuePtr any) (bool, error) {
	if len(ctx.previousHeartbeat) == 0 {
		return false, nil
	}
	if err := json.Unmarshal(ctx.previousHeartbeat, valuePtr); err != nil {
		return false, fmt.Errorf("decode heartbeat checkpoint: %w", err)
	}
	return true, nil
}

func (ctx *localStepContext) SetStepExecutionLocal(key string, value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode Step execution local %q: %w", key, err)
	}
	ctx.mu.Lock()
	defer ctx.mu.Unlock()
	ctx.stepExecutionLocals[key] = encoded
	return nil
}

func (ctx *localStepContext) GetStepExecutionLocal(key string, valuePtr any) (bool, error) {
	ctx.mu.Lock()
	encoded, found := ctx.stepExecutionLocals[key]
	ctx.mu.Unlock()
	if !found {
		return false, nil
	}
	if err := json.Unmarshal(encoded, valuePtr); err != nil {
		return false, fmt.Errorf("decode Step execution local %q: %w", key, err)
	}
	return true, nil
}

// recordedHeartbeat returns the last checkpoint this attempt recorded and whether it recorded any.
func (ctx *localStepContext) recordedHeartbeat() (json.RawMessage, bool) {
	ctx.mu.Lock()
	defer ctx.mu.Unlock()
	return ctx.lastHeartbeat, ctx.hasRecordedHeartbeats
}

func isNilHeartbeat(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Interface:
		return reflected.IsNil()
	default:
		return false
	}
}
