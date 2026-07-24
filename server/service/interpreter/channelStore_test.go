// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package interpreter

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/superdurable/iwf/gen/iwfpb"
	"github.com/superdurable/iwf/service/interpreter/channel"
)

func TestChannelStoreCommitMatchConsumesFIFOWithoutCloning(t *testing.T) {
	first := stringValue("first")
	second := stringValue("second")
	third := stringValue("third")
	store := NewChannelStore()
	store.ProcessPublishing([]*iwfpb.ChannelMessage{
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

	require.Equal(t, []*iwfpb.Value{first, second}, consumed[1])
	require.Same(t, first, consumed[1][0])
	require.Equal(t, []*iwfpb.Value{third}, store.GetAllReceived()["events"].GetValues())
}

func TestChannelStoreCommitMatchRejectsStalePlan(t *testing.T) {
	store := NewChannelStore()
	store.ProcessPublishing([]*iwfpb.ChannelMessage{{
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

func stringValue(value string) *iwfpb.Value {
	return &iwfpb.Value{Kind: &iwfpb.Value_StringValue{StringValue: value}}
}
