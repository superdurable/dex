package pbconv

import (
	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
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
