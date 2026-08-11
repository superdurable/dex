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
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/config"
	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/service/interpreter/interfaces"
)

type attributeSynchronizerTestProvider struct {
	interfaces.WorkflowProvider
	options []interfaces.ActivityOptions
	inputs  []*dexpb.SyncAttributeBatchActivityInput
}

func (p *attributeSynchronizerTestProvider) Await(
	_ interfaces.UnifiedContext,
	condition func() bool,
) error {
	if !condition() {
		return fmt.Errorf("condition is not ready")
	}
	return nil
}

func (p *attributeSynchronizerTestProvider) WithActivityOptions(
	ctx interfaces.UnifiedContext,
	options interfaces.ActivityOptions,
) interfaces.UnifiedContext {
	p.options = append(p.options, options)
	return ctx
}

func (p *attributeSynchronizerTestProvider) ExecuteActivity(
	_ interface{},
	durability dexpb.StepDurability,
	_ interfaces.UnifiedContext,
	_ interface{},
	regularInput interface{},
	localActivityOnlyInput interface{},
) error {
	if durability != dexpb.StepDurability_STEP_DURABILITY_ASYNC {
		return fmt.Errorf("unexpected durability %s", durability)
	}
	if localActivityOnlyInput != nil {
		return fmt.Errorf("unexpected local-only input")
	}
	input, ok := regularInput.(*dexpb.SyncAttributeBatchActivityInput)
	if !ok {
		return fmt.Errorf("unexpected input %T", regularInput)
	}
	p.inputs = append(p.inputs, input)
	return nil
}

func TestAttributeSynchronizerBatchesByLimitAndStore(t *testing.T) {
	provider := &attributeSynchronizerTestProvider{}
	synchronizer := &AttributeSynchronizer{
		cfg:        &config.AttributeStoreConfig{SyncBatchSize: 2},
		activities: &Activities{},
		provider:   provider,
		ctx:        interfaces.NewUnifiedContext(nil),
		flowID:     "flow-id",
		pending: []*dexpb.AttributeSyncMutation{
			{ConfigName: "reporting", Key: "first"},
			{ConfigName: "reporting", Key: "second"},
			{ConfigName: "reporting", Key: "third"},
			{ConfigName: "operational", Key: "fourth"},
		},
		flushAndClose: true,
	}

	synchronizer.run(synchronizer.ctx)

	require.True(t, synchronizer.stopped)
	require.Empty(t, synchronizer.pending)
	require.Len(t, provider.inputs, 3)
	require.Equal(t, []int{2, 1, 1}, []int{
		len(provider.inputs[0].GetMutations()),
		len(provider.inputs[1].GetMutations()),
		len(provider.inputs[2].GetMutations()),
	})
	require.Equal(t, "reporting", provider.inputs[0].GetConfigName())
	require.Equal(t, "operational", provider.inputs[2].GetConfigName())
	for _, options := range provider.options {
		require.Equal(t, attributeSyncLocalActivityTimeout, options.LocalActivityScheduleToCloseTimeout)
		require.Equal(t, 30*time.Second, options.StartToCloseTimeout)
		require.Equal(t, int32(3600), options.RetryPolicy.GetTotalDurationSeconds())
		require.Zero(t, options.LocalActivityRetryPolicy.GetMaximumAttempts())
		require.Equal(t, int32(7), options.LocalActivityRetryPolicy.GetTotalDurationSeconds())
		require.Equal(t, options.RetryPolicy.GetInitialIntervalSeconds(), options.LocalActivityRetryPolicy.GetInitialIntervalSeconds())
		require.Equal(t, options.RetryPolicy.GetMaximumIntervalSeconds(), options.LocalActivityRetryPolicy.GetMaximumIntervalSeconds())
		require.Equal(t, options.RetryPolicy.GetBackoffCoefficient(), options.LocalActivityRetryPolicy.GetBackoffCoefficient())
	}
}
