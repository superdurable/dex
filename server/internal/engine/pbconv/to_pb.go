package pbconv

import (
	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
	p "github.com/superdurable/dex/server/internal/persistence"
)

// PersistenceWaitForConditionToPb converts a persistence WaitForCondition to proto.
func PersistenceWaitForConditionToPb(wfc *p.WaitForCondition) *pb.WaitForCondition {
	if wfc == nil {
		return nil
	}
	pbWfc := &pb.WaitForCondition{
		Type:       pb.WaitType(wfc.Type),
		Conditions: make([]*pb.SingleCondition, len(wfc.Conditions)),
	}
	for i, c := range wfc.Conditions {
		sc := &pb.SingleCondition{}
		if c.Timer != nil {
			sc.Condition = &pb.SingleCondition_Timer{
				Timer: &pb.TimerCondition{FireAtUnixMs: c.Timer.FireAtUnixMs},
			}
		} else if c.Channel != nil {
			sc.Condition = &pb.SingleCondition_Channel{
				Channel: &pb.ChannelCondition{
					ChannelName: c.Channel.ChannelName,
					Min:         c.Channel.Min,
					Max:         c.Channel.Max,
				},
			}
		}
		pbWfc.Conditions[i] = sc
	}
	return pbWfc
}

// PersistenceRetryStateToPb converts a persistence RetryState to proto StepRetryState.
func PersistenceRetryStateToPb(rs *p.RetryState) *pb.StepRetryState {
	if rs == nil {
		return nil
	}
	return &pb.StepRetryState{
		FirstAttemptTimeMs:  rs.FirstAttemptTime.UnixMilli(),
		CurrentAttempts:     rs.CurrentAttempts,
		LastError:           rs.LastError,
		LastErrorStackTrace: rs.LastErrorStackTrace,
	}
}

// PersistenceConditionResultsToPb is the read-path inverse, used to surface
// the persisted condition_results back to the worker via PollResponse / GetRun.
func PersistenceConditionResultsToPb(crs []p.ConditionResult) []*pb.ConditionResult {
	if len(crs) == 0 {
		return nil
	}
	out := make([]*pb.ConditionResult, 0, len(crs))
	for _, cr := range crs {
		pcr := &pb.ConditionResult{}
		if cr.Timer != nil {
			pcr.Result = &pb.ConditionResult_Timer{
				Timer: &pb.TimerConditionResult{
					Fired:        cr.Timer.Fired,
					FireAtUnixMs: cr.Timer.FireAtUnixMs,
				},
			}
		} else if cr.Channel != nil {
			pcr.Result = &pb.ConditionResult_Channel{
				Channel: &pb.ChannelConditionResult{
					ChannelName:   cr.Channel.ChannelName,
					Satisfied:     cr.Channel.Satisfied,
					ConsumedCount: cr.Channel.ConsumedCount,
				},
			}
		}
		out = append(out, pcr)
	}
	return out
}

func PersistenceStateMapToHistoryPb(stateMap map[string]p.Value) map[string]*pb.Value {
	if len(stateMap) == 0 {
		return nil
	}
	out := make(map[string]*pb.Value, len(stateMap))
	for key, value := range stateMap {
		out[key] = PersistenceValueToHistoryPb(value)
	}
	return out
}

func PersistenceChannelsToHistoryPb(channels map[string][]p.ChannelMessage) map[string]*pb.ChannelMessages {
	if len(channels) == 0 {
		return nil
	}
	out := make(map[string]*pb.ChannelMessages, len(channels))
	for ch, msgs := range channels {
		pbMsgs := make([]*pb.ChannelMessage, len(msgs))
		for index, message := range msgs {
			pbMsgs[index] = &pb.ChannelMessage{
				Id:    message.ID,
				Value: PersistenceValueToHistoryPb(message.Value),
			}
		}
		out[ch] = &pb.ChannelMessages{Messages: pbMsgs}
	}
	return out
}

func PersistenceActiveStepsToHistoryPb(activeSteps map[string]p.ActiveStepExecution) map[string]*pb.ActiveStepExecution {
	if len(activeSteps) == 0 {
		return nil
	}
	out := make(map[string]*pb.ActiveStepExecution, len(activeSteps))
	for stepExeID, step := range activeSteps {
		pbStep := &pb.ActiveStepExecution{
			Input:              PersistenceValueToHistoryPb(step.Input),
			Status:             pb.StepExecutionStatus(step.Status),
			FromStepExeId:      step.FromStepExeID,
			WaitForMethodExeId: step.WaitForMethodExeID,
			ExecuteMethodExeId: step.ExecuteMethodExeID,
		}
		if step.WaitForCondition != nil {
			pbStep.WaitForCondition = PersistenceWaitForConditionToPb(step.WaitForCondition)
		}
		if len(step.ConditionResults) > 0 {
			pbStep.ConditionResults = PersistenceConditionResultsToPb(step.ConditionResults)
		}
		out[stepExeID] = pbStep
	}
	return out
}

// PersistenceValueToHistoryPb stores blob refs as server-internal blob_id strings.
func PersistenceValueToHistoryPb(val p.Value) *pb.Value {
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

	panic("invalid value type")
}
