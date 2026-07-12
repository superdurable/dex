package evaluate

import (
	"testing"

	p "github.com/superdurable/dex/server/internal/persistence"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func chMsg(id, v int64) p.ChannelMessage {
	val := p.Value{Type: p.ValueTypeInt, IntVal: &v}
	return p.ChannelMessage{ID: id, Value: val}
}

func reservedStepExe(exeID int64, channel string, count int32) p.ActiveStepExecution {
	return p.ActiveStepExecution{
		Status:             p.StepExeStatusInvokingExecute,
		ExecuteMethodExeID: exeID,
		ConditionResults: []p.ConditionResult{{
			Channel: &p.ChannelConditionResult{
				ChannelName: channel, Satisfied: true, ConsumedCount: count,
			},
		}},
	}
}

func msgs(vs ...int64) []p.ChannelMessage {
	out := make([]p.ChannelMessage, len(vs))
	for i, v := range vs {
		out[i] = chMsg(int64(i+1), v)
	}
	return out
}

// ============================================================================
// hasMetSingleChannelCondition
// ============================================================================

func TestHasMetSingleChannelCondition_NoReservation(t *testing.T) {
	unconsumed := map[string][]p.ChannelMessage{"ch": msgs(1, 2, 3)}

	met, count := hasMetSingleChannelCondition(&p.ChannelCondition{ChannelName: "ch", Min: 1}, nil, unconsumed, 0)
	assert.True(t, met)
	assert.Equal(t, 1, count)

	met, count = hasMetSingleChannelCondition(&p.ChannelCondition{ChannelName: "ch", Min: 2}, nil, unconsumed, 0)
	assert.True(t, met)
	assert.Equal(t, 2, count)

	met, count = hasMetSingleChannelCondition(&p.ChannelCondition{ChannelName: "ch", Min: 1, Max: 3}, nil, unconsumed, 0)
	assert.True(t, met)
	assert.Equal(t, 3, count)

	met, count = hasMetSingleChannelCondition(&p.ChannelCondition{ChannelName: "ch", Min: 4}, nil, unconsumed, 0)
	assert.False(t, met)
	assert.Equal(t, 0, count)

	met, count = hasMetSingleChannelCondition(nil, nil, unconsumed, 0)
	assert.False(t, met)
	assert.Equal(t, 0, count)
}

func TestHasMetSingleChannelCondition_AccountsForReservations(t *testing.T) {
	unconsumed := map[string][]p.ChannelMessage{"ch": msgs(1, 2, 3)}
	active := map[string]p.ActiveStepExecution{
		"stepA": reservedStepExe(1, "ch", 1),
		"stepB": reservedStepExe(2, "ch", 1),
	}

	// 3 total - 2 reserved = 1 available
	met, count := hasMetSingleChannelCondition(&p.ChannelCondition{ChannelName: "ch", Min: 1}, active, unconsumed, 0)
	assert.True(t, met)
	assert.Equal(t, 1, count)

	met, count = hasMetSingleChannelCondition(&p.ChannelCondition{ChannelName: "ch", Min: 2}, active, unconsumed, 0)
	assert.False(t, met)
	assert.Equal(t, 0, count)
}

func TestHasMetSingleChannelCondition_MissingChannel(t *testing.T) {
	active := map[string]p.ActiveStepExecution{
		"stepA": reservedStepExe(1, "ch", 1),
	}
	met, count := hasMetSingleChannelCondition(&p.ChannelCondition{ChannelName: "ch", Min: 1}, active, map[string][]p.ChannelMessage{}, 0)
	assert.False(t, met)
	assert.Equal(t, 0, count)
}

// ============================================================================
// hasMetAllChannelConditions
// ============================================================================

func TestHasMetAllChannelConditions_Empty(t *testing.T) {
	met, counts := hasMetAllChannelConditions(nil, nil, nil)
	assert.True(t, met)
	assert.Empty(t, counts)
}

func TestHasMetAllChannelConditions_AllDistinctChannelsMet(t *testing.T) {
	unconsumed := map[string][]p.ChannelMessage{
		"ch1": msgs(1, 2),
		"ch2": msgs(10),
	}
	conds := []*p.ChannelCondition{
		{ChannelName: "ch1", Min: 2},
		{ChannelName: "ch2", Min: 1},
	}
	met, counts := hasMetAllChannelConditions(conds, nil, unconsumed)
	assert.True(t, met)
	assert.Equal(t, []int{2, 1}, counts)
}

func TestHasMetAllChannelConditions_OneChannelShortFailsAll(t *testing.T) {
	unconsumed := map[string][]p.ChannelMessage{
		"ch1": msgs(1, 2, 3),
		"ch2": {},
	}
	conds := []*p.ChannelCondition{
		{ChannelName: "ch1", Min: 1},
		{ChannelName: "ch2", Min: 1},
	}
	met, counts := hasMetAllChannelConditions(conds, nil, unconsumed)
	assert.False(t, met)
	assert.Nil(t, counts)
}

func TestHasMetAllChannelConditions_SameChannelAggregatesMin(t *testing.T) {
	unconsumed := map[string][]p.ChannelMessage{"ch1": msgs(1, 2, 3, 4, 5)}
	conds := []*p.ChannelCondition{
		{ChannelName: "ch1", Min: 2},
		{ChannelName: "ch1", Min: 1},
	}
	met, counts := hasMetAllChannelConditions(conds, nil, unconsumed)
	assert.True(t, met)
	assert.Equal(t, []int{2, 1}, counts)

	unconsumed["ch1"] = msgs(1, 2)
	met, counts = hasMetAllChannelConditions(conds, nil, unconsumed)
	assert.False(t, met)
	assert.Nil(t, counts)
}

func TestHasMetAllChannelConditions_AccountsForReservations(t *testing.T) {
	unconsumed := map[string][]p.ChannelMessage{"ch": msgs(1, 2, 3, 4)}
	active := map[string]p.ActiveStepExecution{
		"stepA": reservedStepExe(1, "ch", 2),
	}
	conds := []*p.ChannelCondition{
		{ChannelName: "ch", Min: 1},
		{ChannelName: "ch", Min: 1},
	}
	// 4 total - 2 reserved = 2 available, need 2 → satisfied
	met, counts := hasMetAllChannelConditions(conds, active, unconsumed)
	assert.True(t, met)
	assert.Equal(t, []int{1, 1}, counts)

	// 3 total - 2 reserved = 1 available, need 2 → not satisfied
	unconsumed["ch"] = msgs(1, 2, 3)
	met, counts = hasMetAllChannelConditions(conds, active, unconsumed)
	assert.False(t, met)
	assert.Nil(t, counts)
}

func TestHasMetAllChannelConditions_SingleConditionTakesUpToMax(t *testing.T) {
	unconsumed := map[string][]p.ChannelMessage{"ch1": msgs(1, 2, 3, 4, 5)}
	conds := []*p.ChannelCondition{{ChannelName: "ch1", Min: 1, Max: 3}}
	met, counts := hasMetAllChannelConditions(conds, nil, unconsumed)
	assert.True(t, met)
	assert.Equal(t, []int{3}, counts)
}

func TestHasMetAllChannelConditions_SameChannelDistributesSurplusByMax(t *testing.T) {
	unconsumed := map[string][]p.ChannelMessage{"ch1": msgs(1, 2, 3, 4, 5)}
	conds := []*p.ChannelCondition{
		{ChannelName: "ch1", Min: 1, Max: 3},
		{ChannelName: "ch1", Min: 1, Max: 3},
	}
	met, counts := hasMetAllChannelConditions(conds, nil, unconsumed)
	assert.True(t, met)
	assert.Equal(t, []int{3, 2}, counts)
}

func TestHasMetAllChannelConditions_NilConditionFails(t *testing.T) {
	unconsumed := map[string][]p.ChannelMessage{"ch": msgs(1)}
	conds := []*p.ChannelCondition{{ChannelName: "ch", Min: 1}, nil}
	met, counts := hasMetAllChannelConditions(conds, nil, unconsumed)
	assert.False(t, met)
	assert.Nil(t, counts)
}

// ============================================================================
// SpliceUnconsumed
// ============================================================================

func TestSpliceUnconsumed_ReverseOrder(t *testing.T) {
	unconsumed := map[string][]p.ChannelMessage{"ch": msgs(1, 2, 3)}
	active := map[string]p.ActiveStepExecution{
		"stepA": reservedStepExe(1, "ch", 1),
		"stepB": reservedStepExe(2, "ch", 1),
	}
	SpliceUnconsumed([]string{"stepB"}, active, unconsumed)
	require.Len(t, unconsumed["ch"], 2)
	assert.Equal(t, int64(1), *unconsumed["ch"][0].Value.IntVal)
	assert.Equal(t, int64(3), *unconsumed["ch"][1].Value.IntVal)

	// stepA still reads at offset 0 of the post-splice queue → value 1.
	offset := reservationOffset(getCurrentReservations(active), "ch", 1)
	assert.Equal(t, 0, offset)
	assert.Equal(t, int64(1), *unconsumed["ch"][offset].Value.IntVal)
}
