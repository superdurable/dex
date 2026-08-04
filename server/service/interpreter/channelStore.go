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
	"fmt"

	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/service/interpreter/channel"
)

// ChannelStore holds FIFO messages by channel.
type ChannelStore struct {
	channelMessages map[string][]*dexpb.Value
}

func NewChannelStore() *ChannelStore {
	return &ChannelStore{channelMessages: map[string][]*dexpb.Value{}}
}

// RebuildChannelStore restores a snapshot.
func RebuildChannelStore(refill map[string]*dexpb.ChannelValues) *ChannelStore {
	chMsgs := make(map[string][]*dexpb.Value, len(refill))
	for name, channelValues := range refill {
		if len(channelValues.GetValues()) > 0 {
			chMsgs[name] = channelValues.GetValues()
		}
	}
	return &ChannelStore{channelMessages: chMsgs}
}

// ProcessPublishing appends messages.
func (i *ChannelStore) ProcessPublishing(messages []*dexpb.ChannelMessage) {
	for _, message := range messages {
		i.receive(message.GetChannelName(), message.GetValue())
	}
}

// Availability returns an isolated count snapshot.
func (i *ChannelStore) Availability() channel.ChannelAvailability {
	availability := make(channel.ChannelAvailability, len(i.channelMessages))
	for name, values := range i.channelMessages {
		availability[name] = int32(len(values))
	}
	return availability
}

// HasData reports whether a channel has messages.
func (i *ChannelStore) HasData(channelName string) bool {
	return len(i.channelMessages[channelName]) > 0
}

// GetInfos returns channel sizes.
func (i *ChannelStore) GetInfos() map[string]*dexpb.ChannelInfo {
	infos := make(map[string]*dexpb.ChannelInfo, len(i.channelMessages))
	for name, values := range i.channelMessages {
		infos[name] = &dexpb.ChannelInfo{Size: int32(len(values))}
	}
	return infos
}

// GetAllReceived returns the current messages.
func (i *ChannelStore) GetAllReceived() map[string]*dexpb.ChannelValues {
	snapshot := make(map[string]*dexpb.ChannelValues, len(i.channelMessages))
	for name, values := range i.channelMessages {
		snapshot[name] = &dexpb.ChannelValues{Values: values}
	}
	return snapshot
}

// CommitMatch consumes a plan and returns the consumed messages.
func (i *ChannelStore) CommitMatch(plan *channel.MatchPlan) map[int][]*dexpb.Value {
	consumed := make(map[int][]*dexpb.Value, len(plan.Consumes))
	for _, consumption := range plan.Consumes {
		values := i.channelMessages[consumption.ChannelName]
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
			delete(i.channelMessages, consumption.ChannelName)
		} else {
			i.channelMessages[consumption.ChannelName] = values
		}
	}
	return consumed
}

func (i *ChannelStore) receive(channelName string, data *dexpb.Value) {
	values := i.channelMessages[channelName]
	values = append(values, data)
	i.channelMessages[channelName] = values
}
