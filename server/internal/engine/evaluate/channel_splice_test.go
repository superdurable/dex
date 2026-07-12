package evaluate

import (
	"testing"

	p "github.com/superdurable/dex/server/internal/persistence"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func intChannelMsg(id int64, v int64) p.ChannelMessage {
	return p.ChannelMessage{ID: id, Value: p.Value{Type: p.ValueTypeInt, IntVal: &v}}
}

func reservedStep(stepExeID string, executeID int64, channel string, count int32) p.ActiveStepExecution {
	return p.ActiveStepExecution{
		Status:             p.StepExeStatusInvokingExecute,
		ExecuteMethodExeID: executeID,
		ConditionResults: []p.ConditionResult{{
			Channel: &p.ChannelConditionResult{
				ChannelName: channel, Satisfied: true, ConsumedCount: count,
			},
		}},
	}
}

func cloneQueue(q map[string][]p.ChannelMessage) map[string][]p.ChannelMessage {
	out := make(map[string][]p.ChannelMessage, len(q))
	for ch, msgs := range q {
		clone := make([]p.ChannelMessage, len(msgs))
		copy(clone, msgs)
		out[ch] = clone
	}
	return out
}

func TestSpliceReservedMessages_ReverseCompleteOrder(t *testing.T) {
	v1, v2, v3 := int64(1), int64(2), int64(3)
	queue := map[string][]p.ChannelMessage{
		"ch": {
			intChannelMsg(1, v1),
			intChannelMsg(2, v2),
			intChannelMsg(3, v3),
		},
	}
	active := map[string]p.ActiveStepExecution{
		"stepA": reservedStep("stepA", 1, "ch", 1),
		"stepB": reservedStep("stepB", 2, "ch", 1),
	}

	// stepB (middle) completes first.
	q := cloneQueue(queue)
	SpliceUnconsumed([]string{"stepB"}, active, q)
	require.Len(t, q["ch"], 2)
	assert.Equal(t, v1, *q["ch"][0].Value.IntVal)
	assert.Equal(t, v3, *q["ch"][1].Value.IntVal)

	// stepA still reads at offset 0 (value 1) of the post-splice queue.
	offset := reservationOffset(getCurrentReservations(active), "ch", 1)
	assert.Equal(t, 0, offset)
	assert.Equal(t, v1, *q["ch"][offset].Value.IntVal)
}

func TestSpliceReservedMessages_BatchDescendingExeID(t *testing.T) {
	v1, v2, v3 := int64(10), int64(20), int64(30)
	queue := map[string][]p.ChannelMessage{
		"ch": {intChannelMsg(1, v1), intChannelMsg(2, v2), intChannelMsg(3, v3)},
	}
	active := map[string]p.ActiveStepExecution{
		"low":  reservedStep("low", 1, "ch", 1),
		"high": reservedStep("high", 2, "ch", 1),
	}

	qA := cloneQueue(queue)
	SpliceUnconsumed([]string{"low", "high"}, active, qA)
	qB := cloneQueue(queue)
	SpliceUnconsumed([]string{"high", "low"}, active, qB)

	require.Equal(t, len(qA["ch"]), len(qB["ch"]))
	assert.Equal(t, *qA["ch"][0].Value.IntVal, *qB["ch"][0].Value.IntVal)
	require.Len(t, qA["ch"], 1)
	assert.Equal(t, v3, *qA["ch"][0].Value.IntVal)
}

func TestSpliceReservedMessages_CancelReservedInvokingExecute(t *testing.T) {
	v1, v2, v3 := int64(1), int64(2), int64(3)
	queue := map[string][]p.ChannelMessage{
		"ch": {intChannelMsg(1, v1), intChannelMsg(2, v2), intChannelMsg(3, v3)},
	}
	active := map[string]p.ActiveStepExecution{
		"cancel":   reservedStep("cancel", 1, "ch", 1),
		"survivor": reservedStep("survivor", 2, "ch", 1),
	}
	q := cloneQueue(queue)
	SpliceUnconsumed([]string{"cancel"}, active, q)
	require.Len(t, q["ch"], 2)
	assert.Equal(t, v2, *q["ch"][0].Value.IntVal)

	// Caller removes spliced steps from active before querying survivors;
	// see worker_run_executor.go handleExecuteCompletion (splice then delete).
	delete(active, "cancel")

	// survivor's offset recomputes to 0 (cancel gone) → reads value 2.
	offset := reservationOffset(getCurrentReservations(active), "ch", 2)
	assert.Equal(t, 0, offset)
	assert.Equal(t, v2, *q["ch"][offset].Value.IntVal)
}

// Regression: a step reserving on MULTIPLE channels must not corrupt the
// offsets of higher-exeID steps on a shared channel when spliced together.
// The former bug deleted the multi-channel step from the shared reservation
// table mid-splice; it was Go-map-iteration-order dependent, so we iterate.
func TestSpliceUnconsumed_MultiChannelStep_KeepsSurvivorAligned(t *testing.T) {
	for iter := 0; iter < 200; iter++ {
		active := map[string]p.ActiveStepExecution{
			// X reserves channel "a" (1) AND channel "b" (1); lowest exeID.
			"X": {
				Status:             p.StepExeStatusInvokingExecute,
				ExecuteMethodExeID: 1,
				ConditionResults: []p.ConditionResult{
					{Channel: &p.ChannelConditionResult{ChannelName: "a", Satisfied: true, ConsumedCount: 1}},
					{Channel: &p.ChannelConditionResult{ChannelName: "b", Satisfied: true, ConsumedCount: 1}},
				},
			},
			// Z reserves "b" (1); survives (NOT removed).
			"Z": reservedStep("Z", 2, "b", 1),
			// Y reserves "b" (1); removed alongside X.
			"Y": reservedStep("Y", 3, "b", 1),
		}
		queue := map[string][]p.ChannelMessage{
			"a": {intChannelMsg(100, 100)},
			"b": {intChannelMsg(10, 10), intChannelMsg(11, 11), intChannelMsg(12, 12)},
		}
		SpliceUnconsumed([]string{"X", "Y"}, active, queue)

		// X consumed a[0]; X consumed b[0]; Y consumed b[2]; Z keeps b[1].
		assert.Empty(t, queue["a"], "iter %d", iter)
		require.Len(t, queue["b"], 1, "iter %d", iter)
		assert.Equal(t, int64(11), *queue["b"][0].Value.IntVal, "iter %d: survivor Z must keep b[1]", iter)
	}
}

// Regression: a duplicated / self-referential stepExeID in removeStepExeIDs
// must splice its reserved range exactly once (not once per occurrence), else
// it over-consumes the channel and corrupts a surviving sibling's messages.
func TestSpliceUnconsumed_DuplicateStepExeID_SplicesOnce(t *testing.T) {
	active := map[string]p.ActiveStepExecution{
		"self":     reservedStep("self", 1, "ch", 1),
		"survivor": reservedStep("survivor", 2, "ch", 1),
	}
	queue := map[string][]p.ChannelMessage{
		"ch": {intChannelMsg(1, 10), intChannelMsg(2, 20), intChannelMsg(3, 30)},
	}
	// "self" appears twice (mirrors a worker putting StepExeId into
	// CanceledStepExecutions). Only its single reserved message (index 0) is removed.
	SpliceUnconsumed([]string{"self", "self"}, active, queue)

	require.Len(t, queue["ch"], 2)
	assert.Equal(t, int64(20), *queue["ch"][0].Value.IntVal, "survivor's message must be preserved")
	assert.Equal(t, int64(30), *queue["ch"][1].Value.IntVal)
}

func TestSpliceReservedMessages_TimerOnlyNoSlice(t *testing.T) {
	queue := map[string][]p.ChannelMessage{
		"ch": {intChannelMsg(1, 1)},
	}
	active := map[string]p.ActiveStepExecution{
		"t": {
			Status:             p.StepExeStatusInvokingExecute,
			ExecuteMethodExeID: 1,
			ConditionResults: []p.ConditionResult{{
				Timer: &p.TimerConditionResult{Fired: true, FireAtUnixMs: 1},
			}},
		},
	}
	q := cloneQueue(queue)
	SpliceUnconsumed([]string{"t"}, active, q)
	require.Len(t, q["ch"], 1)
}
