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
		`window.__DEX_WEB_CONFIG__={"workQueuePermissionMode":"trusted-header","basePath":"/","embedded":false}`,
	) {
		t.Fatalf("SPA response = %d %q", response.Code, response.Body.String())
	}
}

func TestForwardedEmbeddingHeadersAreRejectedUnlessTrusted(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set(forwardedPrefixHeader, "/hosted/dex")
	request.Header.Set(forwardedEmbeddedHeader, "true")
	response := httptest.NewRecorder()
	forwardedEmbeddingHandler(&Config{}, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("untrusted forwarded headers reached the Web handler")
	})).ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), untrustedEmbeddingHeadersCode) {
		t.Fatalf("untrusted response = %d %q", response.Code, response.Body.String())
	}
}

func TestEmbeddedSPAUsesTrustedRequestBootstrapAndFramePolicy(t *testing.T) {
	assets := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte(`<html><head><link rel="icon" href="/logo.png"></head><body><script src="/assets/app.js"></script></body></html>`)},
		"logo.png":   &fstest.MapFile{Data: []byte("logo")},
	}
	handler := forwardedEmbeddingHandler(
		&Config{TrustForwardedEmbeddingHeaders: true},
		spaHandler(assets, api.V2PermissionModeTrustedHeader),
	)
	request := httptest.NewRequest(http.MethodGet, "/v2/run", nil)
	request.Header.Set(forwardedPrefixHeader, "/api/dex-web/proxy/projects/p1/environments/STAGING")
	request.Header.Set(forwardedEmbeddedHeader, "true")
	request.Header.Set(forwardedCSRFTokenHeader, "csrf-token-p1")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("embedded SPA response = %d %q", response.Code, response.Body.String())
	}
	body := response.Body.String()
	for _, expected := range []string{
		`"basePath":"/api/dex-web/proxy/projects/p1/environments/STAGING"`,
		`"embedded":true`,
		`"csrfHeaderName":"X-CSRF-Token"`,
		`"csrfToken":"csrf-token-p1"`,
		`href="/api/dex-web/proxy/projects/p1/environments/STAGING/logo.png"`,
		`src="/api/dex-web/proxy/projects/p1/environments/STAGING/assets/app.js"`,
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("embedded SPA body missing %q: %s", expected, body)
		}
	}
	if response.Header().Get("Content-Security-Policy") != "frame-ancestors 'self'" ||
		response.Header().Get("X-Frame-Options") != "SAMEORIGIN" ||
		response.Header().Get("Cache-Control") != "no-store" ||
		response.Header().Get("Vary") != forwardedEmbeddingVaryHeader {
		t.Fatalf("embedded SPA headers = %v", response.Header())
	}
}

func TestStandaloneSPARejectsFraming(t *testing.T) {
	handler := forwardedEmbeddingHandler(
		&Config{},
		spaHandler(fstest.MapFS{
			"index.html": &fstest.MapFile{Data: []byte("<html><head></head><body></body></html>")},
		}, api.V2PermissionModeLocalSelector),
	)
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Header().Get("Content-Security-Policy") != "frame-ancestors 'none'" ||
		response.Header().Get("X-Frame-Options") != "DENY" {
		t.Fatalf("standalone SPA headers = %v", response.Header())
	}
}

func TestForwardedEmbeddingHeadersFailClosed(t *testing.T) {
	tests := []struct {
		name    string
		headers http.Header
	}{
		{name: "missing embedded", headers: http.Header{forwardedPrefixHeader: []string{"/hosted/dex"}}},
		{name: "missing prefix", headers: http.Header{forwardedEmbeddedHeader: []string{"true"}}},
		{name: "multiple prefix", headers: http.Header{forwardedPrefixHeader: []string{"/one", "/two"}, forwardedEmbeddedHeader: []string{"true"}}},
		{name: "invalid embedded", headers: http.Header{forwardedPrefixHeader: []string{"/hosted/dex"}, forwardedEmbeddedHeader: []string{"1"}}},
		{name: "authority", headers: http.Header{forwardedPrefixHeader: []string{"//evil.example/dex"}, forwardedEmbeddedHeader: []string{"true"}}},
		{name: "trailing slash", headers: http.Header{forwardedPrefixHeader: []string{"/hosted/dex/"}, forwardedEmbeddedHeader: []string{"true"}}},
		{name: "dot segment", headers: http.Header{forwardedPrefixHeader: []string{"/hosted/../dex"}, forwardedEmbeddedHeader: []string{"true"}}},
		{name: "encoded dot segment", headers: http.Header{forwardedPrefixHeader: []string{"/hosted/%2e%2e/dex"}, forwardedEmbeddedHeader: []string{"true"}}},
		{name: "encoded separator", headers: http.Header{forwardedPrefixHeader: []string{"/hosted%2fdex"}, forwardedEmbeddedHeader: []string{"true"}}},
		{name: "query", headers: http.Header{forwardedPrefixHeader: []string{"/hosted/dex?project=other"}, forwardedEmbeddedHeader: []string{"true"}}},
		{name: "empty csrf", headers: http.Header{forwardedPrefixHeader: []string{"/hosted/dex"}, forwardedEmbeddedHeader: []string{"true"}, http.CanonicalHeaderKey(forwardedCSRFTokenHeader): []string{""}}},
		{name: "invalid csrf", headers: http.Header{forwardedPrefixHeader: []string{"/hosted/dex"}, forwardedEmbeddedHeader: []string{"true"}, http.CanonicalHeaderKey(forwardedCSRFTokenHeader): []string{"token with spaces"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := webRequestConfigFromHeaders(test.headers, true)
			if err == nil {
				t.Fatalf("headers accepted: %v", test.headers)
			}
		})
	}
}

func TestForwardedEmbeddingHeadersAcceptCanonicalRootAndNestedPaths(t *testing.T) {
	for _, prefix := range []string{"/", "/hosted/dex", "/projects/customer%20success/dex"} {
		t.Run(prefix, func(t *testing.T) {
			requestConfig, err := webRequestConfigFromHeaders(http.Header{
				forwardedPrefixHeader:   []string{prefix},
				forwardedEmbeddedHeader: []string{"false"},
			}, true)
			if err != nil {
				t.Fatal(err)
			}
			if requestConfig.basePath != prefix || requestConfig.isEmbedded {
				t.Fatalf("request config = %+v", requestConfig)
			}
		})
	}
}
