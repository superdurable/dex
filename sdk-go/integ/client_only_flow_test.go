// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package integ

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/blob-cache-go/blobcache"
	"github.com/superdurable/dex/sdk-go/dex"
)

var remoteStreamTestProgress = dex.DefineStream[string]("stream-test-progress", 1<<20)

// remoteRPCFlow types the rpcFlow RPCs that the suite's Worker serves, as an application without that Worker would.
type remoteRPCFlow struct {
	dex.FlowDefaults
	dex.ClientOnlyFlow
}

func (remoteRPCFlow) GetFlowType() string { return "rpcFlow" }

func (remoteRPCFlow) GetSteps() []dex.StepDef { return nil }

func (flow remoteRPCFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{
		dex.DefineRPC(flow.Increment, &dex.RPCOptions{
			LockAttributes: []dex.AttributeLock{dex.LockAttribute(rpcFlowStatus)},
		}),
		dex.DefineRPC(flow.GetStatus, nil),
	}
}

func (remoteRPCFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{Attributes: []dex.AttributeDef{rpcFlowStatus}}
}

func (remoteRPCFlow) Increment(dex.Context, int) (*dex.RPCResult[rpcIncrementOutput], error) {
	return nil, errors.New("remoteRPCFlow is client-only")
}

func (remoteRPCFlow) GetStatus(dex.Context, dex.None) (*dex.RPCResult[string], error) {
	return nil, errors.New("remoteRPCFlow is client-only")
}

// remoteStreamTestFlow types the Stream of the streamTestFlow that the suite's Worker serves.
type remoteStreamTestFlow struct {
	dex.FlowDefaults
	dex.ClientOnlyFlow
}

func (remoteStreamTestFlow) GetFlowType() string { return "streamTestFlow" }

func (remoteStreamTestFlow) GetSteps() []dex.StepDef { return nil }

func (remoteStreamTestFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{Streams: []dex.StreamDef{remoteStreamTestProgress}}
}

// A Client whose Registry holds only client-only Flows invokes RPCs and reads Streams of Flows
// that another process's Worker serves, and a Worker refuses that Registry.
func TestClientOnlyFlowsReachFlowsThatAnotherWorkerServes(t *testing.T) {
	ctx := integrationContext(t)
	registry, err := dex.NewRegistry([]dex.Flow{remoteRPCFlow{}, remoteStreamTestFlow{}})
	require.NoError(t, err)
	cache, err := blobcache.New(&blobcache.Config{Dir: t.TempDir(), MaxBytes: 1 << 20})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cache.Close()) })
	remoteClient, err := dex.NewClient(registry, cache, dex.ClientOptions{FlowServiceAddress: flowServiceAddress()})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, remoteClient.Close()) })

	_, err = dex.NewWorker(registry, cache, dex.WorkerOptions{FlowServiceAddress: flowServiceAddress()})
	var clientOnly *dex.ClientOnlyFlowError
	require.ErrorAs(t, err, &clientOnly)
	require.Equal(t, "rpcFlow", clientOnly.FlowType)

	rpcFlowID := newFlowID(t, "client-only-rpc")
	_, err = integClient.StartFlow(ctx, rpcFlow{}, rpcFlowID, 1, dex.StartFlowOptions{})
	require.NoError(t, err)
	var incremented rpcIncrementOutput
	require.NoError(t, remoteClient.InvokeRPC(ctx, rpcFlowID, remoteRPCFlow{}.Increment, 1, &incremented))
	require.Equal(t, rpcIncrementOutput{Value: 2, SizeBefore: 0, SizeAfter: 1, StatusFound: false}, incremented)
	var status string
	require.NoError(t, remoteClient.InvokeRPC(ctx, rpcFlowID, remoteRPCFlow{}.GetStatus, nil, &status))
	require.Equal(t, "invoked", status)
	result := waitForFlow(t, rpcFlowID, true)
	require.Equal(t, dex.FlowCompleted, result.Status)

	streamFlowID := newFlowID(t, "client-only-stream")
	_, err = integClient.StartFlow(ctx, streamTestFlow{}, streamFlowID, struct{}{}, dex.StartFlowOptions{})
	require.NoError(t, err)
	waitForFlow(t, streamFlowID, false)
	var progress string
	message, err := remoteClient.ReadStream(ctx, streamFlowID, remoteStreamTestProgress, "", &progress)
	require.NoError(t, err)
	require.Equal(t, "step-progress-1", progress)
	require.NotEmpty(t, message.ResumeToken)
}
