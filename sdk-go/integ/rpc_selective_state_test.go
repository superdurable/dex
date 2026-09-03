// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

package integ

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/dex"
)

var (
	selectiveRPCItems    = dex.DefineAttributeMap[int]("selective-rpc-items")
	selectiveRPCQueued   = dex.DefineChannel[string]("selective-rpc-queued")
	selectiveRPCByTenant = dex.DefineChannelMap[string]("selective-rpc-by-tenant")
)

type selectiveRPCFlow struct {
	dex.FlowDefaults
}

func (selectiveRPCFlow) GetSteps() []dex.StepDef {
	return nil
}

func (selectiveRPCFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{selectiveRPCItems},
		Channels:   []dex.ChannelDef{selectiveRPCQueued, selectiveRPCByTenant},
	}
}

func (selectiveRPCFlow) Seed(
	ctx dex.Context,
	_ dex.None,
) (*dex.RPCResult[dex.None], error) {
	if err := selectiveRPCItems.Set(ctx, "tenant-a", 11); err != nil {
		return nil, err
	}
	if err := selectiveRPCItems.Set(ctx, "tenant-b", 22); err != nil {
		return nil, err
	}
	if err := selectiveRPCQueued.Publish(ctx, "first"); err != nil {
		return nil, err
	}
	if err := selectiveRPCQueued.Publish(ctx, "second"); err != nil {
		return nil, err
	}
	if err := selectiveRPCByTenant.Publish(ctx, "tenant-a", "alpha"); err != nil {
		return nil, err
	}
	if err := selectiveRPCByTenant.Publish(ctx, "tenant-b", "beta"); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

type selectiveRPCSnapshot struct {
	Item             int
	Queued           []dex.ChannelMessage[string]
	TenantA          []dex.ChannelMessage[string]
	TenantB          []dex.ChannelMessage[string]
	UnloadedRejected bool
}

type selectiveRPCComplementarySnapshot struct {
	ItemKeys         []string
	TenantA          []dex.ChannelMessage[string]
	UnloadedRejected bool
}

func (selectiveRPCFlow) Snapshot(
	ctx dex.Context,
	_ dex.None,
) (*dex.RPCResult[selectiveRPCSnapshot], error) {
	item, err := selectiveRPCItems.Get(ctx, "tenant-a")
	if err != nil {
		return nil, err
	}
	_, err = selectiveRPCItems.Get(ctx, "tenant-b")
	var notLoaded *dex.StateNotLoadedError
	if !errors.As(err, &notLoaded) {
		return nil, fmt.Errorf("unloaded AttributeMap instance error = %v", err)
	}
	queued, err := selectiveRPCQueued.PendingMessages(ctx)
	if err != nil {
		return nil, err
	}
	tenantA, err := selectiveRPCByTenant.PendingMessages(ctx, "tenant-a")
	if err != nil {
		return nil, err
	}
	tenantB, err := selectiveRPCByTenant.PendingMessages(ctx, "tenant-b")
	if err != nil {
		return nil, err
	}
	return &dex.RPCResult[selectiveRPCSnapshot]{Output: selectiveRPCSnapshot{
		Item:             item,
		Queued:           queued,
		TenantA:          tenantA,
		TenantB:          tenantB,
		UnloadedRejected: true,
	}}, nil
}

func (selectiveRPCFlow) ComplementarySnapshot(
	ctx dex.Context,
	_ dex.None,
) (*dex.RPCResult[selectiveRPCComplementarySnapshot], error) {
	itemKeys := selectiveRPCItems.AllInstanceKeys(ctx)
	tenantA, err := selectiveRPCByTenant.PendingMessages(ctx, "tenant-a")
	if err != nil {
		return nil, err
	}
	_, err = selectiveRPCByTenant.PendingMessages(ctx, "tenant-b")
	var notLoaded *dex.StateNotLoadedError
	if !errors.As(err, &notLoaded) {
		return nil, fmt.Errorf("unloaded ChannelMap instance error = %v", err)
	}
	return &dex.RPCResult[selectiveRPCComplementarySnapshot]{
		Output: selectiveRPCComplementarySnapshot{
			ItemKeys:         itemKeys,
			TenantA:          tenantA,
			UnloadedRejected: true,
		},
	}, nil
}

func TestRPCSelectiveStateLoading(t *testing.T) {
	ctx := integrationContext(t)
	flow := selectiveRPCFlow{}
	flowID := newFlowID(t, "rpc-selective-state")
	_, err := integClient.StartFlow(ctx, flow, flowID, nil, dex.StartFlowOptions{})
	require.NoError(t, err)
	require.NoError(t, integClient.InvokeRPC(
		ctx,
		flowID,
		flow.Seed,
		nil,
		new(dex.None),
		dex.InvokeOptions{},
	))

	var snapshot selectiveRPCSnapshot
	require.NoError(t, integClient.InvokeRPC(
		ctx,
		flowID,
		flow.Snapshot,
		nil,
		&snapshot,
		dex.InvokeOptions{
			LoadAttributeMapInstances: []dex.AttributeMapLoad{
				selectiveRPCItems.Load("tenant-a"),
			},
			LoadChannels:    []dex.ChannelDef{selectiveRPCQueued},
			LoadChannelMaps: []dex.ChannelDef{selectiveRPCByTenant},
		},
	))
	require.Equal(t, 11, snapshot.Item)
	require.True(t, snapshot.UnloadedRejected)
	require.Equal(t, []string{"first", "second"}, channelMessageValues(snapshot.Queued))
	require.Equal(t, []string{"alpha"}, channelMessageValues(snapshot.TenantA))
	require.Equal(t, []string{"beta"}, channelMessageValues(snapshot.TenantB))
	for _, message := range append(
		append(snapshot.Queued, snapshot.TenantA...),
		snapshot.TenantB...,
	) {
		require.NotEmpty(t, message.MessageID)
	}

	var complementary selectiveRPCComplementarySnapshot
	require.NoError(t, integClient.InvokeRPC(
		ctx,
		flowID,
		flow.ComplementarySnapshot,
		nil,
		&complementary,
		dex.InvokeOptions{
			LoadAttributeMaps: []dex.AttributeDef{selectiveRPCItems},
			LoadChannelMapInstances: []dex.ChannelMapLoad{
				selectiveRPCByTenant.LoadMessages("tenant-a"),
			},
		},
	))
	require.Equal(t, []string{"tenant-a", "tenant-b"}, complementary.ItemKeys)
	require.Equal(t, []string{"alpha"}, channelMessageValues(complementary.TenantA))
	require.True(t, complementary.UnloadedRejected)
	require.NoError(t, integClient.StopFlow(
		ctx,
		flowID,
		dex.StopOptions{Type: dex.FailFlow, Reason: "test complete"},
	))
}

func channelMessageValues(messages []dex.ChannelMessage[string]) []string {
	values := make([]string, 0, len(messages))
	for _, message := range messages {
		values = append(values, message.Value)
	}
	return values
}
