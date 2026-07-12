package pbconv

import (
	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
	p "github.com/superdurable/dex/server/internal/persistence"
)

// persistenceWaitForConditionToPb converts a persistence WaitForCondition to proto.
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

// persistenceRetryStateToPb converts a persistence RetryState to proto StepRetryState.
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

// persistenceConditionResultsToPb is the read-path inverse, used to surface
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
