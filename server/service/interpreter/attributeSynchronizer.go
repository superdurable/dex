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
	ctx                  interfaces.UnifiedContext
	continueAsNewCounter *cont.ContinueAsNewCounter
	flowID               string
	pending              []*dexpb.AttributeSyncItem
	flushRequested       bool
	actorStopped         bool
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
		ctx:                  ctx,
		continueAsNewCounter: continueAsNewCounter,
		flowID:               provider.GetWorkflowInfo(ctx).WorkflowExecution.ID,
		pending:              pending,
	}
}

func (s *AttributeSynchronizer) Start() {
	s.provider.GoNamed(s.ctx, "attribute-store-synchronizer", s.run)
}

func (s *AttributeSynchronizer) run(ctx interfaces.UnifiedContext) {
	for {
		if err := s.provider.Await(ctx, s.ready); err != nil {
			s.actorStopped = true
			return
		}
		if s.shouldStop() {
			s.actorStopped = true
			return
		}
		batch := s.nextBatch()
		s.recordCounter("attribute_sync_batch", 1, batch[0].GetConfigName())
		s.recordCounter("attribute_sync_batch_mutation", int64(len(batch)), batch[0].GetConfigName())
		activityCtx := s.provider.WithActivityOptions(ctx, interfaces.ActivityOptions{
			StartToCloseTimeout:                 s.cfg.EffectiveSyncAttemptTimeout(),
			LocalActivityScheduleToCloseTimeout: attributeSyncLocalActivityTimeout,
			RetryPolicy:                         s.cfg.EffectiveSyncRetryPolicy(),
			LocalActivityRetryPolicy:            s.localRetryPolicy(),
		})
		err := s.provider.ExecuteActivity(
			nil,
			dexpb.StepDurability_STEP_DURABILITY_ASYNC,
			activityCtx,
			s.activities.SyncAttributeBatch,
			&dexpb.SyncAttributeBatchActivityInput{
				FlowId:     s.flowID,
				ConfigName: batch[0].GetConfigName(),
				Mutations:  batch,
			},
			nil,
		)
		if err != nil {
			s.recordCounter("attribute_sync_retry_exhausted", 1, batch[0].GetConfigName())
			s.provider.GetLogger(ctx).Error(
				"Attribute Store batch retry exhausted; skipping batch",
				"configName", batch[0].GetConfigName(),
				"batchSize", len(batch),
				"error", err,
			)
		}
		s.pending = s.pending[len(batch):]
		if s.shouldStop() {
			s.actorStopped = true
			return
		}
	}
}

func (s *AttributeSynchronizer) localRetryPolicy() *dexpb.RetryPolicy {
	configured := s.cfg.EffectiveSyncRetryPolicy()
	return &dexpb.RetryPolicy{
		InitialIntervalSeconds: configured.GetInitialIntervalSeconds(),
		MaximumIntervalSeconds: configured.GetMaximumIntervalSeconds(),
		BackoffCoefficient:     configured.GetBackoffCoefficient(),
		TotalDurationSeconds:   int32(attributeSyncLocalActivityTimeout / time.Second),
	}
}

func (s *AttributeSynchronizer) recordCounter(name string, value int64, configName string) {
	recorder, ok := s.provider.(interface {
		RecordCounter(interfaces.UnifiedContext, string, int64, map[string]string)
	})
	if !ok {
		return
	}
	recorder.RecordCounter(s.ctx, name, value, map[string]string{"attribute_store": configName})
}

func (s *AttributeSynchronizer) ApplyAttributeWrites(
	writes []*dexpb.AttributeWrite,
	configName string,
) {
	for _, write := range writes {
		if write == nil || !write.GetSyncConfig().GetEnabled() {
			continue
		}
		if configName == "" {
			s.recordCounter("attribute_sync_missing_target", 1, "")
			s.provider.GetLogger(s.ctx).Error(
				"Attribute sync is enabled without an Attribute Store target",
				"attributeName", write.GetKey(),
			)
			continue
		}
		s.pending = append(s.pending, &dexpb.AttributeSyncItem{
			ConfigName: configName,
			Key:        write.GetKey(),
			Value:      write.GetValue(),
		})
	}
}

func (s *AttributeSynchronizer) FlushAndClose(ctx interfaces.UnifiedContext) error {
	s.flushRequested = true
	if s.actorStopped && len(s.pending) > 0 {
		// A terminal signal can supersede Continue-as-New after the actor stops.
		s.actorStopped = false
		s.Start()
	}
	return s.provider.Await(ctx, func() bool { return s.actorStopped })
}

func (s *AttributeSynchronizer) Pending() []*dexpb.AttributeSyncItem {
	return s.pending
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

func (s *AttributeSynchronizer) ready() bool {
	return len(s.pending) > 0 || s.shouldStop()
}

func (s *AttributeSynchronizer) shouldStop() bool {
	if s.flushRequested {
		return len(s.pending) == 0
	}
	return s.continueAsNewCounter.IsThresholdMet()
}
