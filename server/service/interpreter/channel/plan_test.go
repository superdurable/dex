// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package channel

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/service/common/ptr"
)

func chCond(id, name string, atLeast, atMost *int32) *dexpb.ChannelCondition {
	return &dexpb.ChannelCondition{ConditionId: id, ChannelName: name, AtLeast: atLeast, AtMost: atMost}
}

func timerCond(id string) *dexpb.TimerCondition {
	return &dexpb.TimerCondition{ConditionId: id, DurationSeconds: 1}
}

func wcAny(channels ...*dexpb.ChannelCondition) *dexpb.WaitingConditionState {
	return &dexpb.WaitingConditionState{
		WaitingConditionType: dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ANY_COMPLETED,
		ChannelConditions:    channels,
	}
}

func wcAll(channels ...*dexpb.ChannelCondition) *dexpb.WaitingConditionState {
	return &dexpb.WaitingConditionState{
		WaitingConditionType: dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ALL_COMPLETED,
		ChannelConditions:    channels,
	}
}

func consumeByConditionIndex(plan *MatchPlan) map[int]int32 {
	counts := map[int]int32{}
	for _, consumption := range plan.Consumes {
		counts[consumption.ChannelConditionIndex] = consumption.Count
	}
	return counts
}

func TestNormalization(t *testing.T) {
	cases := []struct {
		name      string
		cond      *dexpb.ChannelCondition
		avail     int32
		wantMatch bool
		wantCount int32
	}{
		{"exact1_default_unmet", chCond("c", "ch", nil, nil), 0, false, 0},
		{"exact1_default_met", chCond("c", "ch", nil, nil), 3, true, 1},
		{"atMostOnly_empty", chCond("c", "ch", nil, ptr.Any(int32(3))), 0, true, 0},
		{"atMostOnly_partial", chCond("c", "ch", nil, ptr.Any(int32(3))), 2, true, 2},
		{"atMostOnly_capped", chCond("c", "ch", nil, ptr.Any(int32(3))), 5, true, 3},
		{"oneToAll_atLeast1_unmet", chCond("c", "ch", ptr.Any(int32(1)), nil), 0, false, 0},
		{"oneToAll_atLeast1_consumesAll", chCond("c", "ch", ptr.Any(int32(1)), nil), 4, true, 4},
		{"atLeast3ToAll_consumesAll", chCond("c", "ch", ptr.Any(int32(3)), nil), 5, true, 5},
		{"zeroToAll_explicit0_consumesAllEvenZero", chCond("c", "ch", ptr.Any(int32(0)), nil), 0, true, 0},
		{"zeroToAll_explicit0_consumesAll", chCond("c", "ch", ptr.Any(int32(0)), nil), 4, true, 4},
		{"atMost0_consumesZero", chCond("c", "ch", nil, ptr.Any(int32(0))), 2, true, 0},
		{"range2to4_met_capped", chCond("c", "ch", ptr.Any(int32(2)), ptr.Any(int32(4))), 10, true, 4},
		{"range2to4_partial", chCond("c", "ch", ptr.Any(int32(2)), ptr.Any(int32(4))), 3, true, 3},
		{"range2to4_unmet", chCond("c", "ch", ptr.Any(int32(2)), ptr.Any(int32(4))), 1, false, 0},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			waitingCondition := wcAny(testCase.cond)
			plan, ok := Plan(waitingCondition, ChannelAvailability{"ch": testCase.avail}, nil)
			assert.Equal(t, testCase.wantMatch, ok)
			if testCase.wantMatch {
				assert.Equal(t, testCase.wantCount, consumeByConditionIndex(plan)[0])
			}
		})
	}
}

func TestPlan_DoesNotMutateAvailability(t *testing.T) {
	waitingCondition := wcAny(chCond("c", "ch", ptr.Any(int32(1)), nil))
	availability := ChannelAvailability{"ch": 3}
	_, ok := Plan(waitingCondition, availability, nil)
	require.True(t, ok)
	assert.Equal(t, int32(3), availability["ch"], "Plan must not consume; commit is a separate step")
}

func TestPlan_ALL_SharedChannelCompetingMinima(t *testing.T) {
	// Two Exact-2 conditions on the same channel; ALL needs both minima.
	waitingCondition := wcAll(
		chCond("a", "ch", ptr.Any(int32(2)), ptr.Any(int32(2))),
		chCond("b", "ch", ptr.Any(int32(2)), ptr.Any(int32(2))),
	)

	_, ok := Plan(waitingCondition, ChannelAvailability{"ch": 3}, nil)
	assert.False(t, ok, "summed minima 4 > available 3 is infeasible")

	plan, ok := Plan(waitingCondition, ChannelAvailability{"ch": 4}, nil)
	require.True(t, ok)
	got := consumeByConditionIndex(plan)
	assert.Equal(t, int32(2), got[0])
	assert.Equal(t, int32(2), got[1])
}

func TestPlan_ALL_ZeroToAllDoesNotStealExactMinimum(t *testing.T) {
	// Exact-2 must keep its minimum; ZeroToAll takes only leftovers.
	waitingCondition := wcAll(
		chCond("exact", "ch", ptr.Any(int32(2)), ptr.Any(int32(2))),
		chCond("zero", "ch", ptr.Any(int32(0)), nil),
	)
	plan, ok := Plan(waitingCondition, ChannelAvailability{"ch": 3}, nil)
	require.True(t, ok)
	got := consumeByConditionIndex(plan)
	assert.Equal(t, int32(2), got[0])
	assert.Equal(t, int32(1), got[1], "ZeroToAll consumes only the remaining message")
}

func TestPlan_ALL_ZeroToAllStealsNothingWhenMinimumTight(t *testing.T) {
	waitingCondition := wcAll(
		chCond("exact", "ch", ptr.Any(int32(2)), ptr.Any(int32(2))),
		chCond("zero", "ch", ptr.Any(int32(0)), nil),
	)
	plan, ok := Plan(waitingCondition, ChannelAvailability{"ch": 2}, nil)
	require.True(t, ok)
	got := consumeByConditionIndex(plan)
	assert.Equal(t, int32(2), got[0])
	assert.Equal(t, int32(0), got[1])
}

func TestPlan_ANY_FirstFeasibleInDeclarationOrder(t *testing.T) {
	// c1 needs 5 (unmet), c2 needs 1 (met) -> ANY picks c2.
	waitingCondition := wcAny(
		chCond("c1", "chA", ptr.Any(int32(5)), ptr.Any(int32(5))),
		chCond("c2", "chB", nil, nil),
	)
	plan, ok := Plan(waitingCondition, ChannelAvailability{"chA": 1, "chB": 2}, nil)
	require.True(t, ok)
	counts := consumeByConditionIndex(plan)
	assert.Contains(t, counts, 1)
	assert.NotContains(t, counts, 0)
}

func TestPlan_ANY_TimerCandidate(t *testing.T) {
	waitingCondition := &dexpb.WaitingConditionState{
		WaitingConditionType: dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ANY_COMPLETED,
		TimerConditions:      []*dexpb.TimerCondition{timerCond("t1")},
		ChannelConditions:    []*dexpb.ChannelCondition{chCond("c1", "ch", ptr.Any(int32(5)), ptr.Any(int32(5)))},
	}

	_, ok := Plan(
		waitingCondition,
		ChannelAvailability{"ch": 0},
		map[int32]dexpb.InternalTimerStatus{},
	)
	assert.False(t, ok, "timer pending and channel unmet -> no trigger")

	plan, ok := Plan(
		waitingCondition,
		ChannelAvailability{"ch": 0},
		map[int32]dexpb.InternalTimerStatus{
			0: dexpb.InternalTimerStatus_INTERNAL_TIMER_STATUS_FIRED,
		},
	)
	require.True(t, ok, "fired timer satisfies ANY")
	assert.Empty(t, plan.Consumes)
}

func TestPlan_ALL_RequiresTimerCompletion(t *testing.T) {
	waitingCondition := &dexpb.WaitingConditionState{
		WaitingConditionType: dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ALL_COMPLETED,
		TimerConditions:      []*dexpb.TimerCondition{timerCond("t1")},
		ChannelConditions:    []*dexpb.ChannelCondition{chCond("c1", "ch", nil, nil)},
	}
	_, ok := Plan(
		waitingCondition,
		ChannelAvailability{"ch": 3},
		map[int32]dexpb.InternalTimerStatus{},
	)
	assert.False(t, ok, "ALL requires the timer to have fired")
	_, ok = Plan(
		waitingCondition,
		ChannelAvailability{"ch": 3},
		map[int32]dexpb.InternalTimerStatus{
			0: dexpb.InternalTimerStatus_INTERNAL_TIMER_STATUS_FIRED,
		},
	)
	assert.True(t, ok)
}

func TestPlan_ALL_AllowsMissingConditionIds(t *testing.T) {
	waitingCondition := &dexpb.WaitingConditionState{
		WaitingConditionType: dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ALL_COMPLETED,
		TimerConditions:      []*dexpb.TimerCondition{timerCond("")},
		ChannelConditions: []*dexpb.ChannelCondition{
			chCond("", "first", nil, nil),
			chCond("", "second", nil, nil),
		},
	}
	plan, ok := Plan(
		waitingCondition,
		ChannelAvailability{"first": 1, "second": 1},
		map[int32]dexpb.InternalTimerStatus{
			0: dexpb.InternalTimerStatus_INTERNAL_TIMER_STATUS_FIRED,
		},
	)
	require.True(t, ok)
	require.Equal(t, map[int]int32{0: 1, 1: 1}, consumeByConditionIndex(plan))
}

func TestPlan_ANY_AllowsMissingConditionIds(t *testing.T) {
	waitingCondition := wcAny(
		chCond("", "unavailable", nil, nil),
		chCond("", "available", nil, nil),
	)
	plan, ok := Plan(waitingCondition, ChannelAvailability{"available": 1}, nil)
	require.True(t, ok)
	require.Equal(t, map[int]int32{1: 1}, consumeByConditionIndex(plan))

	results := BuildConditionResults(
		waitingCondition,
		nil,
		map[int][]*dexpb.Value{1: nil},
	)
	require.Equal(
		t,
		dexpb.ConditionStatus_CONDITION_STATUS_WAITING,
		results.GetChannelResults()[0].GetConditionStatus(),
	)
	require.Equal(
		t,
		dexpb.ConditionStatus_CONDITION_STATUS_COMPLETED,
		results.GetChannelResults()[1].GetConditionStatus(),
	)
}

func TestPlan_AnyCombination(t *testing.T) {
	// Two combinations; first is infeasible (needs 5 on chA), second feasible.
	waitingCondition := &dexpb.WaitingConditionState{
		WaitingConditionType: dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ANY_COMBINATION_COMPLETED,
		ChannelConditions: []*dexpb.ChannelCondition{
			chCond("a", "chA", ptr.Any(int32(5)), ptr.Any(int32(5))),
			chCond("b", "chB", nil, nil),
			chCond("c", "chC", nil, nil),
		},
		ConditionCombinations: []*dexpb.ConditionCombination{
			{ConditionIds: []string{"a", "b"}},
			{ConditionIds: []string{"b", "c"}},
		},
	}
	plan, ok := Plan(waitingCondition, ChannelAvailability{"chA": 1, "chB": 1, "chC": 1}, nil)
	require.True(t, ok)
	counts := consumeByConditionIndex(plan)
	assert.Contains(t, counts, 1)
	assert.Contains(t, counts, 2)
	assert.NotContains(t, counts, 0)
}

func TestPlan_EmptyWaitingConditionMatches(t *testing.T) {
	waitingCondition := &dexpb.WaitingConditionState{
		WaitingConditionType: dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ALL_COMPLETED,
	}
	plan, ok := Plan(waitingCondition, ChannelAvailability{}, nil)
	require.True(t, ok)
	assert.Empty(t, plan.Consumes)
}

func TestBuildConditionResults(t *testing.T) {
	waitingCondition := &dexpb.WaitingConditionState{
		WaitingConditionType: dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ANY_COMPLETED,
		TimerConditions:      []*dexpb.TimerCondition{timerCond("t1")},
		ChannelConditions: []*dexpb.ChannelCondition{
			chCond("win", "chA", nil, nil),
			chCond("lose", "chB", nil, nil),
		},
	}
	values := []*dexpb.Value{{Kind: &dexpb.Value_StringValue{StringValue: "m1"}}}
	results := BuildConditionResults(
		waitingCondition,
		map[int32]dexpb.InternalTimerStatus{
			0: dexpb.InternalTimerStatus_INTERNAL_TIMER_STATUS_FIRED,
		},
		map[int][]*dexpb.Value{0: values},
	)

	require.Len(t, results.GetTimerResults(), 1)
	assert.Equal(
		t,
		dexpb.ConditionStatus_CONDITION_STATUS_COMPLETED,
		results.GetTimerResults()[0].GetConditionStatus(),
	)

	byId := map[string]*dexpb.ChannelResult{}
	for _, channelResult := range results.GetChannelResults() {
		byId[channelResult.GetConditionId()] = channelResult
	}
	assert.Equal(t, dexpb.ConditionStatus_CONDITION_STATUS_COMPLETED, byId["win"].GetConditionStatus())
	assert.Len(t, byId["win"].GetValues(), 1)
	assert.Equal(t, dexpb.ConditionStatus_CONDITION_STATUS_WAITING, byId["lose"].GetConditionStatus())
	assert.Empty(t, byId["lose"].GetValues())
}
