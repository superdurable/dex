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
	"github.com/superdurable/dex/service/interpreter/condition"
)

func TestChannelStoreCommitMatchConsumesFIFOWithoutCloning(t *testing.T) {
	first := stringValue("first")
	second := stringValue("second")
	third := stringValue("third")
	store := NewChannelStore()
	store.ProcessPublishing([]*dexpb.ChannelMessage{
		{ChannelName: "events", MessageId: "1", Value: first},
		{ChannelName: "events", MessageId: "2", Value: second},
		{ChannelName: "events", MessageId: "3", Value: third},
	})

	require.Equal(t, condition.ChannelAvailability{"events": 3}, store.Availability())

	consumed := store.CommitMatch(&condition.MatchPlan{
		Consumes: []condition.Consume{{
			ChannelConditionIndex: 1,
			ChannelName:           "events",
			Count:                 2,
		}},
	})

	require.Equal(t, []*dexpb.Value{first, second}, consumed[1])
	require.Same(t, first, consumed[1][0])
	require.Equal(t, []*dexpb.ChannelMessage{{ChannelName: "events", MessageId: "3", Value: third}}, store.GetAllReceived()["events"].GetMessages())
}

func TestChannelStoreCommitMatchRejectsStalePlan(t *testing.T) {
	store := NewChannelStore()
	store.ProcessPublishing([]*dexpb.ChannelMessage{{
		ChannelName: "events",
		MessageId:   "1",
		Value:       stringValue("only"),
	}})

	require.Panics(t, func() {
		store.CommitMatch(&condition.MatchPlan{
			Consumes: []condition.Consume{{
				ChannelConditionIndex: 1,
				ChannelName:           "events",
				Count:                 2,
			}},
		})
	})
}

func TestChannelStoreDeleteAllRemovesFirstFIFOIdentityMatch(t *testing.T) {
	store := NewChannelStore()
	store.ProcessPublishing([]*dexpb.ChannelMessage{
		{ChannelName: "events", MessageId: "same", Value: stringValue("first")},
		{ChannelName: "events", MessageId: "other", Value: stringValue("second")},
		{ChannelName: "events", MessageId: "same", Value: stringValue("third")},
	})

	store.DeleteAll([]*dexpb.ChannelMessageDeletion{{ChannelName: "events", MessageId: "same"}})

	remaining := store.GetMessages("events")
	require.Len(t, remaining, 2)
	require.Equal(t, "second", remaining[0].GetValue().GetStringValue())
	require.Equal(t, "third", remaining[1].GetValue().GetStringValue())
}

func TestChannelStoreCanDeleteAllValidatesBatchWithoutMutation(t *testing.T) {
	store := NewChannelStore()
	store.ProcessPublishing([]*dexpb.ChannelMessage{
		{ChannelName: "events", MessageId: "one", Value: stringValue("first")},
		{ChannelName: "events", MessageId: "two", Value: stringValue("second")},
	})
	deletions := []*dexpb.ChannelMessageDeletion{
		{ChannelName: "events", MessageId: "one"},
		{ChannelName: "events", MessageId: "missing"},
	}

	missing := store.CanDeleteAll(deletions)

	require.Same(t, deletions[1], missing)
	require.Len(t, store.GetMessages("events"), 2)
}

func stringValue(value string) *dexpb.Value {
	return &dexpb.Value{Kind: &dexpb.Value_StringValue{StringValue: value}}
}
