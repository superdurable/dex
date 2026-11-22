// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package connectortools

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	dexagents "github.com/superdurable/dex/agents/go/core"
	"github.com/superdurable/dex/agents/go/core/internal/pythonjson"
	"github.com/superdurable/dex/sdk-go/dex"
)

// connectorStepContext is the tool Step's dex.Context with heartbeat values kept as the session's
// text checkpoint.
//
// The single-dispatch fence records markers as heartbeat values and reads the
// previous attempt's marker back. The session's tool Step keeps one text
// checkpoint per attempt and repeats it on every background heartbeat, so a
// raw heartbeat would be overwritten and the next attempt could not read it as
// text. This context saves each value through SaveHeartbeatCheckpoint and
// decodes the previous checkpoint on the next attempt, as the Python and
// TypeScript cores do. Every other method is the tool Step's own.
type connectorStepContext struct {
	dex.Context
	previousCheckpoint *string
	heartbeat          func() error
	saveCheckpoint     func(checkpoint string) error
}

func newConnectorStepContext(ctx dex.Context, call dexagents.DeferredToolExecution) *connectorStepContext {
	return &connectorStepContext{
		Context:            ctx,
		previousCheckpoint: call.HeartbeatCheckpoint,
		heartbeat:          call.Heartbeat,
		saveCheckpoint:     call.SaveHeartbeatCheckpoint,
	}
}

// RecordHeartbeat saves a value as the attempt's text checkpoint; nil repeats the saved checkpoint.
func (ctx *connectorStepContext) RecordHeartbeat(value any) error {
	if isNilHeartbeatValue(value) {
		if ctx.heartbeat == nil {
			return nil
		}
		return ctx.heartbeat()
	}
	if ctx.saveCheckpoint == nil {
		return errors.New("this tool Step cannot save heartbeat checkpoints")
	}
	checkpoint, err := encodeHeartbeatValue(value)
	if err != nil {
		return err
	}
	return ctx.saveCheckpoint(checkpoint)
}

// GetLastHeartbeatValue decodes the checkpoint an earlier attempt of the tool Step saved.
func (ctx *connectorStepContext) GetLastHeartbeatValue(valuePtr any) (bool, error) {
	if ctx.previousCheckpoint == nil {
		return false, nil
	}
	if err := json.Unmarshal([]byte(*ctx.previousCheckpoint), valuePtr); err != nil {
		return false, fmt.Errorf("decode the tool Step's heartbeat checkpoint: %w", err)
	}
	return true, nil
}

// encodeHeartbeatValue returns compact JSON with sorted keys, the text the Python core stores.
func encodeHeartbeatValue(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("heartbeat value %T does not encode as JSON: %w", value, err)
	}
	return pythonjson.Marshal(json.RawMessage(encoded),
		pythonjson.Options{IsCompact: true, ShouldSortKeys: true, ShouldEscapeNonASCII: true})
}

func isNilHeartbeatValue(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Interface, reflect.Func, reflect.Chan:
		return reflected.IsNil()
	default:
		return false
	}
}
