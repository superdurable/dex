// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package interpreter

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/service/interpreter/channel"
)

func TestChannelStoreCommitMatchConsumesFIFOWithoutCloning(t *testing.T) {
	first := stringValue("first")
	second := stringValue("second")
	third := stringValue("third")
	store := NewChannelStore()
	store.ProcessPublishing([]*dexpb.ChannelMessage{
		{ChannelName: "events", Value: first},
		{ChannelName: "events", Value: second},
		{ChannelName: "events", Value: third},
	})

	require.Equal(t, channel.ChannelAvailability{"events": 3}, store.Availability())

	consumed := store.CommitMatch(&channel.MatchPlan{
		Consumes: []channel.Consume{{
			ChannelConditionIndex: 1,
			ChannelName:           "events",
			Count:                 2,
		}},
	})

	require.Equal(t, []*dexpb.Value{first, second}, consumed[1])
	require.Same(t, first, consumed[1][0])
	require.Equal(t, []*dexpb.Value{third}, store.GetAllReceived()["events"].GetValues())
}

func TestChannelStoreCommitMatchRejectsStalePlan(t *testing.T) {
	store := NewChannelStore()
	store.ProcessPublishing([]*dexpb.ChannelMessage{{
		ChannelName: "events",
		Value:       stringValue("only"),
	}})

	require.Panics(t, func() {
		store.CommitMatch(&channel.MatchPlan{
			Consumes: []channel.Consume{{
				ChannelConditionIndex: 1,
				ChannelName:           "events",
				Count:                 2,
			}},
		})
	})
}

func stringValue(value string) *dexpb.Value {
	return &dexpb.Value{Kind: &dexpb.Value_StringValue{StringValue: value}}
}
