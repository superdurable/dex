// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package interpreter

import (
	"encoding/json"
	"time"

	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/service/common/index"
	"github.com/superdurable/dex/service/interpreter/cont"
	"github.com/superdurable/dex/service/interpreter/interfaces"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type IndexSynchronizer struct {
	activities           *Activities
	provider             interfaces.WorkflowProvider
	ctx                  interfaces.UnifiedContext
	continueAsNewCounter *cont.ContinueAsNewCounter
	flowID               string
	runID                string
	flowType             string
	runStartedAt         *timestamppb.Timestamp
	projection           map[string]*dexpb.Value
	pending              []*dexpb.FlowIndexMutation
	inFlight             *dexpb.FlowIndexMutation
	nextSequence         int64
	flushAndClose        bool
	stopped              bool
	err                  error
}

func NewIndexSynchronizer(
	activities *Activities,
	provider interfaces.WorkflowProvider,
	ctx interfaces.UnifiedContext,
	continueAsNewCounter *cont.ContinueAsNewCounter,
	flowType string,
	projection map[string]*dexpb.Value,
	pending []*dexpb.FlowIndexMutation,
	nextSequence int64,
) *IndexSynchronizer {
	if activities == nil || provider == nil || continueAsNewCounter == nil {
		panic("IndexSynchronizer requires non-nil dependencies")
	}
	info := provider.GetWorkflowInfo(ctx)
	if projection == nil {
		projection = map[string]*dexpb.Value{}
	}
	return &IndexSynchronizer{
		activities:           activities,
		provider:             provider,
		ctx:                  ctx,
		continueAsNewCounter: continueAsNewCounter,
		flowID:               info.WorkflowExecution.ID,
		runID:                info.WorkflowExecution.RunID,
		flowType:             flowType,
		runStartedAt:         timestamppb.New(provider.Now(ctx)),
		projection:           projection,
		pending:              pending,
		nextSequence:         nextSequence,
	}
}

func (s *IndexSynchronizer) Start() {
	s.provider.GoNamed(s.ctx, "flow-index-synchronizer", s.run)
}

func (s *IndexSynchronizer) run(ctx interfaces.UnifiedContext) {
	for {
		if err := s.provider.Await(ctx, s.ready); err != nil {
			s.err = err
			s.stopped = true
			return
		}
		if s.shouldStop() {
			s.stopped = true
			return
		}
		s.inFlight = s.pending[0]
		activityCtx := s.provider.WithActivityOptions(ctx, interfaces.ActivityOptions{
			StartToCloseTimeout:                 7 * time.Second,
			LocalActivityScheduleToCloseTimeout: 7 * time.Second,
			RetryPolicy: &dexpb.RetryPolicy{
				InitialIntervalSeconds: 1,
				MaximumIntervalSeconds: 30,
				BackoffCoefficient:     2,
			},
		})
		err := s.provider.ExecuteLocalActivity(
			nil,
			activityCtx,
			s.activities.WriteFlowIndex,
			&dexpb.WriteFlowIndexActivityInput{
				FlowId:       s.flowID,
				RunId:        s.runID,
				FlowType:     s.flowType,
				RunStartedAt: s.runStartedAt,
				Mutation:     s.inFlight,
			},
		)
		if err != nil {
			s.err = err
			s.stopped = true
			return
		}
		s.pending = s.pending[1:]
		s.inFlight = nil
		if s.shouldStop() {
			s.stopped = true
			return
		}
	}
}

func (s *IndexSynchronizer) ApplyAttributeWrites(writes []*dexpb.AttributeWrite) {
	upserts, deletes := index.ConvertAttributeWritesToIndexedValues(writes)
	if len(upserts) == 0 && len(deletes) == 0 {
		return
	}
	for _, key := range deletes {
		delete(s.projection, key)
	}
	for key, value := range upserts {
		s.projection[key] = value
	}
	s.enqueue(&dexpb.FlowIndexMutation{Upserts: upserts, Deletes: deletes})
}

func (s *IndexSynchronizer) UpdateActiveStepTypes(stepTypes []string) {
	payload, err := json.Marshal(stepTypes)
	if err != nil {
		panic("active step types must be JSON serializable")
	}
	s.enqueue(&dexpb.FlowIndexMutation{Upserts: map[string]*dexpb.Value{
		"ActiveStepTypes": {
			Kind: &dexpb.Value_ObjValue{ObjValue: &dexpb.EncodedObject{Encoding: "json", Payload: payload}},
		},
	}})
}

func (s *IndexSynchronizer) EnqueueRunMetadata(replace bool) {
	upserts := map[string]*dexpb.Value{}
	if replace {
		for key, value := range s.projection {
			upserts[key] = value
		}
	}
	s.enqueue(&dexpb.FlowIndexMutation{
		Upserts:    upserts,
		Replace:    replace,
		FlowStatus: dexpb.FlowStatus_FLOW_STATUS_RUNNING,
	})
}

func (s *IndexSynchronizer) EnqueueReplaceSnapshot() {
	upserts := make(map[string]*dexpb.Value, len(s.projection))
	for key, value := range s.projection {
		upserts[key] = value
	}
	s.enqueue(&dexpb.FlowIndexMutation{
		Upserts:    upserts,
		Replace:    true,
		FlowStatus: dexpb.FlowStatus_FLOW_STATUS_RUNNING,
	})
}

func (s *IndexSynchronizer) Reconcile(runStartedAt *timestamppb.Timestamp) {
	if runStartedAt != nil && runStartedAt.IsValid() {
		s.runStartedAt = runStartedAt
	}
	s.EnqueueReplaceSnapshot()
}

func (s *IndexSynchronizer) FlushAndClose(
	ctx interfaces.UnifiedContext,
	status dexpb.FlowStatus,
) error {
	s.enqueue(&dexpb.FlowIndexMutation{
		FlowStatus: status,
		CloseTime:  timestamppb.New(s.provider.Now(ctx)),
	})
	s.flushAndClose = true
	if s.stopped && s.err == nil {
		s.stopped = false
		s.Start()
	}
	if err := s.provider.Await(ctx, func() bool { return s.stopped }); err != nil {
		return err
	}
	return s.err
}

func (s *IndexSynchronizer) Projection() map[string]*dexpb.Value {
	return s.projection
}

func (s *IndexSynchronizer) Pending() []*dexpb.FlowIndexMutation {
	return s.pending
}

func (s *IndexSynchronizer) NextSequence() int64 {
	return s.nextSequence
}

func (s *IndexSynchronizer) enqueue(mutation *dexpb.FlowIndexMutation) {
	mutation.Sequence = s.nextSequence
	s.nextSequence++
	s.pending = append(s.pending, mutation)
}

func (s *IndexSynchronizer) ready() bool {
	return len(s.pending) > 0 || s.shouldStop()
}

func (s *IndexSynchronizer) shouldStop() bool {
	if s.inFlight != nil {
		return false
	}
	if s.flushAndClose {
		return len(s.pending) == 0
	}
	return s.continueAsNewCounter.IsThresholdMet()
}
