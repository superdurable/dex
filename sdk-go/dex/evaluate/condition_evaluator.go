package evaluate

import (
	"fmt"

	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
)

// EvaluationResult holds the outcome of evaluating a pb.WaitForCondition
// against the current per-channel unconsumed message buffer.
//
// SDK port of server/internal/engine/condition_evaluator.go.
type EvaluationResult struct {
	Satisfied bool
	// Timer/Channel sub-result reports
	ConditionResults []*pb.ConditionResult
}

type ConditionEvaluator struct {
	unconsumedMessages map[string][]*pb.Value
	effectiveNow       int64
	active             map[string]*pb.ActiveStepExecution
}

func NewConditionEvaluator(active map[string]*pb.ActiveStepExecution, effectiveNow int64, unconsumedMessages map[string][]*pb.Value) *ConditionEvaluator {
	return &ConditionEvaluator{
		unconsumedMessages: unconsumedMessages,
		effectiveNow:       effectiveNow,
		active:             active,
	}
}

// EvaluateWaitForCondition checks whether a pb.WaitForCondition is satisfied given
// the current unconsumed channel messages and effectiveNow timestamp.
//
// NOTE: require conditions to be wrapped in AnyOf or AllOf with at least
// one element. Returns an error otherwise.
func (ce *ConditionEvaluator) EvaluateWaitForCondition(
	stepExeId string,
) (*EvaluationResult, error) {
	stepExe := ce.active[stepExeId]
	if stepExe == nil {
		return nil, fmt.Errorf("stepExeId %s not in active step executions", stepExeId)
	}
	cond := stepExe.WaitForCondition
	if cond == nil || len(cond.Conditions) == 0 {
		return nil, fmt.Errorf("stepExeId %s has empty WaitForCondition; must have at least one condition wrapped in AnyOf or AllOf", stepExeId)
	}
	switch cond.Type {
	case pb.WaitType_WAIT_TYPE_ANY_OF:
		return ce.evaluateAnyOf(cond.Conditions), nil
	case pb.WaitType_WAIT_TYPE_ALL_OF:
		return ce.evaluateAllOf(cond.Conditions), nil
	default:
		return nil, fmt.Errorf("stepExeId %s has unknown WaitType %d", stepExeId, cond.Type)
	}
}

func (ce *ConditionEvaluator) evaluateAnyOf(conditions []*pb.SingleCondition) *EvaluationResult {
	pers := make([]*pb.ConditionResult, len(conditions))
	for index, condition := range conditions {
		pers[index] = makeUnsatisfiedResult(condition)
	}

	satisfied := false
	takenPerChannel := map[string]int{}
	for index, condition := range conditions {
		if timer := condition.GetTimer(); timer != nil {
			if timer.FireAtUnixMs <= ce.effectiveNow {
				pers[index] = &pb.ConditionResult{Result: &pb.ConditionResult_Timer{
					Timer: &pb.TimerConditionResult{Fired: true, FireAtUnixMs: timer.FireAtUnixMs},
				}}
				satisfied = true
			}
			continue
		}
		if channelCond := condition.GetChannel(); channelCond != nil {
			met, count := HasMetSingleChannelCondition(channelCond, ce.active, ce.unconsumedMessages, takenPerChannel[channelCond.GetChannelName()])
			if met {
				pers[index] = &pb.ConditionResult{Result: &pb.ConditionResult_Channel{
					Channel: &pb.ChannelConditionResult{
						ChannelName:   channelCond.ChannelName,
						Satisfied:     true,
						ConsumedCount: int32(count),
					},
				}}
				takenPerChannel[channelCond.GetChannelName()] += count
				satisfied = true
			}
		}
	}
	return &EvaluationResult{Satisfied: satisfied, ConditionResults: pers}
}

func (ce *ConditionEvaluator) evaluateAllOf(conditions []*pb.SingleCondition) *EvaluationResult {
	pers := make([]*pb.ConditionResult, len(conditions))
	for i, c := range conditions {
		pers[i] = makeUnsatisfiedResult(c)
	}

	var channelConds []*pb.ChannelCondition
	for _, condition := range conditions {
		if timer := condition.GetTimer(); timer != nil && timer.FireAtUnixMs > ce.effectiveNow {
			return &EvaluationResult{Satisfied: false, ConditionResults: pers}
		}
		if channelCond := condition.GetChannel(); channelCond != nil {
			channelConds = append(channelConds, channelCond)
		}
	}

	met, counts := HasMetAllChannelConditions(channelConds, ce.active, ce.unconsumedMessages)
	if !met {
		return &EvaluationResult{Satisfied: false, ConditionResults: pers}
	}

	channelCondIndex := 0
	for index, condition := range conditions {
		if timer := condition.GetTimer(); timer != nil {
			pers[index] = &pb.ConditionResult{Result: &pb.ConditionResult_Timer{
				Timer: &pb.TimerConditionResult{Fired: true, FireAtUnixMs: timer.FireAtUnixMs},
			}}
			continue
		}
		if channelCond := condition.GetChannel(); channelCond != nil {
			pers[index] = &pb.ConditionResult{Result: &pb.ConditionResult_Channel{
				Channel: &pb.ChannelConditionResult{
					ChannelName:   channelCond.ChannelName,
					Satisfied:     true,
					ConsumedCount: int32(counts[channelCondIndex]),
				},
			}}
			channelCondIndex++
		}
	}
	return &EvaluationResult{
		Satisfied:        true,
		ConditionResults: pers,
	}
}

// makeUnsatisfiedResult builds the default per-condition result entry shape
// for an unsatisfied condition (timer not yet fired / channel under-min).
func makeUnsatisfiedResult(c *pb.SingleCondition) *pb.ConditionResult {
	if t := c.GetTimer(); t != nil {
		return &pb.ConditionResult{Result: &pb.ConditionResult_Timer{
			Timer: &pb.TimerConditionResult{Fired: false, FireAtUnixMs: t.FireAtUnixMs},
		}}
	}
	if ch := c.GetChannel(); ch != nil {
		return &pb.ConditionResult{Result: &pb.ConditionResult_Channel{
			Channel: &pb.ChannelConditionResult{
				ChannelName:   ch.ChannelName,
				Satisfied:     false,
				ConsumedCount: 0,
			},
		}}
	}
	return &pb.ConditionResult{}
}
