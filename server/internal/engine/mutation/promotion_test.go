package mutation

import (
	"testing"

	"github.com/superdurable/dex/server/common/utils/ptr"
	p "github.com/superdurable/dex/server/internal/persistence"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func waitingOnChannel(channel string, min int32) p.ActiveStepExecution {
	return p.ActiveStepExecution{
		Status: p.StepExeStatusWaitingForCondition,
		WaitForCondition: &p.WaitForCondition{
			Type:       p.WaitTypeAnyOf,
			Conditions: []p.SingleCondition{{Channel: &p.ChannelCondition{ChannelName: channel, Min: min}}},
		},
	}
}

// Regression: competing waiting steps on one channel must not all promote when
// there are fewer messages than steps. Each promotion's reservation must be fed
// back so the next step sees available = queue - reserved (symmetric with SDK).
func TestPromoteAllSatisfiedWaitingSteps_CompetingStepsBoundedByMessages(t *testing.T) {
	activeSteps := map[string]p.ActiveStepExecution{
		"s-1": waitingOnChannel("c", 1),
		"s-2": waitingOnChannel("c", 1),
		"s-3": waitingOnChannel("c", 1),
	}
	unconsumed := map[string][]p.ChannelMessage{
		"c": {{ID: 1, Value: p.Value{Type: p.ValueTypeInt, IntVal: ptr.Any(int64(10))}}},
	}
	update := &p.RunRowUpdate{
		ActiveStepExecutions: map[string]*p.ActiveStepExecution{},
		StepMethodExeCounter: ptr.Any(int64(0)),
	}

	promoted, ok := promoteAllSatisfiedWaitingSteps(update, activeSteps, unconsumed, 0)

	require.True(t, ok)
	// Only ONE step promoted — one message, one winner.
	require.Len(t, promoted, 1)
	assert.Equal(t, "s-1", promoted[0].stepExeID, "lowest sorted stepExeID wins")
	require.Len(t, update.ActiveStepExecutions, 1)

	winner := update.ActiveStepExecutions["s-1"]
	require.NotNil(t, winner)
	assert.Equal(t, p.StepExeStatusInvokingExecute, winner.Status)
	assert.Equal(t, int64(1), winner.ExecuteMethodExeID)
	require.Len(t, winner.ConditionResults, 1)
	assert.Equal(t, int32(1), winner.ConditionResults[0].Channel.ConsumedCount)
}

// With enough messages, all competing steps promote (each reserves its own).
func TestPromoteAllSatisfiedWaitingSteps_AllPromoteWhenEnoughMessages(t *testing.T) {
	activeSteps := map[string]p.ActiveStepExecution{
		"s-1": waitingOnChannel("c", 1),
		"s-2": waitingOnChannel("c", 1),
	}
	unconsumed := map[string][]p.ChannelMessage{
		"c": {
			{ID: 1, Value: p.Value{Type: p.ValueTypeInt, IntVal: ptr.Any(int64(10))}},
			{ID: 2, Value: p.Value{Type: p.ValueTypeInt, IntVal: ptr.Any(int64(20))}},
		},
	}
	update := &p.RunRowUpdate{
		ActiveStepExecutions: map[string]*p.ActiveStepExecution{},
		StepMethodExeCounter: ptr.Any(int64(0)),
	}

	promoted, ok := promoteAllSatisfiedWaitingSteps(update, activeSteps, unconsumed, 0)

	require.True(t, ok)
	require.Len(t, promoted, 2)
	// Distinct, monotonically-allocated exeIDs in sorted order.
	assert.Equal(t, int64(1), update.ActiveStepExecutions["s-1"].ExecuteMethodExeID)
	assert.Equal(t, int64(2), update.ActiveStepExecutions["s-2"].ExecuteMethodExeID)
}
