package mutation

import (
	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
	"github.com/superdurable/dex/server/common/utils/ids"
	"github.com/superdurable/dex/server/internal/engine/pbconv"
	p "github.com/superdurable/dex/server/internal/persistence"
)

// buildRunStateSnapshot captures post-mutation mutable run state for history.
func (mutation *runMutation) buildRunStateSnapshot() *pb.RunStateSnapshot {
	stateMap := mutation.mergedStateMap()
	channels := mutation.mergedUnconsumedChannels()
	counters := mutation.mergedStepExeIDCounters()
	activeSteps := mutation.mergedActiveSteps()

	snap := &pb.RunStateSnapshot{
		StateMap:                      persistenceStateMapToHistoryPb(stateMap),
		UnconsumedChannelMessages:     persistenceChannelsToHistoryPb(channels),
		StepExeIdCounters:             copyInt32Map(counters),
		ActiveStepExecutions:          persistenceActiveStepsToHistoryPb(activeSteps),
		ExternalChannelMessageCounter: mutation.mergedExternalChannelMessageCounter(),
	}
	return snap
}

func (mutation *runMutation) mergedStateMap() map[string]p.Value {
	stateMap := make(map[string]p.Value, len(mutation.run.StateMap))
	for key, value := range mutation.run.StateMap {
		stateMap[key] = value
	}
	if mutation.update.StateMap != nil {
		for key, value := range mutation.update.StateMap {
			stateMap[key] = value
		}
	}
	if mutation.update.ReplaceStateMap != nil {
		stateMap = copyValueMap(*mutation.update.ReplaceStateMap)
	}
	return stateMap
}

func (mutation *runMutation) mergedStepExeIDCounters() map[string]int32 {
	counters := make(map[string]int32, len(mutation.run.StepExeIDCounters))
	for key, value := range mutation.run.StepExeIDCounters {
		counters[key] = value
	}
	if mutation.update.StepExeIDCounters != nil {
		for key, value := range mutation.update.StepExeIDCounters {
			counters[key] = value
		}
	}
	if mutation.update.ReplaceStepExeIDCounters != nil {
		counters = copyInt32Map(*mutation.update.ReplaceStepExeIDCounters)
	}
	return counters
}

func (mutation *runMutation) mergedExternalChannelMessageCounter() int64 {
	if mutation.update.ExternalChannelMessageCounter != nil {
		return *mutation.update.ExternalChannelMessageCounter
	}
	return mutation.run.ExternalChannelMessageCounter
}

func persistenceStateMapToHistoryPb(stateMap map[string]p.Value) map[string]*pb.Value {
	if len(stateMap) == 0 {
		return nil
	}
	out := make(map[string]*pb.Value, len(stateMap))
	for key, value := range stateMap {
		out[key] = persistenceValueToHistoryPb(value)
	}
	return out
}

func persistenceChannelsToHistoryPb(channels map[string][]p.ChannelMessage) map[string]*pb.ChannelMessages {
	if len(channels) == 0 {
		return nil
	}
	out := make(map[string]*pb.ChannelMessages, len(channels))
	for ch, msgs := range channels {
		pbMsgs := make([]*pb.ChannelMessage, len(msgs))
		for index, message := range msgs {
			pbMsgs[index] = &pb.ChannelMessage{
				Id:    message.ID,
				Value: persistenceValueToHistoryPb(message.Value),
			}
		}
		out[ch] = &pb.ChannelMessages{Messages: pbMsgs}
	}
	return out
}

func persistenceActiveStepsToHistoryPb(activeSteps map[string]p.ActiveStepExecution) map[string]*pb.ActiveStepExecution {
	if len(activeSteps) == 0 {
		return nil
	}
	out := make(map[string]*pb.ActiveStepExecution, len(activeSteps))
	for stepExeID, step := range activeSteps {
		pbStep := &pb.ActiveStepExecution{
			Input:              persistenceValueToHistoryPb(step.Input),
			Status:             pb.StepExecutionStatus(step.Status),
			FromStepExeId:      step.FromStepExeID,
			WaitForMethodExeId: step.WaitForMethodExeID,
			ExecuteMethodExeId: step.ExecuteMethodExeID,
		}
		if step.WaitForCondition != nil {
			pbStep.WaitForCondition = pbconv.PersistenceWaitForConditionToPb(step.WaitForCondition)
		}
		if len(step.ConditionResults) > 0 {
			pbStep.ConditionResults = pbconv.PersistenceConditionResultsToPb(step.ConditionResults)
		}
		out[stepExeID] = pbStep
	}
	return out
}

// persistenceValueToHistoryPb stores blob refs as server-internal blob_id strings.
func persistenceValueToHistoryPb(val p.Value) *pb.Value {
	switch val.Type {
	case p.ValueTypeInt:
		if val.IntVal != nil {
			return &pb.Value{Kind: &pb.Value_IntValue{IntValue: *val.IntVal}}
		}
	case p.ValueTypeDouble:
		if val.DoubleVal != nil {
			return &pb.Value{Kind: &pb.Value_DoubleValue{DoubleValue: *val.DoubleVal}}
		}
	case p.ValueTypeBool:
		if val.BoolVal != nil {
			return &pb.Value{Kind: &pb.Value_BoolValue{BoolValue: *val.BoolVal}}
		}
	case p.ValueTypeBlobRef:
		if !val.BlobID.IsZero() {
			return &pb.Value{Kind: &pb.Value_EncodedObjectBlobIdInternalOnly{
				EncodedObjectBlobIdInternalOnly: val.BlobID.String(),
			}}
		}
	}
	return &pb.Value{Kind: &pb.Value_NullValue{NullValue: pb.NullValue_NULL_VALUE}}
}

func copyInt32Map(source map[string]int32) map[string]int32 {
	if len(source) == 0 {
		return nil
	}
	out := make(map[string]int32, len(source))
	for key, value := range source {
		out[key] = value
	}
	return out
}

func copyValueMap(source map[string]p.Value) map[string]p.Value {
	if len(source) == 0 {
		return nil
	}
	out := make(map[string]p.Value, len(source))
	for key, value := range source {
		out[key] = value
	}
	return out
}

// SnapshotToPersistence converts a trusted history snapshot back to RunRow fields.
func SnapshotToPersistence(snapshot *pb.RunStateSnapshot) (
	stateMap map[string]p.Value,
	channels map[string][]p.ChannelMessage,
	counters map[string]int32,
	activeSteps map[string]p.ActiveStepExecution,
	externalCounter int64,
) {
	if snapshot == nil {
		return nil, nil, nil, nil, 0
	}
	stateMap = historyPbStateMapToPersistence(snapshot.StateMap)
	channels = historyPbChannelsToPersistence(snapshot.UnconsumedChannelMessages)
	counters = copyInt32Map(snapshot.StepExeIdCounters)
	activeSteps = historyPbActiveStepsToPersistence(snapshot.ActiveStepExecutions)
	return stateMap, channels, counters, activeSteps, snapshot.ExternalChannelMessageCounter
}

func historyPbStateMapToPersistence(stateMap map[string]*pb.Value) map[string]p.Value {
	if len(stateMap) == 0 {
		return map[string]p.Value{}
	}
	out := make(map[string]p.Value, len(stateMap))
	for key, value := range stateMap {
		out[key] = historyPbValueToPersistence(value)
	}
	return out
}

func historyPbChannelsToPersistence(channels map[string]*pb.ChannelMessages) map[string][]p.ChannelMessage {
	if len(channels) == 0 {
		return map[string][]p.ChannelMessage{}
	}
	out := make(map[string][]p.ChannelMessage, len(channels))
	for ch, channelMsgs := range channels {
		if channelMsgs == nil {
			out[ch] = nil
			continue
		}
		msgs := make([]p.ChannelMessage, len(channelMsgs.Messages))
		for index, message := range channelMsgs.Messages {
			msgs[index] = p.ChannelMessage{
				ID:    message.Id,
				Value: historyPbValueToPersistence(message.Value),
			}
		}
		out[ch] = msgs
	}
	return out
}

func historyPbActiveStepsToPersistence(activeSteps map[string]*pb.ActiveStepExecution) map[string]p.ActiveStepExecution {
	if len(activeSteps) == 0 {
		return map[string]p.ActiveStepExecution{}
	}
	out := make(map[string]p.ActiveStepExecution, len(activeSteps))
	for stepExeID, step := range activeSteps {
		if step == nil {
			continue
		}
		persistStep := p.ActiveStepExecution{
			Input:              historyPbValueToPersistence(step.Input),
			Status:             p.StepExecutionStatus(step.Status),
			FromStepExeID:      step.FromStepExeId,
			WaitForMethodExeID: step.WaitForMethodExeId,
			ExecuteMethodExeID: step.ExecuteMethodExeId,
		}
		if step.WaitForCondition != nil {
			waitFor := pbconv.PbWaitForConditionToPersistence(step.WaitForCondition)
			persistStep.WaitForCondition = &waitFor
		}
		if len(step.ConditionResults) > 0 {
			persistStep.ConditionResults = pbconv.PbConditionResultsToPersistence(step.ConditionResults)
		}
		out[stepExeID] = persistStep
	}
	return out
}

// historyPbValueToPersistence restores trusted server-minted blob refs from history.
func historyPbValueToPersistence(pbVal *pb.Value) p.Value {
	if pbVal == nil {
		return p.Value{Type: p.ValueTypeNull}
	}
	switch value := pbVal.Kind.(type) {
	case *pb.Value_IntValue:
		val := value.IntValue
		return p.Value{Type: p.ValueTypeInt, IntVal: &val}
	case *pb.Value_DoubleValue:
		val := value.DoubleValue
		return p.Value{Type: p.ValueTypeDouble, DoubleVal: &val}
	case *pb.Value_BoolValue:
		val := value.BoolValue
		return p.Value{Type: p.ValueTypeBool, BoolVal: &val}
	case *pb.Value_NullValue:
		return p.Value{Type: p.ValueTypeNull}
	case *pb.Value_EncodedObjectBlobIdInternalOnly:
		blobID := ids.MustParseBlobID(value.EncodedObjectBlobIdInternalOnly)
		return p.Value{Type: p.ValueTypeBlobRef, BlobID: blobID}
	default:
		return p.Value{Type: p.ValueTypeNull}
	}
}
