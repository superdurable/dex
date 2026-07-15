package pbconv

import (
	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
	"github.com/superdurable/dex/server/common/utils/ids"
	p "github.com/superdurable/dex/server/internal/persistence"
)

// PbConditionResultsToPersistence converts a slice of proto ConditionResult
// to persistence form. Used when the engine persists ActiveStepExecution
// condition_results on a WAITING_FOR_CONDITION → INVOKING_EXECUTE transition
// (worker StepUnblocked path; engine durable-timer / external-publish path).
func PbConditionResultsToPersistence(crs []*pb.ConditionResult) []p.ConditionResult {
	if len(crs) == 0 {
		return nil
	}
	out := make([]p.ConditionResult, 0, len(crs))
	for _, cr := range crs {
		var pc p.ConditionResult
		if t := cr.GetTimer(); t != nil {
			pc.Timer = &p.TimerConditionResult{
				Fired:        t.Fired,
				FireAtUnixMs: t.FireAtUnixMs,
			}
		} else if ch := cr.GetChannel(); ch != nil {
			pc.Channel = &p.ChannelConditionResult{
				ChannelName:   ch.ChannelName,
				Satisfied:     ch.Satisfied,
				ConsumedCount: ch.ConsumedCount,
			}
		}
		out = append(out, pc)
	}
	return out
}

// PbWaitForConditionToPersistence converts a proto WaitForCondition to persistence type.
func PbWaitForConditionToPersistence(pbCond *pb.WaitForCondition) p.WaitForCondition {
	if pbCond == nil {
		return p.WaitForCondition{}
	}
	result := p.WaitForCondition{
		Type: p.WaitType(pbCond.Type),
	}
	for _, sc := range pbCond.Conditions {
		var pc p.SingleCondition
		if sc.GetTimer() != nil {
			pc.Timer = &p.TimerCondition{FireAtUnixMs: sc.GetTimer().FireAtUnixMs}
		}
		if sc.GetChannel() != nil {
			pc.Channel = &p.ChannelCondition{
				ChannelName: sc.GetChannel().ChannelName,
				Min:         sc.GetChannel().Min,
				Max:         sc.GetChannel().Max,
			}
		}
		result.Conditions = append(result.Conditions, pc)
	}
	return result
}

func PbStateMapToPersistence(stateMap map[string]*pb.Value) map[string]p.Value {
	out := make(map[string]p.Value, len(stateMap))
	for key, value := range stateMap {
		out[key] = PbValueToPersistence(value)
	}
	return out
}

func PbChannelsToPersistence(channels map[string]*pb.ChannelMessages) map[string][]p.ChannelMessage {
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
				Value: PbValueToPersistence(message.Value),
			}
		}
		out[ch] = msgs
	}
	return out
}

func PbActiveStepsToPersistence(activeSteps map[string]*pb.ActiveStepExecution) map[string]p.ActiveStepExecution {
	if len(activeSteps) == 0 {
		return map[string]p.ActiveStepExecution{}
	}
	out := make(map[string]p.ActiveStepExecution, len(activeSteps))
	for stepExeID, step := range activeSteps {
		if step == nil {
			continue
		}
		persistStep := p.ActiveStepExecution{
			Input:              PbValueToPersistence(step.Input),
			Status:             p.StepExecutionStatus(step.Status),
			FromStepExeID:      step.FromStepExeId,
			WaitForMethodExeID: step.WaitForMethodExeId,
			ExecuteMethodExeID: step.ExecuteMethodExeId,
		}
		if step.WaitForCondition != nil {
			waitFor := PbWaitForConditionToPersistence(step.WaitForCondition)
			persistStep.WaitForCondition = &waitFor
		}
		if len(step.ConditionResults) > 0 {
			persistStep.ConditionResults = PbConditionResultsToPersistence(step.ConditionResults)
		}
		out[stepExeID] = persistStep
	}
	return out
}

func PbValueToPersistence(pbVal *pb.Value) p.Value {
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
		panic("unknown value type")
	}
}
