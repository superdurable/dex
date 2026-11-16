// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package api

import (
	"context"
	"sync"
	"time"

	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

const serverEngineLookupTimeout = 2 * time.Second

// serverProtocolCacheTTL bounds how long a server upgrade or rollback stays unseen.
const serverProtocolCacheTTL = 60 * time.Second

// runHistoryForkProtocolVersion is the first server protocol with CloneForkFlow, EVENT_ID resets, and SubFlow reset policies.
const runHistoryForkProtocolVersion = 6

// ServerEngineResolver reports the engine behind the FlowService and caches the first definitive answer.
// It also reports the server's current protocol version, which it refreshes in the background after serverProtocolCacheTTL.
type ServerEngineResolver struct {
	client             dexpb.FlowServiceClient
	now                func() time.Time
	mutex              sync.Mutex
	engine             dexpb.ServerEngine
	isResolved         bool
	protocolVersion    uint32
	protocolReadAt     time.Time
	isProtocolResolved bool
	// inFlightRefresh closes when the one running GetServerInfo lookup ends; nil when none runs. Lookups never overlap.
	inFlightRefresh chan struct{}
}

// NewServerEngineResolver returns a resolver that looks up the engine on first use.
func NewServerEngineResolver(client dexpb.FlowServiceClient) *ServerEngineResolver {
	return newServerEngineResolverWithClock(client, time.Now)
}

func newServerEngineResolverWithClock(client dexpb.FlowServiceClient, now func() time.Time) *ServerEngineResolver {
	if client == nil {
		panic("Dex FlowService client must not be nil")
	}
	if now == nil {
		panic("server engine resolver clock must not be nil")
	}
	return &ServerEngineResolver{client: client, now: now}
}

// ResolveServerEngine returns SERVER_ENGINE_UNSPECIFIED while the FlowService cannot answer.
// Servers released before GetServerInfo existed are Go Servers and also return SERVER_ENGINE_UNSPECIFIED.
func (r *ServerEngineResolver) ResolveServerEngine(ctx context.Context) dexpb.ServerEngine {
	if engine, isResolved := r.cachedServerEngine(); isResolved {
		return engine
	}
	engine, _, _ := r.resolveServerInfo(ctx)
	return engine
}

// ResolveCurrentProtocolVersion returns the server's current protocol version and whether the FlowService answered.
// The version is 0 for a server without GetServerInfo or without the field; the bool is false while it cannot answer.
func (r *ServerEngineResolver) ResolveCurrentProtocolVersion(ctx context.Context) (uint32, bool) {
	_, protocolVersion, isProtocolResolved := r.resolveServerInfo(ctx)
	return protocolVersion, isProtocolResolved
}

// resolveServerInfo returns the engine, the protocol version, and whether the protocol version is known.
// A cached version returns at once, even when stale. Only the first lookup makes callers wait.
func (r *ServerEngineResolver) resolveServerInfo(ctx context.Context) (dexpb.ServerEngine, uint32, bool) {
	r.mutex.Lock()
	isStale := !r.isProtocolResolved || r.now().Sub(r.protocolReadAt) >= serverProtocolCacheTTL
	if isStale && r.inFlightRefresh == nil {
		r.inFlightRefresh = make(chan struct{})
		// Detached so a slow FlowService never blocks a caller once a version is cached.
		go r.runRefresh(r.inFlightRefresh)
	}
	if r.isProtocolResolved {
		defer r.mutex.Unlock()
		return r.engine, r.protocolVersion, true
	}
	refreshDone := r.inFlightRefresh
	r.mutex.Unlock()
	select {
	case <-refreshDone:
	case <-ctx.Done():
	}
	r.mutex.Lock()
	defer r.mutex.Unlock()
	if !r.isProtocolResolved {
		return r.engine, 0, false
	}
	return r.engine, r.protocolVersion, true
}

// runRefresh looks up the server info with its own timeout and caches a definitive answer.
func (r *ServerEngineResolver) runRefresh(refreshDone chan struct{}) {
	lookupContext, cancel := context.WithTimeout(context.Background(), serverEngineLookupTimeout)
	defer cancel()
	serverInfo, err := r.client.GetServerInfo(lookupContext, &emptypb.Empty{})
	if status.Code(err) == codes.Unimplemented {
		serverInfo, err = &dexpb.ServerInfo{}, nil
	}
	r.mutex.Lock()
	defer r.mutex.Unlock()
	// Web-only Dex can start before the FlowService; definition pages must not fail, so retry next request.
	if err == nil {
		r.cacheServerInfo(serverInfo)
	}
	r.inFlightRefresh = nil
	close(refreshDone)
}

func (r *ServerEngineResolver) cachedServerEngine() (dexpb.ServerEngine, bool) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	return r.engine, r.isResolved
}

// cacheServerInfo keeps the first cached engine and stores the newest protocol version; the caller holds the mutex.
func (r *ServerEngineResolver) cacheServerInfo(serverInfo *dexpb.ServerInfo) {
	if !r.isResolved {
		r.engine, r.isResolved = serverInfo.GetServerEngine(), true
	}
	r.protocolVersion = serverInfo.GetCurrentProtocolVersion()
	r.protocolReadAt = r.now()
	r.isProtocolResolved = true
}

func serverEngineName(engine dexpb.ServerEngine) string {
	switch engine {
	case dexpb.ServerEngine_SERVER_ENGINE_TEMPORAL:
		return "temporal"
	case dexpb.ServerEngine_SERVER_ENGINE_CADENCE:
		return "cadence"
	case dexpb.ServerEngine_SERVER_ENGINE_ZD:
		return "zd"
	default:
		return "unspecified"
	}
}
