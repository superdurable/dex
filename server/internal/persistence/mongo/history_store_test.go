package mongo

import (
	"context"
	"os"
	"testing"

	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
	p "github.com/superdurable/dex/server/internal/persistence"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runStartEvent / runEndEvent / channelPublishEvent are short test factories
// for typed history events. Each returns a HistoryEvent with exactly one
// payload variant set so the store's payload validator is happy.
func runStartEvent(runID string, eventID int64, occurredMs int64, flowType string) p.HistoryEvent {
	return p.HistoryEvent{
		Namespace: "ns", RunID: runID, EventID: eventID, OccurredAtMs: occurredMs,
		Payload: p.HistoryEventPayload{
			RunStart: &pb.HistoryRunStartPayload{FlowType: flowType, TaskListName: "g"},
		},
	}
}

func stepExecCompletedEvent(runID string, eventID int64, occurredMs int64, stepExeID string) p.HistoryEvent {
	return p.HistoryEvent{
		Namespace: "ns", RunID: runID, EventID: eventID, OccurredAtMs: occurredMs,
		Payload: p.HistoryEventPayload{
			StepExecuteCompleted: &pb.HistoryStepExecuteCompletedPayload{
				StepExeId:            stepExeID,
				WorkerRequestCounter: 1,
			},
		},
	}
}

func runEndEvent(runID string, eventID int64, occurredMs int64, status p.RunStatus) p.HistoryEvent {
	return p.HistoryEvent{
		Namespace: "ns", RunID: runID, EventID: eventID, OccurredAtMs: occurredMs,
		Payload: p.HistoryEventPayload{
			RunStop: &pb.HistoryRunStopPayload{RunStatus: int32(status)},
		},
	}
}

func channelPublishEvent(runID string, eventID int64, occurredMs int64, channel string) p.HistoryEvent {
	return p.HistoryEvent{
		Namespace: "ns", RunID: runID, EventID: eventID, OccurredAtMs: occurredMs,
		Payload: p.HistoryEventPayload{
			ChannelPublish: &pb.HistoryChannelPublishPayload{ChannelName: channel},
		},
	}
}

// TestHistoryStore_BatchInsertHistory_Idempotent verifies that replaying the
// same batch (same run_id, same event_id) is silently ignored thanks to the
// duplicate-key handling in the InsertMany ordered=false path. This is the
// correctness requirement that lets the OpsFIFO retry-the-whole-batch model
// work without producing spurious failures.
func TestHistoryStore_BatchInsertHistory_Idempotent(t *testing.T) {
	uri := os.Getenv("DEX_TEST_MONGO_URI")
	if uri == "" {
		t.Skip("DEX_TEST_MONGO_URI not set; skipping integration test")
	}
	ctx := context.Background()

	store, err := NewHistoryStoreWithDatabase(ctx, uri, testDBName, DefaultOperationTimeouts())
	require.Nil(t, err)
	defer store.Close()
	require.NoError(t, store.DeleteAll(ctx))

	runID := uuid.NewString()
	events := []p.HistoryEvent{
		runStartEvent(runID, 1, 100, "ft"),
		stepExecCompletedEvent(runID, 2, 200, "s1-1"),
		runEndEvent(runID, 3, 300, p.RunStatusCompleted),
	}
	require.Nil(t, store.BatchInsertHistory(ctx, events))

	// Replay the SAME batch — every row is a duplicate, must succeed.
	require.Nil(t, store.BatchInsertHistory(ctx, events))

	// Mixed batch: 2 duplicates + 1 new (event_id=4) must also succeed.
	mixed := []p.HistoryEvent{events[1], events[2], channelPublishEvent(runID, 4, 400, "events")}
	require.Nil(t, store.BatchInsertHistory(ctx, mixed))

	got, getErr := store.GetHistoryEvents(ctx, "ns", runID, 0, 100)
	require.Nil(t, getErr)
	require.Len(t, got, 4)
	for i, ev := range got {
		assert.Equal(t, int64(i+1), ev.EventID)
	}
	// Round-trip: the first event's payload variant decodes back to RunStart
	// with the same flow_type. This is the smoke test for the marshal/unmarshal
	// path — if the discriminator routing is broken, this fails.
	require.NotNil(t, got[0].Payload.RunStart, "event 1 must round-trip as RunStart")
	assert.Equal(t, "ft", got[0].Payload.RunStart.FlowType)
	require.NotNil(t, got[1].Payload.StepExecuteCompleted)
	assert.Equal(t, "s1-1", got[1].Payload.StepExecuteCompleted.StepExeId)
	require.NotNil(t, got[2].Payload.RunStop)
	assert.Equal(t, int32(p.RunStatusCompleted), got[2].Payload.RunStop.RunStatus)
	require.NotNil(t, got[3].Payload.ChannelPublish)
	assert.Equal(t, "events", got[3].Payload.ChannelPublish.ChannelName)
}

// TestHistoryStore_GetHistoryEvents_OrderingAndPagination exercises the
// (afterID, limit) cursor and confirms ASC ordering by event_id.
func TestHistoryStore_GetHistoryEvents_OrderingAndPagination(t *testing.T) {
	uri := os.Getenv("DEX_TEST_MONGO_URI")
	if uri == "" {
		t.Skip("DEX_TEST_MONGO_URI not set; skipping integration test")
	}
	ctx := context.Background()

	store, err := NewHistoryStoreWithDatabase(ctx, uri, testDBName, DefaultOperationTimeouts())
	require.Nil(t, err)
	defer store.Close()
	require.NoError(t, store.DeleteAll(ctx))

	runID := uuid.NewString()
	const total = 7
	events := make([]p.HistoryEvent, total)
	for i := 0; i < total; i++ {
		events[i] = stepExecCompletedEvent(runID, int64(i+1), int64((i+1)*100), "step")
	}
	// Insert out of order to confirm the store sorts on read.
	shuffled := []p.HistoryEvent{events[3], events[0], events[6], events[1], events[5], events[2], events[4]}
	require.Nil(t, store.BatchInsertHistory(ctx, shuffled))

	// First page: afterID=0, limit=3 -> [1,2,3].
	page1, err1 := store.GetHistoryEvents(ctx, "ns", runID, 0, 3)
	require.Nil(t, err1)
	require.Len(t, page1, 3)
	for i, ev := range page1 {
		assert.Equal(t, int64(i+1), ev.EventID)
	}

	// Second page: afterID=last of page1 -> [4,5,6].
	page2, err2 := store.GetHistoryEvents(ctx, "ns", runID, page1[len(page1)-1].EventID, 3)
	require.Nil(t, err2)
	require.Len(t, page2, 3)
	for i, ev := range page2 {
		assert.Equal(t, int64(i+4), ev.EventID)
	}

	// Final page: afterID=last of page2 -> [7].
	page3, err3 := store.GetHistoryEvents(ctx, "ns", runID, page2[len(page2)-1].EventID, 3)
	require.Nil(t, err3)
	require.Len(t, page3, 1)
	assert.Equal(t, int64(7), page3[0].EventID)

	// Beyond the end: empty result, no error.
	page4, err4 := store.GetHistoryEvents(ctx, "ns", runID, page3[0].EventID, 3)
	require.Nil(t, err4)
	assert.Empty(t, page4)
}

// TestHistoryStore_GetHistoryEvents_LimitClamp confirms that a request with
// limit > maxGetHistoryEventsLimit returns at most maxGetHistoryEventsLimit
// rows (and that limit<=0 is also clamped up to that cap, not returning 0
// rows on a stupid input).
func TestHistoryStore_GetHistoryEvents_LimitClamp(t *testing.T) {
	uri := os.Getenv("DEX_TEST_MONGO_URI")
	if uri == "" {
		t.Skip("DEX_TEST_MONGO_URI not set; skipping integration test")
	}
	ctx := context.Background()

	store, err := NewHistoryStoreWithDatabase(ctx, uri, testDBName, DefaultOperationTimeouts())
	require.Nil(t, err)
	defer store.Close()
	require.NoError(t, store.DeleteAll(ctx))

	runID := uuid.NewString()
	const total = 5
	events := make([]p.HistoryEvent, total)
	for i := 0; i < total; i++ {
		events[i] = stepExecCompletedEvent(runID, int64(i+1), 0, "step")
	}
	require.Nil(t, store.BatchInsertHistory(ctx, events))

	// limit=0 -> clamped to maxGetHistoryEventsLimit, but the dataset is
	// only 5 rows, so we should get all 5.
	got, gErr := store.GetHistoryEvents(ctx, "ns", runID, 0, 0)
	require.Nil(t, gErr)
	assert.Len(t, got, total)

	// limit > cap -> clamped to cap, but still <= dataset size.
	got, gErr = store.GetHistoryEvents(ctx, "ns", runID, 0, maxGetHistoryEventsLimit+10000)
	require.Nil(t, gErr)
	assert.Len(t, got, total)
}

// TestHistoryStore_GetHistoryEvents_RequiresRunID — defensive guard.
func TestHistoryStore_GetHistoryEvents_RequiresRunID(t *testing.T) {
	uri := os.Getenv("DEX_TEST_MONGO_URI")
	if uri == "" {
		t.Skip("DEX_TEST_MONGO_URI not set; skipping integration test")
	}
	store, err := NewHistoryStoreWithDatabase(context.Background(), uri, testDBName, DefaultOperationTimeouts())
	require.Nil(t, err)
	defer store.Close()

	_, gErr := store.GetHistoryEvents(context.Background(), "ns", "", 0, 10)
	require.NotNil(t, gErr)
	assert.True(t, gErr.IsInvalidInputError())
}

func TestHistoryStore_BatchInsertHistory_EmptyIsNoop(t *testing.T) {
	uri := os.Getenv("DEX_TEST_MONGO_URI")
	if uri == "" {
		t.Skip("DEX_TEST_MONGO_URI not set; skipping integration test")
	}
	store, err := NewHistoryStoreWithDatabase(context.Background(), uri, testDBName, DefaultOperationTimeouts())
	require.Nil(t, err)
	defer store.Close()
	assert.Nil(t, store.BatchInsertHistory(context.Background(), nil))
}

// TestHistoryEventPayload_Validate covers the sum-type invariant: exactly
// one variant must be set. This is the contract the mongo store relies on
// to choose a single discriminator + marshal exactly one proto message.
func TestHistoryEventPayload_Validate(t *testing.T) {
	cases := []struct {
		name    string
		payload p.HistoryEventPayload
		wantOK  bool
	}{
		{"empty", p.HistoryEventPayload{}, false},
		{"single RunStart", p.HistoryEventPayload{RunStart: &pb.HistoryRunStartPayload{}}, true},
		{"single RunStop", p.HistoryEventPayload{RunStop: &pb.HistoryRunStopPayload{}}, true},
		{"single RunFork", p.HistoryEventPayload{RunFork: &pb.HistoryRunForkPayload{}}, true},
		{"two variants", p.HistoryEventPayload{
			RunStart: &pb.HistoryRunStartPayload{},
			RunStop:  &pb.HistoryRunStopPayload{},
		}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.payload.Validate()
			if tc.wantOK {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err, "expected Validate to reject %s", tc.name)
			}
		})
	}
}
