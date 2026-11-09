// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package integ

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/dex"
)

var countBatchTexts = dex.DefineAttributeMap[string]("count-batch-texts")

type CountBatchHydrationFlow struct {
	dex.FlowDefaults
}

func (CountBatchHydrationFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(countBatchReadStep{})}
}

func (CountBatchHydrationFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{Attributes: []dex.AttributeDef{countBatchTexts}}
}

type countBatchReadStep struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (countBatchReadStep) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{ExecuteLoadAttributeMaps: []dex.AttributeDef{countBatchTexts}}
}

func (countBatchReadStep) Execute(ctx dex.Context, _ dex.None) (*dex.StepDecision, error) {
	for index := 0; index < 2500; index++ {
		instance := fmt.Sprintf("entry-%d", index)
		text, err := countBatchTexts.Get(ctx, instance)
		if err != nil {
			return nil, err
		}
		if text != instance+strings.Repeat("x", 256) {
			return nil, fmt.Errorf("unexpected value for AttributeMap instance %q", instance)
		}
	}
	return dex.GracefulComplete(2500), nil
}

func TestLoadBlobsCountBatchesHydrate2500ValuesInOneStep(t *testing.T) {
	ctx := integrationContext(t)
	flow := CountBatchHydrationFlow{}
	registry, err := dex.NewRegistry([]dex.Flow{flow})
	require.NoError(t, err)
	startUncachedMessageSizeWorker(ctx, t, registry)
	client, err := dex.NewClient(registry, newUncachedMessageSizeCache(t), dex.ClientOptions{
		FlowServiceAddress: flowServiceAddress(),
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	attributes := make([]dex.InitialAttributeDef, 0, 2500)
	for index := 0; index < 2500; index++ {
		instance := fmt.Sprintf("entry-%d", index)
		attribute, err := dex.InitialAttributeMapValue(countBatchTexts, instance, instance+strings.Repeat("x", 256))
		require.NoError(t, err)
		attributes = append(attributes, attribute)
	}
	flowID := newFlowID(t, "loadblobs-count-batches")
	_, err = client.StartFlow(ctx, flow, flowID, nil, dex.StartFlowOptions{Attributes: attributes})
	require.NoError(t, err)
	result, err := client.WaitForFlow(ctx, flowID, dex.WaitForFlowOptions{NeedsResults: true})
	require.NoError(t, err)
	require.Equal(t, dex.FlowCompleted, result.Status)
	require.Len(t, result.Completions, 1)
	var count int
	require.NoError(t, result.Completions[0].Output.Decode(&count))
	require.Equal(t, 2500, count)
}
