package pbconv

import (
	"testing"
	"time"

	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
	p "github.com/superdurable/dex/server/internal/persistence"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Persistence -> Pb -> Persistence must be lossless (guards against a converter
// silently dropping a field on either side).
func TestWaitForCondition_RoundTrip_Persistence(t *testing.T) {
	orig := p.WaitForCondition{
		Type: p.WaitTypeAllOf,
		Conditions: []p.SingleCondition{
			{Timer: &p.TimerCondition{FireAtUnixMs: 1234}},
			{Channel: &p.ChannelCondition{ChannelName: "c", Min: 2, Max: 5}},
		},
	}
	back := PbWaitForConditionToPersistence(PersistenceWaitForConditionToPb(&orig))
	assert.Equal(t, orig, back)
}

func TestConditionResults_RoundTrip_Persistence(t *testing.T) {
	orig := []p.ConditionResult{
		{Timer: &p.TimerConditionResult{Fired: true, FireAtUnixMs: 99}},
		{Channel: &p.ChannelConditionResult{ChannelName: "c", Satisfied: true, ConsumedCount: 3}},
	}
	back := PbConditionResultsToPersistence(PersistenceConditionResultsToPb(orig))
	assert.Equal(t, orig, back)
}

// Pb -> Persistence -> Pb covers the other direction's field mapping.
func TestWaitForCondition_RoundTrip_Pb(t *testing.T) {
	orig := &pb.WaitForCondition{
		Type: pb.WaitType_WAIT_TYPE_ANY_OF,
		Conditions: []*pb.SingleCondition{
			{Condition: &pb.SingleCondition_Timer{Timer: &pb.TimerCondition{FireAtUnixMs: 7}}},
			{Condition: &pb.SingleCondition_Channel{Channel: &pb.ChannelCondition{ChannelName: "x", Min: 1, Max: 4}}},
		},
	}
	persisted := PbWaitForConditionToPersistence(orig)
	back := PersistenceWaitForConditionToPb(&persisted)
	require.Len(t, back.Conditions, 2)
	assert.Equal(t, orig.Type, back.Type)
	assert.Equal(t, int64(7), back.Conditions[0].GetTimer().FireAtUnixMs)
	ch := back.Conditions[1].GetChannel()
	require.NotNil(t, ch)
	assert.Equal(t, "x", ch.ChannelName)
	assert.Equal(t, int32(1), ch.Min)
	assert.Equal(t, int32(4), ch.Max)
}

func TestPersistenceRetryStateToPb_AllFields(t *testing.T) {
	rs := &p.RetryState{
		FirstAttemptTime:    time.UnixMilli(5000),
		CurrentAttempts:     3,
		LastError:           "boom",
		LastErrorStackTrace: "stack",
	}
	got := PersistenceRetryStateToPb(rs)
	require.NotNil(t, got)
	assert.Equal(t, int64(5000), got.FirstAttemptTimeMs)
	assert.Equal(t, int32(3), got.CurrentAttempts)
	assert.Equal(t, "boom", got.LastError)
	assert.Equal(t, "stack", got.LastErrorStackTrace)

	assert.Nil(t, PersistenceRetryStateToPb(nil))
}
