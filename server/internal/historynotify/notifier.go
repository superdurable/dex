// Package historynotify provides an in-process pub/sub that wakes
// WaitForHistoryEvent long-poll waiters when the history events they are
// waiting for are inserted for a run.
//
// The producer is the per-shard OpsFIFO batch reader (which inserts history
// rows and hands the batch to NotifyEventsWritten); the consumer is the
// RunsService WaitForHistoryEvent handler. Both run on the shard owner.
package historynotify

import (
	"context"
	"sync"

	"github.com/superdurable/dex/protocol-grpc/gen/dexpb"
	p "github.com/superdurable/dex/server/internal/persistence"
)

type NotifierManager interface {
	NotifyEventsWritten(events []p.HistoryEvent)
	Subscribe(ctx context.Context, req *dexpb.WaitForHistoryEventRequest) (*Subscription, error)
}

type notifierManagerImpl struct {
	// historyStore is read once per Subscribe to seed the run's latest event so
	// an already-satisfied condition (e.g. the run already closed) fires without
	// waiting for a fresh OpsFIFO insert.
	historyStore p.HistoryStore

	mu sync.Mutex
	// Keyed by getKey (namespace + "/" + runID)
	notifierPerRun map[string]*runNotifier
}

var _ NotifierManager = (*notifierManagerImpl)(nil)

type runNotifier struct {
	lastEvent *p.HistoryEvent
	// using a map because different sub can complete at different times
	subs map[*Subscription]struct{}
}

func NewNotifierManager(historyStore p.HistoryStore) NotifierManager {
	if historyStore == nil {
		panic("NewNotifierManager: historyStore must not be nil")
	}
	return &notifierManagerImpl{historyStore: historyStore, notifierPerRun: make(map[string]*runNotifier)}
}

func getKey(namespace, runId string) string {
	return namespace + "/" + runId
}

func (n *notifierManagerImpl) NotifyEventsWritten(events []p.HistoryEvent) {
	n.mu.Lock()
	defer n.mu.Unlock()
	for i := range events {
		event := events[i]
		notifier, ok := n.notifierPerRun[getKey(event.Namespace, event.RunID)]
		if !ok {
			continue
		}
		notifier.applyEventLocked(event)
	}
}

func (n *runNotifier) applyEventLocked(event p.HistoryEvent) {
	if n.lastEvent == nil || event.EventID > n.lastEvent.EventID {
		n.lastEvent = &event
		for sub := range n.subs {
			sub.completeChanIfConditionMetLocked()
		}
		return
	}
	return
}

func (n *notifierManagerImpl) Subscribe(ctx context.Context, req *dexpb.WaitForHistoryEventRequest) (*Subscription, error) {
	// Register BEFORE the authoritative read so a concurrent NotifyEventsWritten
	// during the read still wakes this subscription — nothing is missed.
	sub := n.register(req)

	event, err := n.historyStore.GetLatestEvent(ctx, req.GetNamespace(), req.GetRunId())
	if err != nil {
		sub.Close()
		return nil, err
	}
	// Seed the run's latest event (nil when the run has no history yet); this
	// fires the subscription immediately if the condition is already satisfied.
	if event != nil {
		n.NotifyEventsWritten([]p.HistoryEvent{*event})
	}
	return sub, nil
}

func (n *notifierManagerImpl) register(req *dexpb.WaitForHistoryEventRequest) *Subscription {
	key := getKey(req.GetNamespace(), req.GetRunId())
	n.mu.Lock()
	defer n.mu.Unlock()

	notifier, ok := n.notifierPerRun[key]
	if !ok {
		notifier = &runNotifier{subs: make(map[*Subscription]struct{})}
		n.notifierPerRun[key] = notifier
	}

	sub := &Subscription{
		cond:     newWaitCondition(req),
		resultCh: make(chan Result, 1),

		notifierManager: n,
		namespace:       req.Namespace,
		runId:           req.RunId,
		notifier:        notifier,
	}

	notifier.subs[sub] = struct{}{}
	sub.completeChanIfConditionMetLocked()
	return sub
}

func newWaitCondition(req *dexpb.WaitForHistoryEventRequest) *waitCondition {
	if id := req.GetUntilEventId(); id > 0 {
		return &waitCondition{untilEventId: id}
	}
	return &waitCondition{untilRunStop: true}
}

type waitCondition struct {
	untilEventId int64
	untilRunStop bool
}

type Result struct {
	EventID   int64
	RunStatus p.RunStatus
}

// Subscription is a single waiter's handle.
type Subscription struct {
	cond         *waitCondition
	resultCh     chan Result
	conditionMet bool

	// below are for clean up logic

	notifierManager *notifierManagerImpl
	namespace       string
	runId           string
	notifier        *runNotifier
}

// WaitUntilConditionMet returns a channel that delivers the Result and is closed
// exactly when this subscription's Condition is satisfied. Once closed the
// waitCondition holds (state is monotonic), so the caller returns without
// re-checking.
func (s *Subscription) WaitUntilConditionMet() <-chan Result {
	return s.resultCh
}

// Close releases the subscription, dropping the run's runNotifier once the last waiter
// leaves so the map does not grow unbounded.
func (s *Subscription) Close() {
	s.notifierManager.mu.Lock()
	defer s.notifierManager.mu.Unlock()
	delete(s.notifier.subs, s)
	if len(s.notifier.subs) == 0 {
		delete(s.notifierManager.notifierPerRun, getKey(s.namespace, s.runId))
	}
}

func (s *Subscription) completeChanIfConditionMetLocked() {
	last := s.notifier.lastEvent
	if last == nil {
		return
	}
	met := false
	switch {
	case last.Payload.RunStop != nil:
		// A closed run satisfies both wait types: an awaited event that has not
		// arrived by the terminal event never will.
		met = true
	case s.cond.untilEventId > 0 && last.EventID >= s.cond.untilEventId:
		met = true
	}
	if !met {
		return
	}
	if !s.conditionMet {
		s.conditionMet = true
		s.resultCh <- Result{EventID: last.EventID, RunStatus: runStatusOf(last)}
		close(s.resultCh)
	}
}

// runStatusOf returns the terminal run status of a RunStop event, or
// RunStatusInvalid (-1) for any non-terminal event. Zero is RunStatusPending, a
// real status, so it cannot double as "no status".
func runStatusOf(event *p.HistoryEvent) p.RunStatus {
	if event.Payload.RunStop != nil {
		return p.RunStatus(event.Payload.RunStop.GetRunStatus())
	}
	return p.RunStatusInvalid
}
