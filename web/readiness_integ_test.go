// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package web_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

type readinessFlowService struct {
	dexpb.UnimplementedFlowServiceServer
	serverEngine              dexpb.ServerEngine
	isServerInfoUnimplemented bool
	searchErr                 error
	healthCheckErr            error
}

func (s *readinessFlowService) GetServerInfo(context.Context, *emptypb.Empty) (*dexpb.ServerInfo, error) {
	if s.isServerInfoUnimplemented {
		return nil, status.Error(codes.Unimplemented, "unknown method GetServerInfo")
	}
	return &dexpb.ServerInfo{ServerEngine: s.serverEngine}, nil
}

func (s *readinessFlowService) SearchFlows(
	context.Context,
	*dexpb.SearchFlowsRequest,
) (*dexpb.SearchFlowsResponse, error) {
	if s.searchErr != nil {
		return nil, s.searchErr
	}
	return &dexpb.SearchFlowsResponse{}, nil
}

func (s *readinessFlowService) HealthCheck(context.Context, *emptypb.Empty) (*dexpb.HealthInfo, error) {
	if s.healthCheckErr != nil {
		return nil, s.healthCheckErr
	}
	return &dexpb.HealthInfo{Condition: "OK"}, nil
}

func TestWebServerReadinessChecksTheServerEngineBackend(t *testing.T) {
	searchErr := status.Error(codes.ResourceExhausted, "visibility scan budget exceeded")
	healthCheckErr := status.Error(codes.Unavailable, "starting")
	goServers := []struct {
		name    string
		service readinessFlowService
	}{
		{name: "Temporal", service: readinessFlowService{serverEngine: dexpb.ServerEngine_SERVER_ENGINE_TEMPORAL}},
		{name: "Cadence", service: readinessFlowService{serverEngine: dexpb.ServerEngine_SERVER_ENGINE_CADENCE}},
		{name: "without GetServerInfo", service: readinessFlowService{isServerInfoUnimplemented: true}},
	}
	type readinessCase struct {
		name       string
		service    readinessFlowService
		wantStatus int
	}
	cases := []readinessCase{
		{
			name:       "server-zd uses HealthCheck instead of a search",
			service:    readinessFlowService{serverEngine: dexpb.ServerEngine_SERVER_ENGINE_ZD, searchErr: searchErr},
			wantStatus: http.StatusOK,
		},
		{
			name:       "server-zd fails HealthCheck",
			service:    readinessFlowService{serverEngine: dexpb.ServerEngine_SERVER_ENGINE_ZD, healthCheckErr: healthCheckErr},
			wantStatus: http.StatusServiceUnavailable,
		},
	}
	for _, goServer := range goServers {
		searchFails := goServer.service
		searchFails.searchErr = searchErr
		healthCheckFails := goServer.service
		healthCheckFails.healthCheckErr = healthCheckErr
		cases = append(cases,
			readinessCase{name: goServer.name + " fails a search", service: searchFails, wantStatus: http.StatusServiceUnavailable},
			readinessCase{name: goServer.name + " ignores HealthCheck", service: healthCheckFails, wantStatus: http.StatusOK},
		)
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			service := testCase.service
			harness := newHarness(t, &service)
			response := get(t, harness.http.URL+"/readyz")
			defer response.Body.Close()
			if response.StatusCode != testCase.wantStatus {
				t.Fatalf("readiness status = %d, want %d body=%q", response.StatusCode, testCase.wantStatus, readBody(t, response))
			}
		})
	}
}
