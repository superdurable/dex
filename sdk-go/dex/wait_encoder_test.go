package dex

import (
	"testing"
	"time"

	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
)

func TestWaitForToProto_AnyOfTimerAndChannel(t *testing.T) {
	notify := NewChannel[int]("notify")
	condition := AnyOf(notify.ConditionWithMinMax(2, 5), Timer(30*time.Second))
	serverNow := int64(1_700_000_000_000)

	out, err := waitForToProto(condition, serverNow)
	if err != nil {
		t.Fatalf("waitForToProto error: %v", err)
	}
	if out.Type != pb.WaitType_WAIT_TYPE_ANY_OF {
		t.Fatalf("expected ANY_OF, got %v", out.Type)
	}
	if len(out.Conditions) != 2 {
		t.Fatalf("expected 2 conditions, got %d", len(out.Conditions))
	}
	channel := out.Conditions[0].GetChannel()
	if channel == nil {
		t.Fatalf("expected first condition to be channel")
	}
	if channel.ChannelName != "notify" || channel.Min != 2 || channel.Max != 5 {
		t.Fatalf("channel mapped wrong: %+v", channel)
	}
	timer := out.Conditions[1].GetTimer()
	if timer == nil {
		t.Fatalf("expected second condition to be timer")
	}
	want := serverNow + (30 * time.Second).Milliseconds()
	if timer.FireAtUnixMs != want {
		t.Fatalf("timer fire_at_unix_ms=%d, want %d", timer.FireAtUnixMs, want)
	}
}

func TestWaitForToProto_AllOf(t *testing.T) {
	notify := NewChannel[int]("notify")
	condition := AllOf(Timer(15*time.Second), notify.Condition())
	out, err := waitForToProto(condition, 1_000)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if out.Type != pb.WaitType_WAIT_TYPE_ALL_OF {
		t.Fatalf("expected ALL_OF, got %v", out.Type)
	}
	if got := out.Conditions[1].GetChannel().Min; got != 1 {
		t.Fatalf("default min=1 expected, got %d", got)
	}
}

func TestWaitForToProto_NilOrEmpty_Errors(t *testing.T) {
	if _, err := waitForToProto(nil, 0); err == nil {
		t.Fatalf("expected error on nil cond")
	}
	empty := AnyOf()
	if _, err := waitForToProto(empty, 0); err == nil {
		t.Fatalf("expected error on empty cond")
	}
}

func TestChannelMessagesToProto_RoundTrip(t *testing.T) {
	notify := NewChannel[map[string]any]("events")
	msgs := []ChannelMessage{
		NewChannelMessage(notify, map[string]any{"a": 1}, map[string]any{"b": 2}),
	}
	out, err := channelMessagesToProto(DefaultObjectCodec(), msgs)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 ChannelPublish, got %d", len(out))
	}
	if out[0].ChannelName != "events" {
		t.Fatalf("channel name mismatch: %q", out[0].ChannelName)
	}
	if len(out[0].Values) != 2 {
		t.Fatalf("expected 2 values, got %d", len(out[0].Values))
	}
	if _, ok := out[0].Values[0].Kind.(*pb.Value_EncodedObject); !ok {
		t.Fatalf("expected EncodedObject value, got %T", out[0].Values[0].Kind)
	}
}
