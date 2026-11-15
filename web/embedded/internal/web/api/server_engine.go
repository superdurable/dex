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

// ServerEngineResolver reports the engine behind the FlowService and caches the first definitive answer.
type ServerEngineResolver struct {
	client     dexpb.FlowServiceClient
	mutex      sync.Mutex
	engine     dexpb.ServerEngine
	isResolved bool
}

// NewServerEngineResolver returns a resolver that looks up the engine on first use.
func NewServerEngineResolver(client dexpb.FlowServiceClient) *ServerEngineResolver {
	if client == nil {
		panic("Dex FlowService client must not be nil")
	}
	return &ServerEngineResolver{client: client}
}

// ResolveServerEngine returns SERVER_ENGINE_UNSPECIFIED while the FlowService cannot answer.
// Servers released before GetServerInfo existed are Go Servers and also return SERVER_ENGINE_UNSPECIFIED.
func (r *ServerEngineResolver) ResolveServerEngine(ctx context.Context) dexpb.ServerEngine {
	if engine, isResolved := r.cachedServerEngine(); isResolved {
		return engine
	}
	// The lookup runs unlocked so a slow FlowService does not serialize concurrent requests.
	lookupContext, cancel := context.WithTimeout(ctx, serverEngineLookupTimeout)
	defer cancel()
	serverInfo, err := r.client.GetServerInfo(lookupContext, &emptypb.Empty{})
	if status.Code(err) == codes.Unimplemented {
		return r.cacheServerEngine(dexpb.ServerEngine_SERVER_ENGINE_UNSPECIFIED)
	}
	if err != nil {
		// Web-only Dex can start before the FlowService; definition pages must not fail, so retry next request.
		return dexpb.ServerEngine_SERVER_ENGINE_UNSPECIFIED
	}
	return r.cacheServerEngine(serverInfo.GetServerEngine())
}

func (r *ServerEngineResolver) cachedServerEngine() (dexpb.ServerEngine, bool) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	return r.engine, r.isResolved
}

// cacheServerEngine keeps the first cached answer when concurrent lookups race.
func (r *ServerEngineResolver) cacheServerEngine(engine dexpb.ServerEngine) dexpb.ServerEngine {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	if !r.isResolved {
		r.engine, r.isResolved = engine, true
	}
	return r.engine
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
