package dex

import (
	"context"
	"errors"
	"testing"

	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
)

func TestPersistence_PublishToChannelFlush(t *testing.T) {
	ch := NewChannel[string]("events")
	ctx := NewTestContext(context.Background(), PersistenceSchema{
		Channels: []ChannelDef{DefineChannel(ch)},
	}, nil, false, nil)
	stepCtx, err := asStepContext(ctx)
	if err != nil {
		t.Fatalf("asStepContext: %v", err)
	}
	if err := ch.Publish(ctx, "a"); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	flushed := stepCtx.persistence.flushPublishes()
	if len(flushed) != 1 || flushed[0].ChannelName != "events" {
		t.Fatalf("unexpected flush: %+v", flushed)
	}
}

func TestChannel_UndeclaredChannelReturnsError(t *testing.T) {
	registered := NewChannel[string]("events")
	ctx := NewTestContext(context.Background(), PersistenceSchema{
		Channels: []ChannelDef{DefineChannel(registered)},
	}, nil, false, nil)
	other := NewChannel[string]("other")

	if err := other.Publish(ctx, "x"); err == nil {
		t.Fatal("expected error for undeclared channel on publish")
	} else if !errors.Is(err, ErrUndeclaredChannel) {
		t.Fatalf("expected ErrUndeclaredChannel, got %v", err)
	}
	if _, err := other.GetConsumedMessages(ctx); err == nil {
		t.Fatal("expected error for undeclared channel on get")
	} else if !errors.Is(err, ErrUndeclaredChannel) {
		t.Fatalf("expected ErrUndeclaredChannel, got %v", err)
	}
}

func TestValidateWaitCondition_UndeclaredChannel(t *testing.T) {
	registered := NewChannel[int]("notify")
	persistence := newPersistence(nil, PersistenceSchema{
		Channels: []ChannelDef{DefineChannel(registered)},
	}, DefaultObjectCodec())
	other := NewChannel[int]("other")
	if err := persistence.validateWaitCondition(AnyOf(other.Condition())); err == nil {
		t.Fatal("expected error for undeclared channel in WaitFor")
	} else if !errors.Is(err, ErrUndeclaredChannel) {
		t.Fatalf("expected ErrUndeclaredChannel, got %v", err)
	}
}

func TestDynamicChannel_UndeclaredChannelReturnsError(t *testing.T) {
	registered := NewDynamicChannel[string]("orders-")
	ctx := NewTestContext(context.Background(), PersistenceSchema{
		DynamicChannels: []ChannelDef{DefineDynamicChannel(registered)},
	}, nil, false, nil)
	other := NewDynamicChannel[string]("other-")

	if err := other.Publish(ctx, "k1", "v"); err == nil {
		t.Fatal("expected error for undeclared dynamic channel on publish")
	} else if !errors.Is(err, ErrUndeclaredChannel) {
		t.Fatalf("expected ErrUndeclaredChannel, got %v", err)
	}
	if _, err := other.GetConsumedMessages(ctx, "k1"); err == nil {
		t.Fatal("expected error for undeclared dynamic channel on get")
	} else if !errors.Is(err, ErrUndeclaredChannel) {
		t.Fatalf("expected ErrUndeclaredChannel, got %v", err)
	}
}

func TestConditionResults_GetConsumedMessages(t *testing.T) {
	ch := NewChannel[string]("events")
	hello, err := DefaultObjectCodec().EncodeValue("hello")
	if err != nil {
		t.Fatalf("EncodeValue: %v", err)
	}
	ctx := NewTestContext(context.Background(), PersistenceSchema{
		Channels: []ChannelDef{DefineChannel(ch)},
	}, nil, false, map[string][]*pb.Value{
		"events": {hello},
	})
	msgs, err := ch.GetConsumedMessages(ctx)
	if err != nil {
		t.Fatalf("GetConsumedMessages: %v", err)
	}
	if len(msgs) != 1 || msgs[0] != "hello" {
		t.Fatalf("msgs = %+v, want [hello]", msgs)
	}
}
