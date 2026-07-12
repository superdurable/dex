package dex

import (
	"testing"
)

func TestNewDynamicChannel_StoresPrefix(t *testing.T) {
	dc := NewDynamicChannel[map[string]any]("order-update-")
	if dc.Prefix != "order-update-" {
		t.Fatalf("Prefix = %q, want %q", dc.Prefix, "order-update-")
	}
}

func TestDynamicChannelName(t *testing.T) {
	dc := NewDynamicChannel[map[string]any]("order-update-")
	cases := []struct {
		key  string
		want string
	}{
		{"ord-1", "order-update-ord-1"},
		{"abc/def", "order-update-abc/def"},
		{"", "order-update-"},
	}
	for _, tc := range cases {
		got := dynamicChannelName(dc.Prefix, tc.key)
		if got != tc.want {
			t.Errorf("dynamicChannelName(%q) = %q, want %q", tc.key, got, tc.want)
		}
	}
}

func TestDynamicChannel_ConditionWithMinMax(t *testing.T) {
	dc := NewDynamicChannel[int]("metric-")
	condition := dc.ConditionWithMinMax("p99", 2, 5)
	channelCondition, ok := condition.(channelCondition)
	if !ok {
		t.Fatalf("ConditionWithMinMax returned %T, want channelCondition", condition)
	}
	if channelCondition.ChannelName != "metric-p99" {
		t.Errorf("ChannelName = %q, want %q", channelCondition.ChannelName, "metric-p99")
	}
	if channelCondition.Min != 2 || channelCondition.Max != 5 {
		t.Errorf("Min/Max = %d/%d, want 2/5", channelCondition.Min, channelCondition.Max)
	}
}

func TestNewDynamicChannelMessage(t *testing.T) {
	type Event struct {
		ID string
	}
	dc := NewDynamicChannel[Event]("events-")
	msg := NewDynamicChannelMessage(dc, "user-42", Event{ID: "e1"}, Event{ID: "e2"})
	if msg.ChannelName != "events-user-42" {
		t.Errorf("ChannelName = %q, want %q", msg.ChannelName, "events-user-42")
	}
	if len(msg.Values) != 2 {
		t.Fatalf("Values len = %d, want 2", len(msg.Values))
	}
	got0, ok := msg.Values[0].(Event)
	if !ok {
		t.Fatalf("Values[0] type = %T, want Event", msg.Values[0])
	}
	if got0.ID != "e1" {
		t.Errorf("Values[0].ID = %q, want e1", got0.ID)
	}
}

func TestDynamicChannelNamesAreIsolatedAcrossKeys(t *testing.T) {
	dc := NewDynamicChannel[string]("orders/")
	a := dynamicChannelName(dc.Prefix, "k1")
	b := dynamicChannelName(dc.Prefix, "k2")
	if a == b {
		t.Fatalf("k1 and k2 channel names collided: both = %q", a)
	}
	if a != "orders/k1" || b != "orders/k2" {
		t.Errorf("got %q, %q; want orders/k1, orders/k2", a, b)
	}
}

func TestDefineDynamicChannel_TagsPrefix(t *testing.T) {
	dc := NewDynamicChannel[int]("p-")
	def := DefineDynamicChannel(dc)
	if def.Name != "p-" {
		t.Errorf("Name = %q, want %q", def.Name, "p-")
	}
	if !def.IsDynamic {
		t.Errorf("isDynamic = false, want true")
	}
}

func TestPublishHelpers_TypeSignatureCompiles(t *testing.T) {
	type Event struct {
		ID string
	}
	staticCh := NewChannel[Event]("notify")
	dynCh := NewDynamicChannel[Event]("orders-")

	if false {
		var c *Client
		_ = c.PublishToChannel(nil, "run-1", staticCh.Name, Event{ID: "e1"}, Event{ID: "e2"})
		_ = c.PublishToDynamicChannel(nil, "run-1", dynCh.Prefix, "k1", Event{ID: "e1"})
	}
}
