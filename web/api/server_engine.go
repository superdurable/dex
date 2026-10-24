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

	"github.com/superdurable/dex/gen/dexpb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

const serverEngineLookupTimeout = 2 * time.Second

// serverEngineResolver reports the engine behind the FlowService and caches the first definitive answer.
type serverEngineResolver struct {
	client     dexpb.FlowServiceClient
	mutex      sync.Mutex
	engine     dexpb.ServerEngine
	isResolved bool
}

func newServerEngineResolver(client dexpb.FlowServiceClient) *serverEngineResolver {
	if client == nil {
		panic("Dex FlowService client must not be nil")
	}
	return &serverEngineResolver{client: client}
}

// resolveServerEngine returns SERVER_ENGINE_UNSPECIFIED while the FlowService cannot answer.
// Servers released before GetServerInfo existed are Go Servers and also return SERVER_ENGINE_UNSPECIFIED.
func (r *serverEngineResolver) resolveServerEngine(ctx context.Context) dexpb.ServerEngine {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	if r.isResolved {
		return r.engine
	}
	lookupContext, cancel := context.WithTimeout(ctx, serverEngineLookupTimeout)
	defer cancel()
	serverInfo, err := r.client.GetServerInfo(lookupContext, &emptypb.Empty{})
	if status.Code(err) == codes.Unimplemented {
		r.engine, r.isResolved = dexpb.ServerEngine_SERVER_ENGINE_UNSPECIFIED, true
		return r.engine
	}
	if err != nil {
		// Web-only Dex can start before the FlowService; definition pages must not fail, so retry next request.
		return dexpb.ServerEngine_SERVER_ENGINE_UNSPECIFIED
	}
	r.engine, r.isResolved = serverInfo.GetServerEngine(), true
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
