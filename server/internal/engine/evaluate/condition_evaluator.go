package evaluate

import (
	"fmt"

	"github.com/superdurable/dex/server/common/errors"
	p "github.com/superdurable/dex/server/internal/persistence"
)

// EvaluationResult holds the outcome of evaluating a WaitForCondition.
type EvaluationResult struct {
	Satisfied        bool
	ConditionResults []p.ConditionResult
}

type ConditionEvaluator struct {
	active             map[string]p.ActiveStepExecution
	effectiveNow       int64
	unconsumedMessages map[string][]p.ChannelMessage
}

func NewConditionEvaluator(
	active map[string]p.ActiveStepExecution,
	effectiveNow int64,
	unconsumed map[string][]p.ChannelMessage,
) *ConditionEvaluator {
	return &ConditionEvaluator{
		active:             active,
		effectiveNow:       effectiveNow,
		unconsumedMessages: unconsumed,
	}
}

func (ce *ConditionEvaluator) EvaluateWaitForCondition(stepExeID string) (EvaluationResult, errors.CategorizedError) {
	stepExe, ok := ce.active[stepExeID]
	if !ok {
		return EvaluationResult{}, errors.NewInternalError(
			fmt.Sprintf("stepExeID %s not found in active step executions", stepExeID), nil)
	}
	cond := stepExe.WaitForCondition
	if cond == nil || len(cond.Conditions) == 0 {
		return EvaluationResult{}, errors.NewInvalidInputError(
			"invalid WaitForCondition: SDK must provide at least one condition wrapped in AnyOf or AllOf", nil)
	}
	switch cond.Type {
	case p.WaitTypeAnyOf:
		return ce.evaluateAnyOf(cond.Conditions), nil
	case p.WaitTypeAllOf:
		return ce.evaluateAllOf(cond.Conditions), nil
	default:
		return EvaluationResult{}, errors.NewInvalidInputError(
			fmt.Sprintf("unknown WaitType %d, SDK must use AnyOf or AllOf", cond.Type), nil)
	}
}

// evaluateAnyOf is greedy: it marks EVERY satisfied branch (timer fired and/or
// channel met) and reserves messages for each, not just the first. Symmetric
// with the SDK port. takenPerChannel tracks reservations made by earlier
// branches of this evaluation so same-channel branches don't double-reserve.
func (ce *ConditionEvaluator) evaluateAnyOf(conditions []p.SingleCondition) EvaluationResult {
	pers := make([]p.ConditionResult, len(conditions))
	for index, cond := range conditions {
		pers[index] = makeUnsatisfiedResult(cond)
	}
	satisfied := false
	takenPerChannel := map[string]int{}
	for index, cond := range conditions {
		if cond.Timer != nil {
			if cond.Timer.FireAtUnixMs <= ce.effectiveNow {
				pers[index] = p.ConditionResult{
					Timer: &p.TimerConditionResult{Fired: true, FireAtUnixMs: cond.Timer.FireAtUnixMs},
				}
				satisfied = true
			}
			continue
		}
		if cond.Channel != nil {
			met, count := hasMetSingleChannelCondition(cond.Channel, ce.active, ce.unconsumedMessages, takenPerChannel[cond.Channel.ChannelName])
			if met {
				pers[index] = p.ConditionResult{
					Channel: &p.ChannelConditionResult{
						ChannelName:   cond.Channel.ChannelName,
						Satisfied:     true,
						ConsumedCount: int32(count),
					},
				}
				takenPerChannel[cond.Channel.ChannelName] += count
				satisfied = true
			}
		}
	}
	return EvaluationResult{Satisfied: satisfied, ConditionResults: pers}
}

func (ce *ConditionEvaluator) evaluateAllOf(conditions []p.SingleCondition) EvaluationResult {
	pers := make([]p.ConditionResult, len(conditions))
	for index, cond := range conditions {
		pers[index] = makeUnsatisfiedResult(cond)
	}

	var channelConds []*p.ChannelCondition
	for _, cond := range conditions {
		if cond.Timer != nil && cond.Timer.FireAtUnixMs > ce.effectiveNow {
			return EvaluationResult{Satisfied: false, ConditionResults: pers}
		}
		if cond.Channel != nil {
			channelConds = append(channelConds, cond.Channel)
		}
	}

	met, counts := hasMetAllChannelConditions(channelConds, ce.active, ce.unconsumedMessages)
	if !met {
		return EvaluationResult{Satisfied: false, ConditionResults: pers}
	}

	channelCondIdx := 0
	for index, cond := range conditions {
		if cond.Timer != nil {
			pers[index] = p.ConditionResult{
				Timer: &p.TimerConditionResult{Fired: true, FireAtUnixMs: cond.Timer.FireAtUnixMs},
			}
			continue
		}
		if cond.Channel != nil {
			pers[index] = p.ConditionResult{
				Channel: &p.ChannelConditionResult{
					ChannelName:   cond.Channel.ChannelName,
					Satisfied:     true,
					ConsumedCount: int32(counts[channelCondIdx]),
				},
			}
			channelCondIdx++
		}
	}
	return EvaluationResult{Satisfied: true, ConditionResults: pers}
}

func makeUnsatisfiedResult(cond p.SingleCondition) p.ConditionResult {
	if cond.Timer != nil {
		return p.ConditionResult{
			Timer: &p.TimerConditionResult{Fired: false, FireAtUnixMs: cond.Timer.FireAtUnixMs},
		}
	}
	if cond.Channel != nil {
		return p.ConditionResult{
			Channel: &p.ChannelConditionResult{
				ChannelName: cond.Channel.ChannelName,
				Satisfied:   false,
			},
		}
	}
	return p.ConditionResult{}
}
