package dex

import (
	"context"
	"fmt"

	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
)

// StateKey is a typed, named workflow state key.
type StateKey[T any] struct {
	Name string
}

// NewStateKey creates a typed state key with a fixed wire name.
func NewStateKey[T any](name string) StateKey[T] {
	return StateKey[T]{Name: name}
}

// GetValue reads the current value from the step context.
func (key StateKey[T]) GetValue(ctx Context) (T, error) {
	stepCtx, err := asStepContext(ctx)
	if err != nil {
		var zero T
		return zero, err
	}
	return getStateKeyTyped[T](stepCtx.persistence, key.Name)
}

// SetValue buffers a write flushed when the step method completes.
func (key StateKey[T]) SetValue(ctx Context, value T) error {
	stepCtx, err := asStepContext(ctx)
	if err != nil {
		return err
	}
	return stepCtx.persistence.setStateKeyWire(key.Name, value)
}

// GetRunValue reads a key from a terminal or in-flight run via the client.
func (key StateKey[T]) GetRunValue(client *Client, ctx context.Context, runID string) (T, error) {
	var zero T
	result, err := client.raw.GetRun(ctx, runID)
	if err != nil {
		return zero, err
	}
	return decodeStateKeyTyped[T](client.registry.ObjectCodec(), result.State[key.Name])
}

// DynamicStateKey is a family of typed state keys sharing a prefix.
type DynamicStateKey[T any] struct {
	Prefix string
}

// NewDynamicStateKey creates a dynamic state key family.
func NewDynamicStateKey[T any](prefix string) DynamicStateKey[T] {
	return DynamicStateKey[T]{Prefix: prefix}
}

// GetValue reads the value for prefix+instanceKey from the step context.
func (key DynamicStateKey[T]) GetValue(ctx Context, instanceKey string) (T, error) {
	stepCtx, err := asStepContext(ctx)
	if err != nil {
		var zero T
		return zero, err
	}
	return getStateKeyTyped[T](stepCtx.persistence, dynamicStateKeyName(key.Prefix, instanceKey))
}

// SetValue buffers a write for prefix+instanceKey.
func (key DynamicStateKey[T]) SetValue(ctx Context, instanceKey string, value T) error {
	stepCtx, err := asStepContext(ctx)
	if err != nil {
		return err
	}
	return stepCtx.persistence.setStateKeyWire(dynamicStateKeyName(key.Prefix, instanceKey), value)
}

// GetRunValue reads a dynamic key instance from a run via the client.
func (key DynamicStateKey[T]) GetRunValue(client *Client, ctx context.Context, runID, instanceKey string) (T, error) {
	var zero T
	result, err := client.raw.GetRun(ctx, runID)
	if err != nil {
		return zero, err
	}
	return decodeStateKeyTyped[T](client.registry.ObjectCodec(), result.State[dynamicStateKeyName(key.Prefix, instanceKey)])
}

func dynamicStateKeyName(prefix, instanceKey string) string {
	return prefix + instanceKey
}

// StateKeyDef registers a state key with the flow schema.
type StateKeyDef struct {
	Name      string
	IsDynamic bool
}

// DefineStateKey registers a static state key with the flow schema.
func DefineStateKey[T any](key StateKey[T]) StateKeyDef {
	return StateKeyDef{Name: key.Name, IsDynamic: false}
}

// DefineDynamicStateKey registers a dynamic state key family.
func DefineDynamicStateKey[T any](key DynamicStateKey[T]) StateKeyDef {
	return StateKeyDef{Name: key.Prefix, IsDynamic: true}
}

func decodeStateKeyTyped[T any](codec ObjectCodec, val *pb.Value) (T, error) {
	var zero T
	if val == nil || val.Kind == nil {
		return zero, nil
	}
	if _, ok := val.Kind.(*pb.Value_NullValue); ok {
		return zero, nil
	}
	target := new(T)
	if err := codec.DecodeValue(val, target); err != nil {
		return zero, fmt.Errorf("dex: decode state key: %w", err)
	}
	return *target, nil
}
