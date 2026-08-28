// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

package dex

import (
	"fmt"
	"time"
)

// Stream defines one typed, best-effort resumable message stream.
//
// Register the Stream in exactly one Flow's PersistenceSchema. All Flow instances sharing that
// Flow type and Stream name share streamCapacityBytes. Stream writes are immediately visible and do
// not roll back when a Step later fails.
//
//	var Thinking = dex.DefineStream[string]("thinking", 10<<20)
//
//	func (ThinkStep) Execute(ctx dex.Context, input Input) (*dex.StepDecision, error) {
//		if err := Thinking.Write(ctx, "checking inventory"); err != nil {
//			return nil, err
//		}
//		return dex.GracefulComplete(), nil
//	}
type Stream[T any] struct {
	definition *streamDefinition
}

type streamDefinition struct {
	name                string
	streamCapacityBytes int64
}

// DefineStream creates a typed Stream with a stable name and shared approximate byte budget.
//
// streamCapacityBytes must be positive. Validation occurs when NewRegistry registers the Stream.
func DefineStream[T any](name string, streamCapacityBytes int64) Stream[T] {
	return Stream[T]{definition: &streamDefinition{
		name:                name,
		streamCapacityBytes: streamCapacityBytes,
	}}
}

// StreamDef is the sealed schema-erasure interface implemented by Stream.
//
// Applications create Stream values with DefineStream and add them to PersistenceSchema.Streams.
type StreamDef interface {
	streamDefinition() *streamDefinition
}

// StreamMessage describes one retained Stream message returned by Client.ReadStream.
//
// ResumeToken identifies this message and can be passed unchanged to the next ReadStream call.
// CreatedTime is assigned by the Stream Store. IdempotencyKey is the client key or the Step's
// generated runID#stepExecutionID key.
type StreamMessage struct {
	// ResumeToken identifies this message for the next ReadStream call.
	ResumeToken string
	// CreatedTime is the server-assigned creation time.
	CreatedTime time.Time
	// IdempotencyKey is the client key or the Step's generated key.
	IdempotencyKey string
}

// Write appends value immediately from the current Step invocation.
//
// A Step execution may call Write once per Stream. Retries reuse runID#stepExecutionID, so the
// server applies first-write-wins idempotency. RPC invocation contexts are rejected.
func (s Stream[T]) Write(ctx Context, value T) error {
	invocation, ok := ctx.(streamInvocation)
	if !ok {
		return errInvalidInvocationContext
	}
	return invocation.writeStream(s.definition, value)
}

// StreamName returns the stable name sent to Dex.
func (s Stream[T]) StreamName() string {
	if s.definition == nil {
		return ""
	}
	return s.definition.name
}

// StreamCapacityBytes returns the approximate shared byte budget.
func (s Stream[T]) StreamCapacityBytes() int64 {
	if s.definition == nil {
		return 0
	}
	return s.definition.streamCapacityBytes
}

func (s Stream[T]) streamDefinition() *streamDefinition {
	return s.definition
}

type streamInvocation interface {
	writeStream(definition *streamDefinition, value any) error
}

func validateStreamDefinition(definition *streamDefinition) error {
	if definition == nil {
		return fmt.Errorf("stream definition is nil")
	}
	if definition.name == "" {
		return fmt.Errorf("stream name must not be empty")
	}
	if definition.streamCapacityBytes <= 0 {
		return fmt.Errorf("stream %q capacity bytes must be positive", definition.name)
	}
	return nil
}
