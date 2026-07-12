package evaluate

import (
	"testing"

	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testStepExeID = "step-test"

func intPbValue(value int64) *pb.Value {
	return &pb.Value{Kind: &pb.Value_IntValue{IntValue: value}}
}

func newEvaluator(
	waitFor *pb.WaitForCondition,
	unconsumed map[string][]*pb.Value,
	effectiveNow int64,
	active map[string]*pb.ActiveStepExecution,
) *ConditionEvaluator {
	if active == nil {
		active = map[string]*pb.ActiveStepExecution{
			testStepExeID: {WaitForCondition: waitFor},
		}
	} else if step, ok := active[testStepExeID]; ok {
		step.WaitForCondition = waitFor
	} else {
		active[testStepExeID] = &pb.ActiveStepExecution{WaitForCondition: waitFor}
	}
	return NewConditionEvaluator(active, effectiveNow, unconsumed)
}

func evalWaitFor(
	waitFor *pb.WaitForCondition,
	unconsumed map[string][]*pb.Value,
	effectiveNow int64,
	active map[string]*pb.ActiveStepExecution,
) (*EvaluationResult, error) {
	return newEvaluator(waitFor, unconsumed, effectiveNow, active).EvaluateWaitForCondition(testStepExeID)
}

func anyOfTimer(fireAtMs int64) *pb.WaitForCondition {
	return &pb.WaitForCondition{
		Type: pb.WaitType_WAIT_TYPE_ANY_OF,
		Conditions: []*pb.SingleCondition{{
			Condition: &pb.SingleCondition_Timer{Timer: &pb.TimerCondition{FireAtUnixMs: fireAtMs}},
		}},
	}
}

func anyOfChannel(name string, min, max int32) *pb.WaitForCondition {
	return &pb.WaitForCondition{
		Type: pb.WaitType_WAIT_TYPE_ANY_OF,
		Conditions: []*pb.SingleCondition{{
			Condition: &pb.SingleCondition_Channel{Channel: &pb.ChannelCondition{ChannelName: name, Min: min, Max: max}},
		}},
	}
}

func allOfTimerChannel(fireAtMs int64, name string, min int32) *pb.WaitForCondition {
	return &pb.WaitForCondition{
		Type: pb.WaitType_WAIT_TYPE_ALL_OF,
		Conditions: []*pb.SingleCondition{
			{Condition: &pb.SingleCondition_Timer{Timer: &pb.TimerCondition{FireAtUnixMs: fireAtMs}}},
			{Condition: &pb.SingleCondition_Channel{Channel: &pb.ChannelCondition{ChannelName: name, Min: min}}},
		},
	}
}

func TestEvaluateWaitForCondition_AnyOf_TimerNotFired_ChannelUnderMin_NotSatisfied(t *testing.T) {
	res, err := evalWaitFor(
		anyOfChannel("ch1", 2, 0),
		map[string][]*pb.Value{"ch1": {intPbValue(1)}},
		100,
		nil,
	)
	require.NoError(t, err)
	assert.False(t, res.Satisfied)
	assert.False(t, res.ConditionResults[0].GetChannel().Satisfied)
}

func TestEvaluateWaitForCondition_AnyOf_TimerFired_Wins(t *testing.T) {
	res, err := evalWaitFor(anyOfTimer(100), nil, 200, nil)
	require.NoError(t, err)
	assert.True(t, res.Satisfied)
	assert.True(t, res.ConditionResults[0].GetTimer().Fired)
}

func TestEvaluateWaitForCondition_AnyOf_ChannelMinSatisfied(t *testing.T) {
	msgs := map[string][]*pb.Value{"ch1": {intPbValue(1), intPbValue(2), intPbValue(3)}}
	res, err := evalWaitFor(anyOfChannel("ch1", 2, 0), msgs, 100, nil)
	require.NoError(t, err)
	assert.True(t, res.Satisfied)
	assert.Equal(t, int32(2), res.ConditionResults[0].GetChannel().ConsumedCount)
}

func TestEvaluateWaitForCondition_AnyOf_ChannelWithMaxLargerThanAvailable(t *testing.T) {
	msgs := map[string][]*pb.Value{"ch1": {intPbValue(1), intPbValue(2)}}
	res, err := evalWaitFor(anyOfChannel("ch1", 1, 5), msgs, 100, nil)
	require.NoError(t, err)
	assert.True(t, res.Satisfied)
	assert.Equal(t, int32(2), res.ConditionResults[0].GetChannel().ConsumedCount)
}

func TestEvaluateWaitForCondition_AnyOf_GreedyEvaluatesAllSatisfiedBranches(t *testing.T) {
	waitFor := &pb.WaitForCondition{
		Type: pb.WaitType_WAIT_TYPE_ANY_OF,
		Conditions: []*pb.SingleCondition{
			{Condition: &pb.SingleCondition_Timer{Timer: &pb.TimerCondition{FireAtUnixMs: 100}}},
			{Condition: &pb.SingleCondition_Channel{Channel: &pb.ChannelCondition{ChannelName: "ch1", Min: 1}}},
		},
	}
	msgs := map[string][]*pb.Value{"ch1": {intPbValue(1)}}
	res, err := evalWaitFor(waitFor, msgs, 200, nil)
	require.NoError(t, err)
	assert.True(t, res.Satisfied)
	assert.True(t, res.ConditionResults[0].GetTimer().Fired)
	assert.True(t, res.ConditionResults[1].GetChannel().Satisfied)
	assert.Equal(t, int32(1), res.ConditionResults[1].GetChannel().ConsumedCount)
}

func TestEvaluateWaitForCondition_AllOf_TimerNotFired_NotSatisfied(t *testing.T) {
	res, err := evalWaitFor(
		allOfTimerChannel(200, "ch1", 1),
		map[string][]*pb.Value{"ch1": {intPbValue(1)}},
		100,
		nil,
	)
	require.NoError(t, err)
	assert.False(t, res.Satisfied)
	assert.False(t, res.ConditionResults[0].GetTimer().Fired)
	assert.False(t, res.ConditionResults[1].GetChannel().Satisfied)
}

func TestEvaluateWaitForCondition_AllOf_BothSatisfied(t *testing.T) {
	res, err := evalWaitFor(
		allOfTimerChannel(100, "ch1", 1),
		map[string][]*pb.Value{"ch1": {intPbValue(7), intPbValue(8)}},
		200,
		nil,
	)
	require.NoError(t, err)
	assert.True(t, res.Satisfied)
	assert.True(t, res.ConditionResults[0].GetTimer().Fired)
	assert.Equal(t, int32(1), res.ConditionResults[1].GetChannel().ConsumedCount)
}

func TestEvaluateWaitForCondition_AllOf_SameChannelTwice_AggregatesMin(t *testing.T) {
	waitFor := &pb.WaitForCondition{
		Type: pb.WaitType_WAIT_TYPE_ALL_OF,
		Conditions: []*pb.SingleCondition{
			{Condition: &pb.SingleCondition_Channel{Channel: &pb.ChannelCondition{ChannelName: "ch1", Min: 2}}},
			{Condition: &pb.SingleCondition_Channel{Channel: &pb.ChannelCondition{ChannelName: "ch1", Min: 1}}},
		},
	}
	res, err := evalWaitFor(
		waitFor,
		map[string][]*pb.Value{"ch1": {intPbValue(1), intPbValue(2)}},
		0,
		nil,
	)
	require.NoError(t, err)
	assert.False(t, res.Satisfied)

	res, err = evalWaitFor(
		waitFor,
		map[string][]*pb.Value{"ch1": {intPbValue(1), intPbValue(2), intPbValue(3), intPbValue(4)}},
		0,
		nil,
	)
	require.NoError(t, err)
	assert.True(t, res.Satisfied)
	assert.Equal(t, int32(2), res.ConditionResults[0].GetChannel().ConsumedCount)
	assert.Equal(t, int32(1), res.ConditionResults[1].GetChannel().ConsumedCount)
}

func TestEvaluateWaitForCondition_AllOf_SameChannelDistributesSurplusByMax(t *testing.T) {
	waitFor := &pb.WaitForCondition{
		Type: pb.WaitType_WAIT_TYPE_ALL_OF,
		Conditions: []*pb.SingleCondition{
			{Condition: &pb.SingleCondition_Channel{Channel: &pb.ChannelCondition{ChannelName: "ch1", Min: 1, Max: 3}}},
			{Condition: &pb.SingleCondition_Channel{Channel: &pb.ChannelCondition{ChannelName: "ch1", Min: 1, Max: 3}}},
		},
	}
	res, err := evalWaitFor(
		waitFor,
		map[string][]*pb.Value{"ch1": {intPbValue(1), intPbValue(2), intPbValue(3), intPbValue(4), intPbValue(5)}},
		0,
		nil,
	)
	require.NoError(t, err)
	assert.True(t, res.Satisfied)
	assert.Equal(t, int32(3), res.ConditionResults[0].GetChannel().ConsumedCount)
	assert.Equal(t, int32(2), res.ConditionResults[1].GetChannel().ConsumedCount)
}

func TestEvaluateWaitForCondition_UnknownWaitType_Errors(t *testing.T) {
	waitFor := &pb.WaitForCondition{
		Type: 99,
		Conditions: []*pb.SingleCondition{
			{Condition: &pb.SingleCondition_Timer{Timer: &pb.TimerCondition{FireAtUnixMs: 1}}},
		},
	}
	_, err := newEvaluator(waitFor, nil, 0, nil).EvaluateWaitForCondition(testStepExeID)
	require.Error(t, err)
}

func TestEvaluateWaitForCondition_AnyOf_AccountsForReservations(t *testing.T) {
	unconsumed := map[string][]*pb.Value{
		"ch": {intPbValue(1), intPbValue(2), intPbValue(3)},
	}
	active := map[string]*pb.ActiveStepExecution{
		testStepExeID: {WaitForCondition: anyOfChannel("ch", 2, 0)},
		"other": {
			ExecuteMethodExeId: 1,
			ConditionResults: []*pb.ConditionResult{{
				Result: &pb.ConditionResult_Channel{
					Channel: &pb.ChannelConditionResult{
						ChannelName: "ch", Satisfied: true, ConsumedCount: 2,
					},
				},
			}},
		},
	}
	res, err := evalWaitFor(anyOfChannel("ch", 2, 0), unconsumed, 0, active)
	require.NoError(t, err)
	assert.False(t, res.Satisfied)

	res, err = evalWaitFor(anyOfChannel("ch", 1, 0), unconsumed, 0, active)
	require.NoError(t, err)
	assert.True(t, res.Satisfied)
	assert.Equal(t, int32(1), res.ConditionResults[0].GetChannel().ConsumedCount)
}
