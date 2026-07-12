package evaluate

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	p "github.com/superdurable/dex/server/internal/persistence"
)

const testStepExeID = "step-test"

func msgSlice(count int) []p.ChannelMessage {
	msgs := make([]p.ChannelMessage, count)
	for i := range msgs {
		v := int64(i + 1)
		msgs[i] = p.ChannelMessage{ID: int64(i + 1), Value: p.Value{Type: p.ValueTypeInt, IntVal: &v}}
	}
	return msgs
}

func newTestEvaluator(
	waitFor *p.WaitForCondition,
	unconsumed map[string][]p.ChannelMessage,
	effectiveNow int64,
	active map[string]p.ActiveStepExecution,
) *ConditionEvaluator {
	if active == nil {
		active = map[string]p.ActiveStepExecution{
			testStepExeID: {WaitForCondition: waitFor},
		}
	} else if step, ok := active[testStepExeID]; ok {
		step.WaitForCondition = waitFor
		active[testStepExeID] = step
	} else {
		active[testStepExeID] = p.ActiveStepExecution{WaitForCondition: waitFor}
	}
	return NewConditionEvaluator(active, effectiveNow, unconsumed)
}

func evalWaitFor(
	waitFor *p.WaitForCondition,
	unconsumed map[string][]p.ChannelMessage,
	effectiveNow int64,
	active map[string]p.ActiveStepExecution,
) (EvaluationResult, error) {
	return newTestEvaluator(waitFor, unconsumed, effectiveNow, active).EvaluateWaitForCondition(testStepExeID)
}

func anyOfTimer(fireAtMs int64) *p.WaitForCondition {
	return &p.WaitForCondition{
		Type:       p.WaitTypeAnyOf,
		Conditions: []p.SingleCondition{{Timer: &p.TimerCondition{FireAtUnixMs: fireAtMs}}},
	}
}

func anyOfChannel(name string, min, max int32) *p.WaitForCondition {
	return &p.WaitForCondition{
		Type:       p.WaitTypeAnyOf,
		Conditions: []p.SingleCondition{{Channel: &p.ChannelCondition{ChannelName: name, Min: min, Max: max}}},
	}
}

func allOfTimerChannel(fireAtMs int64, name string, min int32) *p.WaitForCondition {
	return &p.WaitForCondition{
		Type: p.WaitTypeAllOf,
		Conditions: []p.SingleCondition{
			{Timer: &p.TimerCondition{FireAtUnixMs: fireAtMs}},
			{Channel: &p.ChannelCondition{ChannelName: name, Min: min}},
		},
	}
}

// ============================================================================
// AnyOf
// ============================================================================

func TestEvaluateWaitForCondition_AnyOf_TimerFired(t *testing.T) {
	res, err := evalWaitFor(anyOfTimer(100), nil, 200, nil)
	require.NoError(t, err)
	assert.True(t, res.Satisfied)
	require.NotNil(t, res.ConditionResults[0].Timer)
	assert.True(t, res.ConditionResults[0].Timer.Fired)
}

func TestEvaluateWaitForCondition_AnyOf_TimerNotFired(t *testing.T) {
	res, err := evalWaitFor(anyOfTimer(200), nil, 100, nil)
	require.NoError(t, err)
	assert.False(t, res.Satisfied)
	require.NotNil(t, res.ConditionResults[0].Timer)
	assert.False(t, res.ConditionResults[0].Timer.Fired)
}

func TestEvaluateWaitForCondition_AnyOf_ChannelMinSatisfied(t *testing.T) {
	unconsumed := map[string][]p.ChannelMessage{"ch1": msgSlice(3)}
	res, err := evalWaitFor(anyOfChannel("ch1", 2, 0), unconsumed, 0, nil)
	require.NoError(t, err)
	assert.True(t, res.Satisfied)
	require.NotNil(t, res.ConditionResults[0].Channel)
	assert.True(t, res.ConditionResults[0].Channel.Satisfied)
	assert.Equal(t, int32(2), res.ConditionResults[0].Channel.ConsumedCount)
}

func TestEvaluateWaitForCondition_AnyOf_ChannelUnderMin(t *testing.T) {
	unconsumed := map[string][]p.ChannelMessage{"ch1": msgSlice(1)}
	res, err := evalWaitFor(anyOfChannel("ch1", 3, 0), unconsumed, 0, nil)
	require.NoError(t, err)
	assert.False(t, res.Satisfied)
	require.NotNil(t, res.ConditionResults[0].Channel)
	assert.False(t, res.ConditionResults[0].Channel.Satisfied)
}

func TestEvaluateWaitForCondition_AnyOf_ChannelMaxCapsConsumption(t *testing.T) {
	unconsumed := map[string][]p.ChannelMessage{"ch1": msgSlice(5)}
	res, err := evalWaitFor(anyOfChannel("ch1", 1, 3), unconsumed, 0, nil)
	require.NoError(t, err)
	assert.True(t, res.Satisfied)
	assert.Equal(t, int32(3), res.ConditionResults[0].Channel.ConsumedCount)
}

func TestEvaluateWaitForCondition_AnyOf_ChannelMaxLargerThanAvailable(t *testing.T) {
	unconsumed := map[string][]p.ChannelMessage{"ch1": msgSlice(2)}
	res, err := evalWaitFor(anyOfChannel("ch1", 1, 5), unconsumed, 0, nil)
	require.NoError(t, err)
	assert.True(t, res.Satisfied)
	assert.Equal(t, int32(2), res.ConditionResults[0].Channel.ConsumedCount)
}

func TestEvaluateWaitForCondition_AnyOf_TimerFiredAndChannelBothConsumed_Greedy(t *testing.T) {
	// Server evaluateAnyOf is greedy (symmetric with SDK): a fired timer does
	// NOT short-circuit — every satisfied branch is marked and the channel's
	// messages are still reserved.
	waitFor := &p.WaitForCondition{
		Type: p.WaitTypeAnyOf,
		Conditions: []p.SingleCondition{
			{Timer: &p.TimerCondition{FireAtUnixMs: 100}},
			{Channel: &p.ChannelCondition{ChannelName: "ch1", Min: 1}},
		},
	}
	unconsumed := map[string][]p.ChannelMessage{"ch1": msgSlice(1)}
	res, err := evalWaitFor(waitFor, unconsumed, 200, nil)
	require.NoError(t, err)
	assert.True(t, res.Satisfied)
	assert.True(t, res.ConditionResults[0].Timer.Fired)
	assert.True(t, res.ConditionResults[1].Channel.Satisfied)
	assert.Equal(t, int32(1), res.ConditionResults[1].Channel.ConsumedCount)
}

func TestEvaluateWaitForCondition_AnyOf_SameChannelTwice_DoesNotOverReserve(t *testing.T) {
	// Greedy AnyOf with two conditions on the SAME channel must not reserve the
	// same messages twice: the second branch sees only what the first left.
	waitFor := &p.WaitForCondition{
		Type: p.WaitTypeAnyOf,
		Conditions: []p.SingleCondition{
			{Channel: &p.ChannelCondition{ChannelName: "ch1", Min: 2}},
			{Channel: &p.ChannelCondition{ChannelName: "ch1", Min: 2}},
		},
	}
	unconsumed := map[string][]p.ChannelMessage{"ch1": msgSlice(3)}
	res, err := evalWaitFor(waitFor, unconsumed, 0, nil)
	require.NoError(t, err)
	assert.True(t, res.Satisfied)
	assert.True(t, res.ConditionResults[0].Channel.Satisfied)
	assert.Equal(t, int32(2), res.ConditionResults[0].Channel.ConsumedCount)
	// Only 1 message remains after the first branch took 2 → second (min 2) fails.
	assert.False(t, res.ConditionResults[1].Channel.Satisfied)
}

func TestEvaluateWaitForCondition_AnyOf_NeitherSatisfied(t *testing.T) {
	waitFor := &p.WaitForCondition{
		Type: p.WaitTypeAnyOf,
		Conditions: []p.SingleCondition{
			{Timer: &p.TimerCondition{FireAtUnixMs: 200}},
			{Channel: &p.ChannelCondition{ChannelName: "ch1", Min: 1}},
		},
	}
	res, err := evalWaitFor(waitFor, nil, 100, nil)
	require.NoError(t, err)
	assert.False(t, res.Satisfied)
}

// ============================================================================
// AllOf
// ============================================================================

func TestEvaluateWaitForCondition_AllOf_BothSatisfied(t *testing.T) {
	unconsumed := map[string][]p.ChannelMessage{"ch1": msgSlice(2)}
	res, err := evalWaitFor(allOfTimerChannel(100, "ch1", 1), unconsumed, 200, nil)
	require.NoError(t, err)
	assert.True(t, res.Satisfied)
	require.NotNil(t, res.ConditionResults[0].Timer)
	assert.True(t, res.ConditionResults[0].Timer.Fired)
	assert.Equal(t, int32(1), res.ConditionResults[1].Channel.ConsumedCount)
}

func TestEvaluateWaitForCondition_AllOf_TimerNotFired(t *testing.T) {
	unconsumed := map[string][]p.ChannelMessage{"ch1": msgSlice(1)}
	res, err := evalWaitFor(allOfTimerChannel(200, "ch1", 1), unconsumed, 100, nil)
	require.NoError(t, err)
	assert.False(t, res.Satisfied)
}

func TestEvaluateWaitForCondition_AllOf_ChannelUnderMin(t *testing.T) {
	res, err := evalWaitFor(allOfTimerChannel(100, "ch1", 1), nil, 200, nil)
	require.NoError(t, err)
	assert.False(t, res.Satisfied)
}

func TestEvaluateWaitForCondition_AllOf_MultipleChannels_AllReady(t *testing.T) {
	waitFor := &p.WaitForCondition{
		Type: p.WaitTypeAllOf,
		Conditions: []p.SingleCondition{
			{Channel: &p.ChannelCondition{ChannelName: "ch1", Min: 1}},
			{Channel: &p.ChannelCondition{ChannelName: "ch2", Min: 1}},
			{Channel: &p.ChannelCondition{ChannelName: "ch3", Min: 2}},
		},
	}
	unconsumed := map[string][]p.ChannelMessage{
		"ch1": msgSlice(2),
		"ch2": msgSlice(1),
		"ch3": msgSlice(3),
	}
	res, err := evalWaitFor(waitFor, unconsumed, 0, nil)
	require.NoError(t, err)
	assert.True(t, res.Satisfied)
	assert.Equal(t, int32(1), res.ConditionResults[0].Channel.ConsumedCount)
	assert.Equal(t, int32(1), res.ConditionResults[1].Channel.ConsumedCount)
	assert.Equal(t, int32(2), res.ConditionResults[2].Channel.ConsumedCount)
}

func TestEvaluateWaitForCondition_AllOf_MultipleChannels_OneShort(t *testing.T) {
	waitFor := &p.WaitForCondition{
		Type: p.WaitTypeAllOf,
		Conditions: []p.SingleCondition{
			{Channel: &p.ChannelCondition{ChannelName: "ch1", Min: 1}},
			{Channel: &p.ChannelCondition{ChannelName: "ch2", Min: 1}},
			{Channel: &p.ChannelCondition{ChannelName: "ch3", Min: 2}},
		},
	}
	unconsumed := map[string][]p.ChannelMessage{
		"ch1": msgSlice(1),
		"ch2": msgSlice(1),
		"ch3": msgSlice(1),
	}
	res, err := evalWaitFor(waitFor, unconsumed, 0, nil)
	require.NoError(t, err)
	assert.False(t, res.Satisfied)
}

func TestEvaluateWaitForCondition_AllOf_SameChannelTwice_AggregatesMin(t *testing.T) {
	waitFor := &p.WaitForCondition{
		Type: p.WaitTypeAllOf,
		Conditions: []p.SingleCondition{
			{Channel: &p.ChannelCondition{ChannelName: "ch1", Min: 2}},
			{Channel: &p.ChannelCondition{ChannelName: "ch1", Min: 1}},
		},
	}
	// 2 messages: under totalMin of 3 → not satisfied
	res, err := evalWaitFor(waitFor, map[string][]p.ChannelMessage{"ch1": msgSlice(2)}, 0, nil)
	require.NoError(t, err)
	assert.False(t, res.Satisfied)

	// 4 messages: enough → satisfied, counts distributed by min
	res, err = evalWaitFor(waitFor, map[string][]p.ChannelMessage{"ch1": msgSlice(4)}, 0, nil)
	require.NoError(t, err)
	assert.True(t, res.Satisfied)
	assert.Equal(t, int32(2), res.ConditionResults[0].Channel.ConsumedCount)
	assert.Equal(t, int32(1), res.ConditionResults[1].Channel.ConsumedCount)
}

func TestEvaluateWaitForCondition_AllOf_SameChannelDistributesSurplusByMax(t *testing.T) {
	waitFor := &p.WaitForCondition{
		Type: p.WaitTypeAllOf,
		Conditions: []p.SingleCondition{
			{Channel: &p.ChannelCondition{ChannelName: "ch1", Min: 1, Max: 3}},
			{Channel: &p.ChannelCondition{ChannelName: "ch1", Min: 1, Max: 3}},
		},
	}
	unconsumed := map[string][]p.ChannelMessage{"ch1": msgSlice(5)}
	res, err := evalWaitFor(waitFor, unconsumed, 0, nil)
	require.NoError(t, err)
	assert.True(t, res.Satisfied)
	assert.Equal(t, int32(3), res.ConditionResults[0].Channel.ConsumedCount)
	assert.Equal(t, int32(2), res.ConditionResults[1].Channel.ConsumedCount)
}

// ============================================================================
// Reservation-aware evaluation
// ============================================================================

func TestEvaluateWaitForCondition_AnyOf_AccountsForReservations(t *testing.T) {
	unconsumed := map[string][]p.ChannelMessage{"ch": msgSlice(3)}
	active := map[string]p.ActiveStepExecution{
		"other": {
			Status:             p.StepExeStatusInvokingExecute,
			ExecuteMethodExeID: 1,
			ConditionResults: []p.ConditionResult{{
				Channel: &p.ChannelConditionResult{
					ChannelName: "ch", Satisfied: true, ConsumedCount: 2,
				},
			}},
		},
	}

	// 3 total, 2 reserved → 1 available; min=2 → not satisfied
	res, err := evalWaitFor(anyOfChannel("ch", 2, 0), unconsumed, 0, active)
	require.NoError(t, err)
	assert.False(t, res.Satisfied)

	// min=1 → satisfied with 1 message
	res, err = evalWaitFor(anyOfChannel("ch", 1, 0), unconsumed, 0, active)
	require.NoError(t, err)
	assert.True(t, res.Satisfied)
	assert.Equal(t, int32(1), res.ConditionResults[0].Channel.ConsumedCount)
}

func TestEvaluateWaitForCondition_AllOf_AccountsForReservations(t *testing.T) {
	unconsumed := map[string][]p.ChannelMessage{"ch": msgSlice(4)}
	active := map[string]p.ActiveStepExecution{
		"other": {
			Status:             p.StepExeStatusInvokingExecute,
			ExecuteMethodExeID: 1,
			ConditionResults: []p.ConditionResult{{
				Channel: &p.ChannelConditionResult{
					ChannelName: "ch", Satisfied: true, ConsumedCount: 3,
				},
			}},
		},
	}

	// 4 total, 3 reserved → 1 available; allOf needs 2 → not satisfied
	waitFor := &p.WaitForCondition{
		Type:       p.WaitTypeAllOf,
		Conditions: []p.SingleCondition{{Channel: &p.ChannelCondition{ChannelName: "ch", Min: 2}}},
	}
	res, err := evalWaitFor(waitFor, unconsumed, 0, active)
	require.NoError(t, err)
	assert.False(t, res.Satisfied)
}

// ============================================================================
// Error cases
// ============================================================================

func TestEvaluateWaitForCondition_NilCondition_ReturnsError(t *testing.T) {
	_, err := evalWaitFor(nil, nil, 0, nil)
	require.Error(t, err)
}

func TestEvaluateWaitForCondition_EmptyConditions_ReturnsError(t *testing.T) {
	waitFor := &p.WaitForCondition{Type: p.WaitTypeAnyOf, Conditions: []p.SingleCondition{}}
	_, err := evalWaitFor(waitFor, nil, 0, nil)
	require.Error(t, err)
}

func TestEvaluateWaitForCondition_UnknownWaitType_ReturnsError(t *testing.T) {
	waitFor := &p.WaitForCondition{
		Type:       99,
		Conditions: []p.SingleCondition{{Timer: &p.TimerCondition{FireAtUnixMs: 1}}},
	}
	_, err := evalWaitFor(waitFor, nil, 0, nil)
	require.Error(t, err)
}

func TestEvaluateWaitForCondition_StepExeIDNotFound_ReturnsError(t *testing.T) {
	eval := NewConditionEvaluator(map[string]p.ActiveStepExecution{}, 0, nil)
	_, err := eval.EvaluateWaitForCondition("nonexistent")
	require.Error(t, err)
}