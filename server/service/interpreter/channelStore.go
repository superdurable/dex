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
	"sort"
	"strings"

	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/service/interpreter/condition"
)

// ChannelStore holds FIFO messages by channel.
type ChannelStore struct {
	channelMessages           map[string][]*dexpb.ChannelMessage
	inFlightConsumptionCounts condition.ChannelAvailability
}

func NewChannelStore() *ChannelStore {
	return &ChannelStore{
		channelMessages:           map[string][]*dexpb.ChannelMessage{},
		inFlightConsumptionCounts: condition.ChannelAvailability{},
	}
}

// RebuildChannelStore restores a snapshot.
func RebuildChannelStore(refill map[string]*dexpb.ChannelValues) *ChannelStore {
	chMsgs := make(map[string][]*dexpb.ChannelMessage, len(refill))
	for name, channelValues := range refill {
		if len(channelValues.GetMessages()) > 0 {
			chMsgs[name] = channelValues.GetMessages()
		}
	}
	return &ChannelStore{
		channelMessages:           chMsgs,
		inFlightConsumptionCounts: condition.ChannelAvailability{},
	}
}

// ProcessPublishing appends messages.
func (i *ChannelStore) ProcessPublishing(messages []*dexpb.ChannelMessage) {
	for _, message := range messages {
		if message.GetChannelName() == "" || message.GetMessageId() == "" {
			panic("published Channel message requires a channel name and message ID")
		}
		i.receive(message)
	}
}

// GetMessages returns pending messages for one channel.
func (i *ChannelStore) GetMessages(channelName string) []*dexpb.ChannelMessage {
	return i.channelMessages[channelName]
}

// CanDeleteAll reports the first missing deletion without changing state.
func (i *ChannelStore) CanDeleteAll(deletions []*dexpb.ChannelMessageDeletion) *dexpb.ChannelMessageDeletion {
	available := make(map[channelMessageIdentity]int)
	for channelName, messages := range i.channelMessages {
		for _, message := range messages {
			available[channelMessageIdentity{channelName: channelName, messageID: message.GetMessageId()}]++
		}
	}
	for _, deletion := range deletions {
		identity := channelMessageIdentity{
			channelName: deletion.GetChannelName(),
			messageID:   deletion.GetMessageId(),
		}
		if available[identity] == 0 {
			return deletion
		}
		available[identity]--
	}
	return nil
}

// DeleteAll removes the first FIFO match for each deletion.
func (i *ChannelStore) DeleteAll(deletions []*dexpb.ChannelMessageDeletion) {
	for _, deletion := range deletions {
		i.deleteFirst(deletion.GetChannelName(), deletion.GetMessageId())
	}
}

// Availability returns an isolated count snapshot.
func (i *ChannelStore) Availability() condition.ChannelAvailability {
	availability := make(condition.ChannelAvailability, len(i.channelMessages))
	for name, values := range i.channelMessages {
		availability[name] = int32(len(values))
	}
	return availability
}

// HasPendingData reports queued messages or committed messages still executing.
func (i *ChannelStore) HasPendingData(channelName string) bool {
	return len(i.channelMessages[channelName]) > 0 ||
		i.inFlightConsumptionCounts[channelName] > 0
}

// GetInfos returns channel sizes.
func (i *ChannelStore) GetInfos() map[string]*dexpb.ChannelInfo {
	infos := make(map[string]*dexpb.ChannelInfo, len(i.channelMessages))
	for name, values := range i.channelMessages {
		infos[name] = &dexpb.ChannelInfo{Size: int32(len(values))}
	}
	return infos
}

// GetLoadedMessages returns pending envelopes for selected Channel definitions.
func (i *ChannelStore) GetLoadedMessages(
	channelNames []string,
	channelMapNames []string,
) map[string]*dexpb.ChannelValues {
	loaded := make(map[string]*dexpb.ChannelValues, len(channelNames))
	for _, name := range channelNames {
		loaded[name] = &dexpb.ChannelValues{Messages: i.channelMessages[name]}
	}
	physicalNames := make([]string, 0, len(i.channelMessages))
	for name := range i.channelMessages {
		physicalNames = append(physicalNames, name)
	}
	sort.Strings(physicalNames)
	for _, mapName := range channelMapNames {
		prefix := mapName + "/"
		for _, physicalName := range physicalNames {
			if strings.HasPrefix(physicalName, prefix) {
				loaded[physicalName] = &dexpb.ChannelValues{
					Messages: i.channelMessages[physicalName],
				}
			}
		}
	}
	return loaded
}

// GetAllReceived returns the current messages.
func (i *ChannelStore) GetAllReceived() map[string]*dexpb.ChannelValues {
	snapshot := make(map[string]*dexpb.ChannelValues, len(i.channelMessages))
	for name, messages := range i.channelMessages {
		snapshot[name] = &dexpb.ChannelValues{Messages: messages}
	}
	return snapshot
}

// CommitMatch consumes a plan and returns the consumed messages.
func (i *ChannelStore) CommitMatch(plan *condition.MatchPlan) map[int][]*dexpb.Value {
	consumed := make(map[int][]*dexpb.Value, len(plan.Consumes))
	for _, consumption := range plan.Consumes {
		messages := i.channelMessages[consumption.ChannelName]
		if int32(len(messages)) < consumption.Count {
			panic(fmt.Sprintf(
				"channel %q holds %d messages but the match plan consumes %d; no yield may occur between plan and commit",
				consumption.ChannelName,
				len(messages),
				consumption.Count,
			))
		}
		if consumption.Count > 0 {
			values := make([]*dexpb.Value, consumption.Count)
			for index, message := range messages[:consumption.Count] {
				values[index] = message.GetValue()
			}
			consumed[consumption.ChannelConditionIndex] = values
			messages = messages[consumption.Count:]
			i.inFlightConsumptionCounts[consumption.ChannelName] += consumption.Count
		} else {
			consumed[consumption.ChannelConditionIndex] = nil
		}
		if len(messages) == 0 {
			delete(i.channelMessages, consumption.ChannelName)
		} else {
			i.channelMessages[consumption.ChannelName] = messages
		}
	}
	return consumed
}

// CompleteMatch releases messages after their consuming Execute returns.
func (i *ChannelStore) CompleteMatch(plan *condition.MatchPlan) {
	for _, consumption := range plan.Consumes {
		if consumption.Count == 0 {
			continue
		}
		inFlightCount := i.inFlightConsumptionCounts[consumption.ChannelName]
		if inFlightCount < consumption.Count {
			panic(fmt.Sprintf(
				"channel %q has %d in-flight messages but completion releases %d",
				consumption.ChannelName,
				inFlightCount,
				consumption.Count,
			))
		}
		remainingCount := inFlightCount - consumption.Count
		if remainingCount == 0 {
			delete(i.inFlightConsumptionCounts, consumption.ChannelName)
		} else {
			i.inFlightConsumptionCounts[consumption.ChannelName] = remainingCount
		}
	}
}

type channelMessageIdentity struct {
	channelName string
	messageID   string
}

func (i *ChannelStore) receive(message *dexpb.ChannelMessage) {
	channelName := message.GetChannelName()
	messages := i.channelMessages[channelName]
	messages = append(messages, message)
	i.channelMessages[channelName] = messages
}

func (i *ChannelStore) deleteFirst(channelName string, messageID string) bool {
	messages := i.channelMessages[channelName]
	for index, message := range messages {
		if message.GetMessageId() != messageID {
			continue
		}
		messages = append(messages[:index], messages[index+1:]...)
		if len(messages) == 0 {
			delete(i.channelMessages, channelName)
		} else {
			i.channelMessages[channelName] = messages
		}
		return true
	}
	return false
}
