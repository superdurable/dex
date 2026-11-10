// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package dex_test

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/superdurable/dex/sdk-go/dex"
)

type standaloneCheckpoint struct {
	State      string `json:"state"`
	Attempt    int32  `json:"attempt"`
	ProviderID string `json:"providerId,omitempty"`
}

type standaloneContextKey struct{}

// sendOnceStep records a claim checkpoint before its send, like a send fence.
type sendOnceStep struct {
	dex.StepDefaultsNoWaitFor[string]
}

func (sendOnceStep) Execute(ctx dex.Context, invoiceID string) (*dex.StepDecision, error) {
	var previous standaloneCheckpoint
	found, err := ctx.GetLastHeartbeatValue(&previous)
	if err != nil {
		return nil, err
	}
	if found && previous.State == "sent" {
		return dex.DeadEnd(), nil
	}
	claim := standaloneCheckpoint{State: "claimed", Attempt: ctx.Attempt()}
	if err := ctx.RecordHeartbeat(claim); err != nil {
		return nil, err
	}
	sent := standaloneCheckpoint{State: "sent", Attempt: ctx.Attempt(), ProviderID: "provider-" + invoiceID}
	if err := ctx.RecordHeartbeat(sent); err != nil {
		return nil, err
	}
	return dex.DeadEnd(), nil
}

func newStandaloneContextForTest(t *testing.T, options dex.StandaloneContextOptions) dex.Context {
	t.Helper()
	ctx, err := dex.NewStandaloneContext(context.Background(), options)
	if err != nil {
		t.Fatalf("NewStandaloneContext: %v", err)
	}
	return ctx
}

func TestStandaloneContextReturnsSuppliedMetadata(t *testing.T) {
	flowStartedAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	firstAttemptAt := flowStartedAt.Add(time.Minute)
	recoveryError := &dex.RecoveryErrorInfo{
		Detail:          "provider unavailable",
		ErrorType:       "ProviderError",
		AttemptProgress: dex.StepAttemptProgressNotCompleted,
	}
	ctx := newStandaloneContextForTest(t, dex.StandaloneContextOptions{
		FlowID:              "flow-1",
		RunID:               "run-1",
		FlowStartedAt:       flowStartedAt,
		StepExecutionID:     "step-execution-1",
		FromStepExecutionID: "step-execution-0",
		RecoveryError:       recoveryError,
		FirstAttemptAt:      firstAttemptAt,
		Attempt:             7,
	})

	if got := ctx.FlowID(); got != "flow-1" {
		t.Fatalf("FlowID = %q", got)
	}
	if got := ctx.RunID(); got != "run-1" {
		t.Fatalf("RunID = %q", got)
	}
	if got := ctx.FlowStartedAt(); !got.Equal(flowStartedAt) {
		t.Fatalf("FlowStartedAt = %v", got)
	}
	if got := ctx.StepExecutionID(); got != "step-execution-1" {
		t.Fatalf("StepExecutionID = %q", got)
	}
	if got := ctx.FromStepExecutionID(); got != "step-execution-0" {
		t.Fatalf("FromStepExecutionID = %q", got)
	}
	if got := ctx.RecoveryError(); got != recoveryError {
		t.Fatalf("RecoveryError = %#v", got)
	}
	if got := ctx.FirstAttemptAt(); !got.Equal(firstAttemptAt) {
		t.Fatalf("FirstAttemptAt = %v", got)
	}
	if got := ctx.Attempt(); got != 7 {
		t.Fatalf("Attempt = %d", got)
	}
	if ctx.HasTimerFired() || ctx.HasTimerFiredByIndex(0) || ctx.WaitForMethodFailed() {
		t.Fatal("standalone timer and WaitFor results must be false")
	}
}

func TestStandaloneContextKeepsZeroMetadata(t *testing.T) {
	ctx := newStandaloneContextForTest(t, dex.StandaloneContextOptions{Attempt: 1})
	if ctx.FlowID() != "" || ctx.RunID() != "" || ctx.StepExecutionID() != "" ||
		ctx.FromStepExecutionID() != "" || ctx.RecoveryError() != nil {
		t.Fatal("unset metadata must stay empty")
	}
	if !ctx.FlowStartedAt().IsZero() || !ctx.FirstAttemptAt().IsZero() {
		t.Fatal("unset times must stay zero")
	}
}

func TestNewStandaloneContextRejectsInvalidOptions(t *testing.T) {
	for _, attempt := range []int32{0, -1} {
		if _, err := dex.NewStandaloneContext(context.Background(), dex.StandaloneContextOptions{
			Attempt: attempt,
		}); err == nil {
			t.Fatalf("attempt %d was accepted", attempt)
		}
	}
	if _, err := dex.NewStandaloneContext(context.Background(), dex.StandaloneContextOptions{
		Attempt:             1,
		StepExecutionLocals: map[string]any{"": "value"},
	}); err == nil {
		t.Fatal("empty step-execution local key was accepted")
	}

	var mappingError *dex.ValueMappingError
	_, err := dex.NewStandaloneContext(context.Background(), dex.StandaloneContextOptions{
		Attempt:            1,
		LastHeartbeatValue: math.NaN(),
	})
	if !errors.As(err, &mappingError) {
		t.Fatalf("heartbeat encoding error = %v", err)
	}
	_, err = dex.NewStandaloneContext(context.Background(), dex.StandaloneContextOptions{
		Attempt:             1,
		StepExecutionLocals: map[string]any{"cursor": make(chan int)},
	})
	if !errors.As(err, &mappingError) {
		t.Fatalf("local encoding error = %v", err)
	}
}

func TestNewStandaloneContextPanicsWithoutParent(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("nil parent did not panic")
		}
	}()
	_, err := dex.NewStandaloneContext(nil, dex.StandaloneContextOptions{Attempt: 1})
	t.Fatalf("NewStandaloneContext returned %v", err)
}

func TestStandaloneContextFollowsParentCancellation(t *testing.T) {
	deadline := time.Now().Add(time.Hour)
	parent, cancel := context.WithDeadline(
		context.WithValue(context.Background(), standaloneContextKey{}, "request-1"),
		deadline,
	)
	ctx, err := dex.NewStandaloneContext(parent, dex.StandaloneContextOptions{Attempt: 1})
	if err != nil {
		t.Fatalf("NewStandaloneContext: %v", err)
	}
	if got, ok := ctx.Deadline(); !ok || !got.Equal(deadline) {
		t.Fatalf("Deadline = %v, %v", got, ok)
	}
	if got := ctx.Value(standaloneContextKey{}); got != "request-1" {
		t.Fatalf("Value = %v", got)
	}
	if ctx.Err() != nil {
		t.Fatalf("Err before cancel = %v", ctx.Err())
	}
	cancel()
	<-ctx.Done()
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatalf("Err after cancel = %v", ctx.Err())
	}
}

func TestStandaloneContextHeartbeatKeepsAttemptStartCheckpoint(t *testing.T) {
	var recorded []any
	ctx := newStandaloneContextForTest(t, dex.StandaloneContextOptions{
		Attempt:            2,
		LastHeartbeatValue: standaloneCheckpoint{State: "notSent", Attempt: 1},
		OnRecordHeartbeat: func(value any) error {
			recorded = append(recorded, value)
			return nil
		},
	})

	if err := ctx.RecordHeartbeat(standaloneCheckpoint{State: "claimed", Attempt: 2}); err != nil {
		t.Fatalf("RecordHeartbeat: %v", err)
	}
	var checkpoint standaloneCheckpoint
	found, err := ctx.GetLastHeartbeatValue(&checkpoint)
	if err != nil || !found {
		t.Fatalf("GetLastHeartbeatValue = %v, %v", found, err)
	}
	if checkpoint != (standaloneCheckpoint{State: "notSent", Attempt: 1}) {
		t.Fatalf("checkpoint = %#v", checkpoint)
	}

	var typedNil *standaloneCheckpoint
	if err := ctx.RecordHeartbeat(nil); err != nil {
		t.Fatalf("RecordHeartbeat(nil): %v", err)
	}
	if err := ctx.RecordHeartbeat(typedNil); err != nil {
		t.Fatalf("RecordHeartbeat(typed nil): %v", err)
	}
	if len(recorded) != 3 || recorded[1] != nil || recorded[2] != nil {
		t.Fatalf("recorded = %#v", recorded)
	}
	if recorded[0] != (standaloneCheckpoint{State: "claimed", Attempt: 2}) {
		t.Fatalf("recorded[0] = %#v", recorded[0])
	}
}

func TestStandaloneContextHeartbeatWithoutCheckpoint(t *testing.T) {
	var typedNil *standaloneCheckpoint
	for _, seed := range []any{nil, typedNil} {
		ctx := newStandaloneContextForTest(t, dex.StandaloneContextOptions{
			Attempt:            1,
			LastHeartbeatValue: seed,
		})
		checkpoint := standaloneCheckpoint{State: "unchanged"}
		found, err := ctx.GetLastHeartbeatValue(&checkpoint)
		if err != nil || found || checkpoint.State != "unchanged" {
			t.Fatalf("GetLastHeartbeatValue = %v, %v, %#v", found, err, checkpoint)
		}
		if err := ctx.RecordHeartbeat(standaloneCheckpoint{State: "claimed"}); err != nil {
			t.Fatalf("RecordHeartbeat without hook: %v", err)
		}
		if _, err := ctx.GetLastHeartbeatValue(nil); err == nil {
			t.Fatal("nil decode target was accepted")
		}
		if _, err := ctx.GetLastHeartbeatValue(checkpoint); err == nil {
			t.Fatal("non-pointer decode target was accepted")
		}
	}
}

func TestStandaloneContextHeartbeatErrors(t *testing.T) {
	hookFailure := errors.New("checkpoint store unavailable")
	hookCalls := 0
	ctx := newStandaloneContextForTest(t, dex.StandaloneContextOptions{
		Attempt:            1,
		LastHeartbeatValue: "not a checkpoint object",
		OnRecordHeartbeat: func(any) error {
			hookCalls++
			return hookFailure
		},
	})

	var mappingError *dex.ValueMappingError
	if err := ctx.RecordHeartbeat(math.Inf(1)); !errors.As(err, &mappingError) {
		t.Fatalf("encoding error = %v", err)
	}
	if hookCalls != 0 {
		t.Fatal("hook ran for a value that failed to encode")
	}
	if err := ctx.RecordHeartbeat("progress"); !errors.Is(err, hookFailure) {
		t.Fatalf("hook error = %v", err)
	}
	var checkpoint standaloneCheckpoint
	if _, err := ctx.GetLastHeartbeatValue(&checkpoint); !errors.As(err, &mappingError) {
		t.Fatalf("decode error = %v", err)
	}
}

// TestStandaloneContextHeartbeatJSONRoundTrip follows a caller that persists checkpoints as JSON.
func TestStandaloneContextHeartbeatJSONRoundTrip(t *testing.T) {
	var persisted []byte
	firstAttempt := newStandaloneContextForTest(t, dex.StandaloneContextOptions{
		FlowID:          "tool-server",
		StepExecutionID: "request-1",
		Attempt:         1,
		OnRecordHeartbeat: func(value any) error {
			encoded, err := json.Marshal(value)
			if err != nil {
				return err
			}
			persisted = encoded
			return nil
		},
	})
	if _, err := (sendOnceStep{}).Execute(firstAttempt, "42"); err != nil {
		t.Fatalf("first attempt: %v", err)
	}

	var seed any
	if err := json.Unmarshal(persisted, &seed); err != nil {
		t.Fatalf("decode persisted checkpoint: %v", err)
	}
	secondAttempt := newStandaloneContextForTest(t, dex.StandaloneContextOptions{
		Attempt:            2,
		LastHeartbeatValue: seed,
	})
	var checkpoint standaloneCheckpoint
	found, err := secondAttempt.GetLastHeartbeatValue(&checkpoint)
	if err != nil || !found {
		t.Fatalf("GetLastHeartbeatValue = %v, %v", found, err)
	}
	want := standaloneCheckpoint{State: "sent", Attempt: 1, ProviderID: "provider-42"}
	if checkpoint != want {
		t.Fatalf("checkpoint = %#v, want %#v", checkpoint, want)
	}

	rawSeed := newStandaloneContextForTest(t, dex.StandaloneContextOptions{
		Attempt:            2,
		LastHeartbeatValue: json.RawMessage(persisted),
	})
	var mappingError *dex.ValueMappingError
	if _, err := rawSeed.GetLastHeartbeatValue(&checkpoint); !errors.As(err, &mappingError) {
		t.Fatalf("raw JSON seed decode error = %v", err)
	}
}

func TestStandaloneContextHeartbeatHooksRunOneAtATime(t *testing.T) {
	var running atomic.Int32
	var overlapped atomic.Bool
	var calls atomic.Int32
	ctx := newStandaloneContextForTest(t, dex.StandaloneContextOptions{
		Attempt: 1,
		OnRecordHeartbeat: func(any) error {
			if running.Add(1) != 1 {
				overlapped.Store(true)
			}
			time.Sleep(time.Millisecond)
			calls.Add(1)
			running.Add(-1)
			return nil
		},
	})
	var group sync.WaitGroup
	for index := range 8 {
		group.Add(1)
		go func() {
			defer group.Done()
			if err := ctx.RecordHeartbeat(index); err != nil {
				t.Errorf("RecordHeartbeat: %v", err)
			}
			if err := ctx.SetStepExecutionLocal("cursor", index); err != nil {
				t.Errorf("SetStepExecutionLocal: %v", err)
			}
		}()
	}
	group.Wait()
	if overlapped.Load() {
		t.Fatal("heartbeat hooks overlapped")
	}
	if calls.Load() != 8 {
		t.Fatalf("hook calls = %d", calls.Load())
	}
}

func TestStandaloneContextStepExecutionLocals(t *testing.T) {
	type stagedLocal struct {
		key   string
		value any
	}
	var staged []stagedLocal
	ctx := newStandaloneContextForTest(t, dex.StandaloneContextOptions{
		Attempt: 1,
		StepExecutionLocals: map[string]any{
			"cursor": "page-2",
			"total":  42,
			"state":  standaloneCheckpoint{State: "notSent", Attempt: 1},
		},
		OnSetStepExecutionLocal: func(key string, value any) error {
			staged = append(staged, stagedLocal{key: key, value: value})
			return nil
		},
	})

	var cursor string
	if found, err := ctx.GetStepExecutionLocal("cursor", &cursor); err != nil || !found || cursor != "page-2" {
		t.Fatalf("cursor = %q, %v, %v", cursor, found, err)
	}
	var total int64
	if found, err := ctx.GetStepExecutionLocal("total", &total); err != nil || !found || total != 42 {
		t.Fatalf("total = %d, %v, %v", total, found, err)
	}
	var state standaloneCheckpoint
	if found, err := ctx.GetStepExecutionLocal("state", &state); err != nil || !found || state.State != "notSent" {
		t.Fatalf("state = %#v, %v, %v", state, found, err)
	}
	var missing string
	if found, err := ctx.GetStepExecutionLocal("missing", &missing); err != nil || found {
		t.Fatalf("missing = %v, %v", found, err)
	}

	if err := ctx.SetStepExecutionLocal("cursor", "page-3"); err != nil {
		t.Fatalf("SetStepExecutionLocal: %v", err)
	}
	if len(staged) != 1 || staged[0].key != "cursor" || staged[0].value != "page-3" {
		t.Fatalf("staged = %#v", staged)
	}
	if _, err := ctx.GetStepExecutionLocal("cursor", &cursor); err != nil || cursor != "page-2" {
		t.Fatalf("cursor after Set = %q, %v", cursor, err)
	}

	next := newStandaloneContextForTest(t, dex.StandaloneContextOptions{
		Attempt:             2,
		StepExecutionLocals: map[string]any{staged[0].key: staged[0].value},
	})
	if found, err := next.GetStepExecutionLocal("cursor", &cursor); err != nil || !found || cursor != "page-3" {
		t.Fatalf("next cursor = %q, %v, %v", cursor, found, err)
	}
}

func TestStandaloneContextStepExecutionLocalErrors(t *testing.T) {
	hookFailure := errors.New("local store unavailable")
	ctx := newStandaloneContextForTest(t, dex.StandaloneContextOptions{
		Attempt:             1,
		StepExecutionLocals: map[string]any{"cursor": "page-2"},
		OnSetStepExecutionLocal: func(string, any) error {
			return hookFailure
		},
	})
	var mappingError *dex.ValueMappingError
	if err := ctx.SetStepExecutionLocal("", "value"); err == nil {
		t.Fatal("empty key was accepted by SetStepExecutionLocal")
	}
	if err := ctx.SetStepExecutionLocal("cursor", math.NaN()); !errors.As(err, &mappingError) {
		t.Fatalf("encoding error = %v", err)
	}
	if err := ctx.SetStepExecutionLocal("cursor", "page-3"); !errors.Is(err, hookFailure) {
		t.Fatalf("hook error = %v", err)
	}
	var cursor string
	if _, err := ctx.GetStepExecutionLocal("", &cursor); err == nil {
		t.Fatal("empty key was accepted by GetStepExecutionLocal")
	}
	if _, err := ctx.GetStepExecutionLocal("cursor", nil); err == nil {
		t.Fatal("nil decode target was accepted")
	}
	var wrongType int
	if _, err := ctx.GetStepExecutionLocal("cursor", &wrongType); !errors.As(err, &mappingError) {
		t.Fatalf("decode error = %v", err)
	}

	withoutHook := newStandaloneContextForTest(t, dex.StandaloneContextOptions{Attempt: 1})
	if err := withoutHook.SetStepExecutionLocal("cursor", "page-3"); err != nil {
		t.Fatalf("SetStepExecutionLocal without hook: %v", err)
	}
	if found, err := withoutHook.GetStepExecutionLocal("cursor", &cursor); err != nil || found {
		t.Fatalf("local without seed = %v, %v", found, err)
	}
}

func TestStandaloneContextRecordEvent(t *testing.T) {
	hookFailure := errors.New("event store unavailable")
	shouldFail := true
	recorded := map[string]any{}
	ctx := newStandaloneContextForTest(t, dex.StandaloneContextOptions{
		Attempt: 1,
		OnRecordEvent: func(name string, value any) error {
			if shouldFail {
				return hookFailure
			}
			recorded[name] = value
			return nil
		},
	})

	if err := ctx.RecordEvent("sent", "provider-42"); !errors.Is(err, hookFailure) {
		t.Fatalf("hook error = %v", err)
	}
	shouldFail = false
	if err := ctx.RecordEvent("sent", "provider-42"); err != nil {
		t.Fatalf("RecordEvent after hook failure: %v", err)
	}
	if recorded["sent"] != "provider-42" {
		t.Fatalf("recorded = %#v", recorded)
	}
	if err := ctx.RecordEvent("sent", "provider-43"); err == nil {
		t.Fatal("duplicate event name was accepted")
	}
	if err := ctx.RecordEvent("", "value"); err == nil {
		t.Fatal("empty event name was accepted")
	}
	var mappingError *dex.ValueMappingError
	if err := ctx.RecordEvent("invalid", math.NaN()); !errors.As(err, &mappingError) {
		t.Fatalf("encoding error = %v", err)
	}
	if err := ctx.RecordEvent("invalid", "valid now"); err != nil {
		t.Fatalf("event name kept after encoding failure: %v", err)
	}

	withoutHook := newStandaloneContextForTest(t, dex.StandaloneContextOptions{Attempt: 1})
	if err := withoutHook.RecordEvent("sent", "provider-42"); err != nil {
		t.Fatalf("RecordEvent without hook: %v", err)
	}
	if err := withoutHook.RecordEvent("sent", "provider-42"); err == nil {
		t.Fatal("duplicate event name was accepted without hook")
	}
}

func TestStandaloneContextRejectsWorkerOnlyOperations(t *testing.T) {
	ctx := newStandaloneContextForTest(t, dex.StandaloneContextOptions{Attempt: 1})
	if _, err := statusAttribute.Get(ctx); err == nil {
		t.Fatal("Attribute.Get succeeded without a Worker invocation")
	}
	if err := progressStream.Write(ctx, "progress"); err == nil {
		t.Fatal("Stream.Write succeeded without a Worker invocation")
	}
	if err := commandChannel.Publish(ctx, command{Name: "ship"}); err == nil {
		t.Fatal("Channel.Publish succeeded without a Worker invocation")
	}
}
