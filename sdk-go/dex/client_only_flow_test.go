// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package dex_test

import (
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/blob-cache-go/blobcache"
	"github.com/superdurable/dex/sdk-go/dex"
)

var (
	remoteOrderStatus = dex.DefineAttribute[string]("remote-order-status")
	remoteOrderEvents = dex.DefineStream[string]("remote-order-events", 1<<20)
)

// remoteOrderFlow types the RPCs of an OrderFlow that another application serves.
type remoteOrderFlow struct {
	dex.FlowDefaults
	dex.ClientOnlyFlow
}

var _ dex.Flow = remoteOrderFlow{}

func (remoteOrderFlow) GetFlowType() string { return "OrderFlow" }

func (remoteOrderFlow) GetSteps() []dex.StepDef { return nil }

func (remoteOrderFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{remoteOrderStatus},
		Streams:    []dex.StreamDef{remoteOrderEvents},
	}
}

func (flow remoteOrderFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{dex.DefineRPC(flow.Cancel, &dex.RPCOptions{
		Name:           "cancel",
		LockAttributes: []dex.AttributeLock{dex.LockAttribute(remoteOrderStatus)},
	})}
}

func (remoteOrderFlow) Cancel(dex.Context, string) (*dex.RPCResult[bool], error) {
	return nil, errors.New("remoteOrderFlow is client-only")
}

// remoteOrderFlowWithSteps embeds the marker but declares a Step, which NewRegistry refuses.
type remoteOrderFlowWithSteps struct {
	remoteOrderFlow
}

func (remoteOrderFlowWithSteps) GetFlowType() string { return "OrderFlowWithSteps" }

func (remoteOrderFlowWithSteps) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(noPayload)}
}

// pointerRemoteOrderFlow registers by pointer, as Flows with state usually do.
type pointerRemoteOrderFlow struct {
	dex.FlowDefaults
	dex.ClientOnlyFlow
}

func (*pointerRemoteOrderFlow) GetFlowType() string { return "PointerOrderFlow" }

func (*pointerRemoteOrderFlow) GetSteps() []dex.StepDef { return nil }

func (*pointerRemoteOrderFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{}
}

// servedOrderFlow is an ordinary Flow, so a Worker can serve it.
type servedOrderFlow struct {
	dex.FlowDefaults
}

func (servedOrderFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(noPayload)}
}

func (servedOrderFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{}
}

func TestIsClientOnlyFlowReportsTheMarker(t *testing.T) {
	require.True(t, dex.IsClientOnlyFlow(remoteOrderFlow{}))
	require.True(t, dex.IsClientOnlyFlow(&remoteOrderFlow{}))
	require.True(t, dex.IsClientOnlyFlow(&pointerRemoteOrderFlow{}))
	require.True(t, dex.IsClientOnlyFlow(remoteOrderFlowWithSteps{}), "an embedded marker is promoted")
	require.False(t, dex.IsClientOnlyFlow(servedOrderFlow{}))
	require.False(t, dex.IsClientOnlyFlow(nil))
	var nilPointer *pointerRemoteOrderFlow
	require.False(t, dex.IsClientOnlyFlow(nilPointer))
}

func TestNewRegistryRejectsAClientOnlyFlowWithSteps(t *testing.T) {
	_, err := dex.NewRegistry([]dex.Flow{servedOrderFlow{}, remoteOrderFlowWithSteps{}})
	var definition *dex.FlowDefinitionError
	require.ErrorAs(t, err, &definition)
	require.Equal(t, "OrderFlowWithSteps", definition.FlowType)
	require.ErrorContains(t, err, "is client-only and declares 1 Steps")
}

func TestNewWorkerRejectsARegistryWithAClientOnlyFlow(t *testing.T) {
	cache := newClientOnlyFlowTestCache(t)
	registry, err := dex.NewRegistry([]dex.Flow{servedOrderFlow{}, &pointerRemoteOrderFlow{}, remoteOrderFlow{}})
	require.NoError(t, err)

	worker, err := dex.NewWorker(registry, cache, dex.WorkerOptions{})
	require.Nil(t, worker)
	var clientOnly *dex.ClientOnlyFlowError
	require.ErrorAs(t, err, &clientOnly)
	require.Equal(t, "OrderFlow", clientOnly.FlowType, "the first client-only Flow type in lexical order")
	require.EqualError(t, err, "dex: Flow type OrderFlow is client-only; a Worker cannot serve it")

	// The check runs before option validation, so it wins over an invalid option.
	_, err = dex.NewWorker(registry, cache, dex.WorkerOptions{GrpcMaxMessageBytes: -1})
	require.ErrorAs(t, err, &clientOnly)
}

func TestClientAndWorkerAcceptRegistriesThatSeparateClientOnlyFlows(t *testing.T) {
	cache := newClientOnlyFlowTestCache(t)
	clientRegistry, err := dex.NewRegistry([]dex.Flow{remoteOrderFlow{}, &pointerRemoteOrderFlow{}})
	require.NoError(t, err)
	client, err := dex.NewClient(clientRegistry, cache, dex.ClientOptions{})
	require.NoError(t, err)
	require.NoError(t, client.Close())

	workerRegistry, err := dex.NewRegistry([]dex.Flow{servedOrderFlow{}})
	require.NoError(t, err)
	worker, err := dex.NewWorker(workerRegistry, cache, dex.WorkerOptions{})
	require.NoError(t, err)
	require.NoError(t, worker.Stop(t.Context()))
}

func TestClientOnlyFlowErrorFormatsANilReceiver(t *testing.T) {
	var clientOnly *dex.ClientOnlyFlowError
	require.Equal(t, "<nil>", clientOnly.Error())
}

func newClientOnlyFlowTestCache(t *testing.T) *blobcache.Cache {
	t.Helper()
	cache, err := blobcache.New(&blobcache.Config{
		Dir:      t.TempDir(),
		MaxBytes: 1 << 20,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cache.Close()) })
	return cache
}
