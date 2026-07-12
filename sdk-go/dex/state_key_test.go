package dex

import (
	"context"
	"errors"
	"testing"

	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
)

func TestStateKey_GetSetValue(t *testing.T) {
	keyCount := NewStateKey[int]("count")
	schema := PersistenceSchema{
		StateKeys: []StateKeyDef{DefineStateKey(keyCount)},
	}
	ctx := NewTestContext(context.Background(), schema, map[string]*pb.Value{
		"count": {Kind: &pb.Value_IntValue{IntValue: 3}},
	}, false, nil)

	count, err := keyCount.GetValue(ctx)
	if err != nil {
		t.Fatalf("GetValue: %v", err)
	}
	if count != 3 {
		t.Fatalf("count = %d, want 3", count)
	}

	if err := keyCount.SetValue(ctx, 7); err != nil {
		t.Fatalf("SetValue: %v", err)
	}
	count, err = keyCount.GetValue(ctx)
	if err != nil {
		t.Fatalf("GetValue after set: %v", err)
	}
	if count != 7 {
		t.Fatalf("count after set = %d, want 7", count)
	}
}

func TestStateKey_UndeclaredKeyReturnsError(t *testing.T) {
	keyCount := NewStateKey[int]("count")
	ctx := NewTestContext(context.Background(), PersistenceSchema{
		StateKeys: []StateKeyDef{DefineStateKey(keyCount)},
	}, nil, false, nil)
	other := NewStateKey[int]("other")

	_, err := other.GetValue(ctx)
	if err == nil {
		t.Fatal("expected error for undeclared key on get")
	} else if !errors.Is(err, ErrUndeclaredStateKey) {
		t.Fatalf("expected ErrUndeclaredStateKey, got %v", err)
	}
	if err := other.SetValue(ctx, 1); err == nil {
		t.Fatal("expected error for undeclared key on set")
	} else if !errors.Is(err, ErrUndeclaredStateKey) {
		t.Fatalf("expected ErrUndeclaredStateKey, got %v", err)
	}
}

func TestDynamicStateKey_WireName(t *testing.T) {
	keyOrders := NewDynamicStateKey[string]("orders/")
	schema := PersistenceSchema{
		DynamicStateKeys: []StateKeyDef{DefineDynamicStateKey(keyOrders)},
	}
	ctx := NewTestContext(context.Background(), schema, nil, false, nil)

	if err := keyOrders.SetValue(ctx, "k1", "v1"); err != nil {
		t.Fatalf("SetValue: %v", err)
	}
	value, err := keyOrders.GetValue(ctx, "k1")
	if err != nil {
		t.Fatalf("GetValue: %v", err)
	}
	if value != "v1" {
		t.Fatalf("value = %q, want v1", value)
	}
}

func TestDefineStateKeySchema(t *testing.T) {
	keyA := NewStateKey[int]("a")
	keyB := NewDynamicStateKey[string]("dyn/")
	defA := DefineStateKey(keyA)
	defB := DefineDynamicStateKey(keyB)
	if defA.Name != "a" || defA.IsDynamic {
		t.Fatalf("DefineStateKey: %+v", defA)
	}
	if defB.Name != "dyn/" || !defB.IsDynamic {
		t.Fatalf("DefineDynamicStateKey: %+v", defB)
	}
}

func TestStateKey_MissingKeyReturnsZero(t *testing.T) {
	keyCount := NewStateKey[int]("count")
	ctx := NewTestContext(context.Background(), PersistenceSchema{
		StateKeys: []StateKeyDef{DefineStateKey(keyCount)},
	}, nil, false, nil)
	count, err := keyCount.GetValue(ctx)
	if err != nil {
		t.Fatalf("GetValue: %v", err)
	}
	if count != 0 {
		t.Fatalf("count = %d, want zero for missing key", count)
	}
}

func TestPersistence_FlushState(t *testing.T) {
	keyCount := NewStateKey[int]("count")
	ctx := NewTestContext(context.Background(), PersistenceSchema{
		StateKeys: []StateKeyDef{DefineStateKey(keyCount)},
	}, nil, false, nil)
	stepCtx, err := asStepContext(ctx)
	if err != nil {
		t.Fatalf("asStepContext: %v", err)
	}
	if err := keyCount.SetValue(ctx, 7); err != nil {
		t.Fatalf("SetValue: %v", err)
	}
	flushed := stepCtx.persistence.flushState()
	if flushed["count"].GetIntValue() != 7 {
		t.Fatalf("flushed count = %v, want 7", flushed["count"])
	}
}
