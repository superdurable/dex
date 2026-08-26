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
	"time"

	"github.com/superdurable/dex/config"
	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/service/interpreter/cont"
	"github.com/superdurable/dex/service/interpreter/interfaces"
)

const attributeSyncLocalActivityTimeout = 7 * time.Second

type AttributeSynchronizer struct {
	cfg                  *config.AttributeStoreConfig
	activities           *Activities
	provider             interfaces.WorkflowProvider
	continueAsNewCounter *cont.ContinueAsNewCounter
	flowID               string
	pending              []*dexpb.AttributeSyncItem
	activeProducers      int
	terminalFlushing     bool
}

func NewAttributeSynchronizer(
	cfg *config.AttributeStoreConfig,
	activities *Activities,
	provider interfaces.WorkflowProvider,
	ctx interfaces.UnifiedContext,
	continueAsNewCounter *cont.ContinueAsNewCounter,
	pending []*dexpb.AttributeSyncItem,
) *AttributeSynchronizer {
	if cfg == nil || activities == nil || provider == nil || continueAsNewCounter == nil {
		panic("AttributeSynchronizer requires non-nil dependencies")
	}
	return &AttributeSynchronizer{
		cfg:                  cfg,
		activities:           activities,
		provider:             provider,
		continueAsNewCounter: continueAsNewCounter,
		flowID:               provider.GetWorkflowInfo(ctx).WorkflowExecution.ID,
		pending:              pending,
	}
}

func (s *AttributeSynchronizer) Start(ctx interfaces.UnifiedContext) {
	s.provider.GoNamed(ctx, "attribute-store-synchronizer", s.run)
}

func (s *AttributeSynchronizer) run(ctx interfaces.UnifiedContext) {
	for {
		if err := s.provider.Await(ctx, func() bool {
			return len(s.pending) > 0 || s.shouldStop()
		}); err != nil {
			return
		}
		if s.shouldStop() {
			return
		}
		batch := s.nextBatch()
		activityCtx := s.provider.WithActivityOptions(ctx, interfaces.ActivityOptions{
			StartToCloseTimeout:                 s.cfg.EffectiveSyncAttemptTimeout(),
			LocalActivityScheduleToCloseTimeout: attributeSyncLocalActivityTimeout,
			RetryPolicy:                         s.cfg.EffectiveSyncRetryPolicy(),
		})
		err := s.provider.ExecuteActivity(
			nil,
			dexpb.StepDurability_STEP_DURABILITY_ASYNC,
			activityCtx,
			s.activities.SyncAttributeBatch,
			&dexpb.SyncAttributeBatchActivityInput{
				FlowId:     s.flowID,
				ConfigName: batch[0].GetConfigName(),
				Items:      batch,
			},
			nil,
		)
		if err != nil {
			s.provider.GetLogger(ctx).Error(
				"Attribute Store batch retry exhausted; skipping batch",
				"configName", batch[0].GetConfigName(),
				"batchSize", len(batch),
				"error", err,
			)
		}
		s.pending = s.pending[len(batch):]
	}
}

func (s *AttributeSynchronizer) AppendingToPendings(
	ctx interfaces.UnifiedContext,
	writes []*dexpb.AttributeWrite,
	storeNames []string,
) {
	if len(storeNames) == 0 {
		for _, write := range writes {
			if write == nil || !write.GetSyncConfig().GetEnabled() {
				continue
			}
			s.provider.GetLogger(ctx).Error(
				"Attribute sync is enabled without Attribute Store targets",
				"attributeName", write.GetKey(),
			)
		}
		return
	}
	for _, storeName := range storeNames {
		for _, write := range writes {
			if write == nil || !write.GetSyncConfig().GetEnabled() {
				continue
			}
			s.pending = append(s.pending, &dexpb.AttributeSyncItem{
				ConfigName: storeName,
				Key:        write.GetKey(),
				Value:      write.GetValue(),
			})
		}
	}
}

func (s *AttributeSynchronizer) ProducerStarted() {
	s.activeProducers++
}

func (s *AttributeSynchronizer) ProducerFinished() {
	s.activeProducers--
	if s.activeProducers < 0 {
		panic("active Attribute producer count is negative")
	}
}

func (s *AttributeSynchronizer) FlushAndClose(ctx interfaces.UnifiedContext) error {
	if !s.terminalFlushing {
		s.terminalFlushing = true
		if s.continueAsNewCounter.IsThresholdMet() {
			s.Start(ctx)
		}
	}
	return s.provider.Await(ctx, func() bool { return len(s.pending) == 0 })
}

func (s *AttributeSynchronizer) PendingItems() []*dexpb.AttributeSyncItem {
	return s.pending
}

func (s *AttributeSynchronizer) ProducersDrained() bool {
	return s.activeProducers == 0
}

func (s *AttributeSynchronizer) nextBatch() []*dexpb.AttributeSyncItem {
	limit := s.cfg.EffectiveSyncBatchSize()
	if limit > len(s.pending) {
		limit = len(s.pending)
	}
	configName := s.pending[0].GetConfigName()
	count := 1
	for count < limit && s.pending[count].GetConfigName() == configName {
		count++
	}
	// TODO: group multiple FlowIDs when cross-workflow batching is introduced.
	return s.pending[:count]
}

func (s *AttributeSynchronizer) shouldStop() bool {
	return !s.terminalFlushing && s.continueAsNewCounter.IsThresholdMet()
}
