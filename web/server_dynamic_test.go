// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/web/api"
	"google.golang.org/grpc"
)

type readinessFlowServiceClient struct {
	dexpb.FlowServiceClient
	err error
}

func (c readinessFlowServiceClient) SearchFlows(
	context.Context,
	*dexpb.SearchFlowsRequest,
	...grpc.CallOption,
) (*dexpb.SearchFlowsResponse, error) {
	return &dexpb.SearchFlowsResponse{}, c.err
}

type failingFlowDefinitionProvider struct{}

func (failingFlowDefinitionProvider) Load(context.Context) (*FlowDefinitionSnapshot, error) {
	return nil, invalidDefinitionSource(context.Canceled)
}

func TestReadinessReportsDefinitionSnapshot(t *testing.T) {
	snapshot, err := buildFlowDefinitionSnapshot(nil, "local", "", "")
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	response := httptest.NewRecorder()
	readinessHandler(readinessFlowServiceClient{}, staticFlowDefinitionProvider{snapshot: snapshot})(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), snapshot.DefinitionRevision) {
		t.Fatalf("readiness response = %d %q", response.Code, response.Body.String())
	}
}

func TestFlowDefinitionResponseReportsRevisionETag(t *testing.T) {
	snapshot, err := buildFlowDefinitionSnapshot(nil, "local", "", "")
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/flow-definitions", nil)
	response := httptest.NewRecorder()
	serveFlowDefinitions(staticFlowDefinitionProvider{snapshot: snapshot})(response, request)
	if response.Code != http.StatusOK || response.Header().Get("ETag") != `"`+snapshot.DefinitionRevision+`"` {
		t.Fatalf("definition response = %d headers=%v", response.Code, response.Header())
	}
}

func TestReadinessFailsForInvalidDefinitionSource(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	response := httptest.NewRecorder()
	readinessHandler(readinessFlowServiceClient{}, failingFlowDefinitionProvider{})(response, request)
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), flowDefinitionSourceInvalid) {
		t.Fatalf("readiness response = %d %q", response.Code, response.Body.String())
	}
}

func TestReadinessFailsForUnavailableFlowService(t *testing.T) {
	snapshot, err := buildFlowDefinitionSnapshot(nil, "local", "", "")
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	response := httptest.NewRecorder()
	readinessHandler(
		readinessFlowServiceClient{err: context.DeadlineExceeded},
		staticFlowDefinitionProvider{snapshot: snapshot},
	)(response, request)
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "FLOW_SERVICE_UNAVAILABLE") {
		t.Fatalf("readiness response = %d %q", response.Code, response.Body.String())
	}
}

func TestSPAInjectsTrustedHeaderMode(t *testing.T) {
	handler := spaHandler(fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<html><head></head><body></body></html>")},
	}, api.V2PermissionModeTrustedHeader)
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(
		response.Body.String(),
		`window.__DEX_WEB_CONFIG__={"workQueuePermissionMode":"trusted-header"}`,
	) {
		t.Fatalf("SPA response = %d %q", response.Code, response.Body.String())
	}
}
