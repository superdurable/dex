package historynotify

import (
	"context"
	"testing"

	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
	commonerrors "github.com/superdurable/dex/server/common/errors"
	p "github.com/superdurable/dex/server/internal/persistence"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeHistoryStore lets a test seed the run's latest event that Subscribe reads.
// Only GetLatestEvent is exercised; the other methods panic if called.
type fakeHistoryStore struct {
	latest *p.HistoryEvent
}

func (f *fakeHistoryStore) GetLatestEvent(context.Context, string, string) (*p.HistoryEvent, commonerrors.CategorizedError) {
	return f.latest, nil
}
func (f *fakeHistoryStore) BatchInsertHistory(context.Context, []p.HistoryEvent) commonerrors.CategorizedError {
	panic("unused")
}
func (f *fakeHistoryStore) GetHistoryEvents(context.Context, string, string, int64, int) ([]p.HistoryEvent, commonerrors.CategorizedError) {
	panic("unused")
}
func (f *fakeHistoryStore) DeleteAll(context.Context) error { return nil }
func (f *fakeHistoryStore) Close() error                    { return nil }

// ev is a plain (non-terminal) history event for (ns, runID) with the given id.
func ev(ns, runID string, id int64) p.HistoryEvent {
	return p.HistoryEvent{Namespace: ns, RunID: runID, EventID: id}
}

// stopEv is a terminal RunStop history event carrying the run status.
func stopEv(ns, runID string, id int64, status p.RunStatus) p.HistoryEvent {
	return p.HistoryEvent{
		Namespace: ns, RunID: runID, EventID: id,
		Payload: p.HistoryEventPayload{RunStop: &pb.HistoryRunStopPayload{RunStatus: int32(status)}},
	}
}

func reqUntilID(ns, runID string, id int64) *pb.WaitForHistoryEventRequest {
	return &pb.WaitForHistoryEventRequest{
		Namespace: ns, RunId: runID,
		Condition: &pb.WaitForHistoryEventRequest_UntilEventId{UntilEventId: id},
	}
}

func reqRunStop(ns, runID string) *pb.WaitForHistoryEventRequest {
	return &pb.WaitForHistoryEventRequest{
		Namespace: ns, RunId: runID,
		Condition: &pb.WaitForHistoryEventRequest_UntilRunStop{UntilRunStop: true},
	}
}

// readMet non-blockingly reports whether the subscription's condition is met and
// the delivered Result. All notifier state changes deliver + close the channel
// synchronously under the lock, so this is deterministic after Notify/Subscribe.
func readMet(sub *Subscription) (Result, bool) {
	select {
	case res := <-sub.WaitUntilConditionMet():
		return res, true
	default:
		return Result{}, false
	}
}

// newManager builds a manager whose store reports no history, so tests drive
// state purely via NotifyEventsWritten.
func newManager(t *testing.T) *notifierManagerImpl {
	t.Helper()
	return NewNotifierManager(&fakeHistoryStore{}).(*notifierManagerImpl)
}

// subscribe registers a waiter and fails the test on a store-read error.
func subscribe(t *testing.T, n *notifierManagerImpl, req *pb.WaitForHistoryEventRequest) *Subscription {
	t.Helper()
	sub, err := n.Subscribe(context.Background(), req)
	require.NoError(t, err)
	return sub
}

func TestNotifier_ByID_MetByTip(t *testing.T) {
	n := newManager(t)
	sub := subscribe(t, n, reqUntilID("ns", "run", 5))
	defer sub.Close()
	_, ok := readMet(sub)
	assert.False(t, ok)

	n.NotifyEventsWritten([]p.HistoryEvent{ev("ns", "run", 3)})
	_, ok = readMet(sub)
	assert.False(t, ok, "tip 3 < expected 5")

	n.NotifyEventsWritten([]p.HistoryEvent{ev("ns", "run", 5)})
	res, ok := readMet(sub)
	assert.True(t, ok)
	assert.Equal(t, int64(5), res.EventID)
	assert.Equal(t, p.RunStatusInvalid, res.RunStatus, "non-terminal event carries no status")
}

// TestNotifier_LateJoinerWakesOnExistingTip guards Subscribe's register-time
// condition check. A waiter joining a run whose tip already satisfies it must
// wake at Subscribe: the store read here is nil, so the trailing fold does
// nothing, and even a non-nil read that is not strictly newer than the existing
// tip is a no-op in applyEventLocked. Only register's immediate check catches it.
func TestNotifier_LateJoinerWakesOnExistingTip(t *testing.T) {
	n := newManager(t)
	early := subscribe(t, n, reqUntilID("ns", "run", 10))
	defer early.Close()
	n.NotifyEventsWritten([]p.HistoryEvent{ev("ns", "run", 5)}) // tip 5, still < 10
	_, ok := readMet(early)
	require.False(t, ok)

	late := subscribe(t, n, reqUntilID("ns", "run", 3)) // 3 <= existing tip 5
	defer late.Close()
	res, ok := readMet(late)
	require.True(t, ok, "late joiner must see the existing tip at Subscribe")
	assert.Equal(t, int64(5), res.EventID)
}

func TestNotifier_ByID_MetByCloseBeforeTip(t *testing.T) {
	n := newManager(t)
	sub := subscribe(t, n, reqUntilID("ns", "run", 10))
	defer sub.Close()

	// A RunStop at id 3 (< expected 10) still satisfies via the closed run, and
	// carries the terminal status.
	n.NotifyEventsWritten([]p.HistoryEvent{stopEv("ns", "run", 3, p.RunStatusCompleted)})
	res, ok := readMet(sub)
	assert.True(t, ok)
	assert.Equal(t, int64(3), res.EventID)
	assert.Equal(t, p.RunStatusCompleted, res.RunStatus)
}

func TestNotifier_RunStop_IgnoresAdvanceWakesOnClose(t *testing.T) {
	n := newManager(t)
	sub := subscribe(t, n, reqRunStop("ns", "run"))
	defer sub.Close()

	// A non-terminal advance must NOT wake a run_stop waiter.
	n.NotifyEventsWritten([]p.HistoryEvent{ev("ns", "run", 5)})
	_, ok := readMet(sub)
	assert.False(t, ok)

	n.NotifyEventsWritten([]p.HistoryEvent{stopEv("ns", "run", 6, p.RunStatusFailed)})
	res, ok := readMet(sub)
	assert.True(t, ok)
	assert.Equal(t, int64(6), res.EventID)
	assert.Equal(t, p.RunStatusFailed, res.RunStatus)
}

func TestNotifier_SubscribeSeedsFromStore(t *testing.T) {
	// The store already has the run's terminal event; Subscribe seeds it and the
	// condition is satisfied immediately, without any NotifyEventsWritten.
	stop := stopEv("ns", "run", 7, p.RunStatusCompleted)
	n := NewNotifierManager(&fakeHistoryStore{latest: &stop}).(*notifierManagerImpl)
	sub, err := n.Subscribe(context.Background(), reqRunStop("ns", "run"))
	require.NoError(t, err)
	defer sub.Close()

	res, ok := readMet(sub)
	assert.True(t, ok)
	assert.Equal(t, int64(7), res.EventID)
	assert.Equal(t, p.RunStatusCompleted, res.RunStatus)
}

func TestNotifier_AlreadyClosedSatisfiesAtSubscribe(t *testing.T) {
	n := newManager(t)
	keepAlive := subscribe(t, n, reqRunStop("ns", "run"))
	defer keepAlive.Close()
	n.NotifyEventsWritten([]p.HistoryEvent{stopEv("ns", "run", 1, p.RunStatusCompleted)})
	_, ok := readMet(keepAlive)
	require.True(t, ok)

	// A fresh subscriber on the still-closed run is satisfied at Subscribe.
	sub := subscribe(t, n, reqUntilID("ns", "run", 99))
	defer sub.Close()
	res, ok := readMet(sub)
	assert.True(t, ok)
	assert.Equal(t, int64(1), res.EventID)
	assert.Equal(t, p.RunStatusCompleted, res.RunStatus)
}

func TestNotifier_BatchSpansMultipleRuns(t *testing.T) {
	n := newManager(t)
	a := subscribe(t, n, reqUntilID("ns", "runA", 2))
	defer a.Close()
	b := subscribe(t, n, reqRunStop("ns", "runB"))
	defer b.Close()
	// runC has no waiter — must be skipped without panicking.

	n.NotifyEventsWritten([]p.HistoryEvent{
		ev("ns", "runA", 1),
		ev("ns", "runA", 2),
		stopEv("ns", "runB", 1, p.RunStatusCompleted),
		ev("ns", "runC", 9),
	})

	resA, okA := readMet(a)
	assert.True(t, okA)
	assert.Equal(t, int64(2), resA.EventID)
	resB, okB := readMet(b)
	assert.True(t, okB)
	assert.Equal(t, p.RunStatusCompleted, resB.RunStatus)
}

func TestNotifier_NotifyWithoutSubscriberIsNoop(t *testing.T) {
	n := newManager(t)
	n.NotifyEventsWritten([]p.HistoryEvent{stopEv("ns", "run", 5, p.RunStatusCompleted)})

	sub := subscribe(t, n, reqUntilID("ns", "run", 1))
	defer sub.Close()
	// The earlier signal was not retained (no entry existed) and the store
	// reports no history, so a fresh subscriber starts unsatisfied.
	_, ok := readMet(sub)
	assert.False(t, ok)
}

func TestNotifier_EntryDroppedWhenLastWaiterLeaves(t *testing.T) {
	n := newManager(t)
	sub1 := subscribe(t, n, reqUntilID("ns", "run", 5))
	sub2 := subscribe(t, n, reqUntilID("ns", "run", 5))

	sub1.Close()
	require.Len(t, n.notifierPerRun, 1, "entry retained while a waiter remains")

	sub2.Close()
	assert.Empty(t, n.notifierPerRun, "entry dropped once the last waiter leaves")
}

func TestNotifier_KeysAreIsolatedByNamespaceAndRun(t *testing.T) {
	n := newManager(t)
	a := subscribe(t, n, reqUntilID("ns1", "run", 9))
	defer a.Close()
	b := subscribe(t, n, reqUntilID("ns2", "run", 9))
	defer b.Close()

	n.NotifyEventsWritten([]p.HistoryEvent{ev("ns1", "run", 9)})
	_, okA := readMet(a)
	assert.True(t, okA)
	_, okB := readMet(b)
	assert.False(t, okB)
}
