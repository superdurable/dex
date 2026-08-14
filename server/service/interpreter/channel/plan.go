// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

// Package channel plans waiting-condition channel consumption.
package channel

import "github.com/superdurable/dex/gen/dexpb"

// ChannelAvailability snapshots message counts by channel.
type ChannelAvailability map[string]int32

// Consume is one winning channel condition and the exact FIFO count to take.
type Consume struct {
	ChannelConditionIndex int
	ChannelName           string
	Count                 int32
}

// MatchPlan contains exact consumption for winning channel conditions.
type MatchPlan struct {
	Consumes []Consume
}

// Plan evaluates a validated condition against channel and timer snapshots.
func Plan(
	waitingCondition *dexpb.WaitingConditionState,
	availability ChannelAvailability,
	completedTimerConditions map[int32]dexpb.InternalTimerStatus,
	completedSubFlowConditions ...map[int32]*dexpb.FlowResult,
) (*MatchPlan, bool) {
	timers := waitingCondition.GetTimerConditions()
	channels := waitingCondition.GetChannelConditions()
	subFlows := waitingCondition.GetSubFlowConditions()
	if len(timers)+len(channels)+len(subFlows) == 0 {
		// Nothing to wait for.
		return &MatchPlan{}, true
	}

	normalizedConditions := make([]normalizedChannelCondition, len(channels))
	for i, channelCondition := range channels {
		normalizedConditions[i] = normalizeChannel(channelCondition)
	}

	completedTimers := make(map[int]bool, len(completedTimerConditions))
	for idx, status := range completedTimerConditions {
		if status == dexpb.InternalTimerStatus_INTERNAL_TIMER_STATUS_FIRED ||
			status == dexpb.InternalTimerStatus_INTERNAL_TIMER_STATUS_SKIPPED {
			completedTimers[int(idx)] = true
		}
	}
	completedSubFlows := map[int]bool{}
	if len(completedSubFlowConditions) > 0 {
		for idx, result := range completedSubFlowConditions[0] {
			if result != nil && isTerminalFlowStatus(result.GetFlowStatus()) {
				completedSubFlows[int(idx)] = true
			}
		}
	}

	for _, matchCandidate := range buildTriggerCandidates(waitingCondition) {
		if plan, ok := checkTriggerCandidate(
			matchCandidate,
			normalizedConditions,
			availability,
			completedTimers,
			completedSubFlows,
		); ok {
			return plan, true
		}
	}
	return nil, false
}

// normalizedChannelCondition contains normalized consumption bounds.
type normalizedChannelCondition struct {
	channelName string
	min         int32
	max         int32
	// Distinguishes an unbounded maximum from a bounded zero.
	unboundedMax bool
}

// triggerCandidate is a condition subset; every member must be satisfied.
// ALL, ANY, and combinations build subsets differently.
type triggerCandidate struct {
	timerIndexes   []int
	channelIndexes []int
	subFlowIndexes []int
}

func buildTriggerCandidates(
	waitingCondition *dexpb.WaitingConditionState,
) []triggerCandidate {
	timers := waitingCondition.GetTimerConditions()
	channels := waitingCondition.GetChannelConditions()
	subFlows := waitingCondition.GetSubFlowConditions()
	switch waitingCondition.GetWaitingConditionType() {
	case dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ALL_COMPLETED:
		// ALL has one candidate containing every condition.
		all := triggerCandidate{}
		for i := range timers {
			all.timerIndexes = append(all.timerIndexes, i)
		}
		for i := range channels {
			all.channelIndexes = append(all.channelIndexes, i)
		}
		for i := range subFlows {
			all.subFlowIndexes = append(all.subFlowIndexes, i)
		}
		return []triggerCandidate{all}

	case dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ANY_COMPLETED:
		// ANY has one candidate per condition.
		//
		// Canonical order: timers by declaration, then channels by declaration.
		candidates := make([]triggerCandidate, 0, len(timers)+len(channels)+len(subFlows))
		for i := range timers {
			candidates = append(candidates, triggerCandidate{timerIndexes: []int{i}})
		}
		for i := range channels {
			candidates = append(candidates, triggerCandidate{channelIndexes: []int{i}})
		}
		for i := range subFlows {
			candidates = append(candidates, triggerCandidate{subFlowIndexes: []int{i}})
		}
		return candidates

	case dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ANY_COMBINATION_COMPLETED:
		// ANY_COMBINATION has one candidate per declared combination.
		timerIndexById := make(map[string]int, len(timers))
		for i, timerCondition := range timers {
			timerIndexById[timerCondition.GetConditionId()] = i
		}
		channelIndexById := make(map[string]int, len(channels))
		for i, channelCondition := range channels {
			channelIndexById[channelCondition.GetConditionId()] = i
		}
		subFlowIndexByID := make(map[string]int, len(subFlows))
		for i, subFlowCondition := range subFlows {
			subFlowIndexByID[subFlowCondition.GetConditionId()] = i
		}
		combinations := waitingCondition.GetConditionCombinations()
		candidates := make([]triggerCandidate, 0, len(combinations))
		for _, combination := range combinations {
			var matchCandidate triggerCandidate
			for _, conditionId := range combination.GetConditionIds() {
				if timerIndex, ok := timerIndexById[conditionId]; ok {
					matchCandidate.timerIndexes = append(matchCandidate.timerIndexes, timerIndex)
				} else if channelIndex, ok := channelIndexById[conditionId]; ok {
					matchCandidate.channelIndexes = append(
						matchCandidate.channelIndexes,
						channelIndex,
					)
				} else if subFlowIndex, ok := subFlowIndexByID[conditionId]; ok {
					matchCandidate.subFlowIndexes = append(
						matchCandidate.subFlowIndexes,
						subFlowIndex,
					)
				}
			}
			// Allocate channels in original declaration order.
			sortInts(matchCandidate.channelIndexes)
			candidates = append(candidates, matchCandidate)
		}
		return candidates

	default:
		return nil
	}
}

// checkTriggerCandidate verifies timers, then reserves channels in two passes.
// It returns nil and false when unsatisfied.
func checkTriggerCandidate(
	candidateToCheck triggerCandidate,
	normalizedConditions []normalizedChannelCondition,
	availability ChannelAvailability,
	completedTimers map[int]bool,
	completedSubFlows map[int]bool,
) (*MatchPlan, bool) {
	for _, timerIndex := range candidateToCheck.timerIndexes {
		if !completedTimers[timerIndex] {
			return nil, false
		}
	}
	for _, subFlowIndex := range candidateToCheck.subFlowIndexes {
		if !completedSubFlows[subFlowIndex] {
			return nil, false
		}
	}

	remaining := map[string]int32{}
	consumeCounts := make(map[int]int32, len(candidateToCheck.channelIndexes))

	// Pass 1 reserves minimums; shared channels may make the candidate infeasible.
	for _, channelIndex := range candidateToCheck.channelIndexes {
		normalized := normalizedConditions[channelIndex]
		remainingCount := remainingForChannel(remaining, availability, normalized.channelName)
		if remainingCount < normalized.min {
			return nil, false
		}
		remaining[normalized.channelName] = remainingCount - normalized.min
		consumeCounts[channelIndex] = normalized.min
	}

	// Pass 2 allocates remaining capacity without stealing reserved minima.
	for _, channelIndex := range candidateToCheck.channelIndexes {
		normalized := normalizedConditions[channelIndex]
		remainingCount := remaining[normalized.channelName]
		if remainingCount <= 0 {
			continue
		}
		var extra int32
		if normalized.unboundedMax {
			extra = remainingCount
		} else {
			room := normalized.max - normalized.min
			if room > remainingCount {
				extra = remainingCount
			} else {
				extra = room
			}
		}
		consumeCounts[channelIndex] += extra
		remaining[normalized.channelName] = remainingCount - extra
	}

	plan := &MatchPlan{}
	for _, channelIndex := range candidateToCheck.channelIndexes {
		normalized := normalizedConditions[channelIndex]
		plan.Consumes = append(plan.Consumes, Consume{
			ChannelConditionIndex: channelIndex,
			ChannelName:           normalized.channelName,
			Count:                 consumeCounts[channelIndex],
		})
	}
	return plan, true
}

func remainingForChannel(
	remaining map[string]int32,
	availability ChannelAvailability,
	channelName string,
) int32 {
	if remainingCount, ok := remaining[channelName]; ok {
		return remainingCount
	}
	return availability[channelName]
}

// normalizeChannel applies exact and bounded consumption semantics.
func normalizeChannel(condition *dexpb.ChannelCondition) normalizedChannelCondition {
	normalized := normalizedChannelCondition{
		channelName: condition.GetChannelName(),
	}
	hasAtLeast := condition.AtLeast != nil
	atLeast := condition.GetAtLeast()
	hasAtMost := condition.AtMost != nil
	atMost := condition.GetAtMost()

	switch {
	case !hasAtLeast && !hasAtMost:
		normalized.min, normalized.max = 1, 1 // Both unset means Exact 1.
	case !hasAtLeast && hasAtMost:
		normalized.max = atMost
	case hasAtLeast && !hasAtMost:
		normalized.min, normalized.unboundedMax = atLeast, true
	default:
		normalized.min, normalized.max = atLeast, atMost
	}
	return normalized
}

// BuildConditionResults reports timer, channel, and SubFlow states.
func BuildConditionResults(
	waitingCondition *dexpb.WaitingConditionState,
	completedTimerConditions map[int32]dexpb.InternalTimerStatus,
	consumedByChannelConditionIndex map[int][]*dexpb.Value,
	completedSubFlowConditions ...map[int32]*dexpb.FlowResult,
) *dexpb.ConditionResults {
	results := &dexpb.ConditionResults{}
	for timerIndex, timerCondition := range waitingCondition.GetTimerConditions() {
		status := dexpb.ConditionStatus_CONDITION_STATUS_WAITING
		if _, ok := completedTimerConditions[int32(timerIndex)]; ok {
			status = dexpb.ConditionStatus_CONDITION_STATUS_COMPLETED
		}
		results.TimerResults = append(results.TimerResults, &dexpb.TimerResult{
			ConditionId:     timerCondition.GetConditionId(),
			ConditionStatus: status,
		})
	}
	for channelIndex, channelCondition := range waitingCondition.GetChannelConditions() {
		channelResult := &dexpb.ChannelResult{
			ConditionId:     channelCondition.GetConditionId(),
			ChannelName:     channelCondition.GetChannelName(),
			ConditionStatus: dexpb.ConditionStatus_CONDITION_STATUS_WAITING,
		}
		if values, completed := consumedByChannelConditionIndex[channelIndex]; completed {
			channelResult.ConditionStatus = dexpb.ConditionStatus_CONDITION_STATUS_COMPLETED
			channelResult.Values = values
		}
		results.ChannelResults = append(results.ChannelResults, channelResult)
	}
	completedSubFlows := map[int32]*dexpb.FlowResult{}
	if len(completedSubFlowConditions) > 0 {
		completedSubFlows = completedSubFlowConditions[0]
	}
	for subFlowIndex := range waitingCondition.GetSubFlowConditions() {
		result := completedSubFlows[int32(subFlowIndex)]
		if result == nil {
			result = &dexpb.FlowResult{
				FlowStatus: dexpb.FlowStatus_FLOW_STATUS_RUNNING,
			}
		}
		results.SubFlowResults = append(results.SubFlowResults, result)
	}
	return results
}

func isTerminalFlowStatus(status dexpb.FlowStatus) bool {
	return status != dexpb.FlowStatus_FLOW_STATUS_RUNNING &&
		status != dexpb.FlowStatus_FLOW_STATUS_CONTINUED_AS_NEW &&
		status != dexpb.FlowStatus_FLOW_STATUS_UNSPECIFIED
}

// sortInts sorts triggerCandidate indexes deterministically.
func sortInts(values []int) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j-1] > values[j]; j-- {
			values[j-1], values[j] = values[j], values[j-1]
		}
	}
}
