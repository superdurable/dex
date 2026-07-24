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
	"fmt"

	"github.com/superdurable/iwf/gen/iwfpb"
	"github.com/superdurable/iwf/service/interpreter/channel"
)

// ChannelStore holds FIFO messages by channel.
type ChannelStore struct {
	receivedData map[string][]*iwfpb.Value
}

func NewChannelStore() *ChannelStore {
	return &ChannelStore{receivedData: map[string][]*iwfpb.Value{}}
}

// RebuildChannelStore restores a snapshot.
func RebuildChannelStore(refill map[string]*iwfpb.ChannelValues) *ChannelStore {
	data := make(map[string][]*iwfpb.Value, len(refill))
	for name, channelValues := range refill {
		if len(channelValues.GetValues()) > 0 {
			data[name] = channelValues.GetValues()
		}
	}
	return &ChannelStore{receivedData: data}
}

// ProcessPublishing appends messages.
func (i *ChannelStore) ProcessPublishing(messages []*iwfpb.ChannelMessage) {
	for _, message := range messages {
		i.receive(message.GetChannelName(), message.GetValue())
	}
}

// Availability returns an isolated count snapshot.
func (i *ChannelStore) Availability() channel.ChannelAvailability {
	availability := make(channel.ChannelAvailability, len(i.receivedData))
	for name, values := range i.receivedData {
		availability[name] = int32(len(values))
	}
	return availability
}

// HasData reports whether a channel has messages.
func (i *ChannelStore) HasData(channelName string) bool {
	return len(i.receivedData[channelName]) > 0
}

// GetInfos returns channel sizes.
func (i *ChannelStore) GetInfos() map[string]*iwfpb.ChannelInfo {
	infos := make(map[string]*iwfpb.ChannelInfo, len(i.receivedData))
	for name, values := range i.receivedData {
		infos[name] = &iwfpb.ChannelInfo{Size: int32(len(values))}
	}
	return infos
}

// GetAllReceived returns the current messages.
func (i *ChannelStore) GetAllReceived() map[string]*iwfpb.ChannelValues {
	snapshot := make(map[string]*iwfpb.ChannelValues, len(i.receivedData))
	for name, values := range i.receivedData {
		snapshot[name] = &iwfpb.ChannelValues{Values: values}
	}
	return snapshot
}

// CommitMatch consumes a plan and returns the consumed messages.
func (i *ChannelStore) CommitMatch(plan *channel.MatchPlan) map[int][]*iwfpb.Value {
	consumed := make(map[int][]*iwfpb.Value, len(plan.Consumes))
	for _, consumption := range plan.Consumes {
		values := i.receivedData[consumption.ChannelName]
		if int32(len(values)) < consumption.Count {
			panic(fmt.Sprintf(
				"channel %q holds %d messages but the match plan consumes %d; no yield may occur between plan and commit",
				consumption.ChannelName,
				len(values),
				consumption.Count,
			))
		}
		if consumption.Count > 0 {
			consumed[consumption.ChannelConditionIndex] = values[:consumption.Count:consumption.Count]
			values = values[consumption.Count:]
		} else {
			consumed[consumption.ChannelConditionIndex] = nil
		}
		if len(values) == 0 {
			delete(i.receivedData, consumption.ChannelName)
		} else {
			i.receivedData[consumption.ChannelName] = values
		}
	}
	return consumed
}

func (i *ChannelStore) receive(channelName string, data *iwfpb.Value) {
	values := i.receivedData[channelName]
	values = append(values, data)
	i.receivedData[channelName] = values
}
