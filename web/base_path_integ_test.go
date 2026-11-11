// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package web_test

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	dexweb "github.com/superdurable/dex/web"
	"github.com/superdurable/dex/web/assets"
)

var indexScriptPattern = regexp.MustCompile(`src="([^"]+\.js)"`)

func TestBasePathServesPagesAssetsAndAPIUnderPrefix(t *testing.T) {
	service := &flowService{}
	harness := newHarnessWithConfig(t, service, &dexweb.Config{BindAddress: "127.0.0.1", BasePath: "/dex/web/"})
	baseURL := harness.http.URL

	for _, pagePath := range []string{"/dex/web/", "/dex/web/v1/flows", "/dex/web/v1/flows/flow-1/run-1?view=session", "/dex/web/v2/connectors"} {
		response := getAndClose(t, baseURL+pagePath)
		body := response.body
		if response.StatusCode != http.StatusOK ||
			!strings.Contains(body, `<meta name="dex-web-base-path" content="/dex/web" />`) ||
			!strings.Contains(body, `src="/dex/web/assets/`) || strings.Contains(body, `src="./`) {
			t.Fatalf("GET %s = %d %s", pagePath, response.StatusCode, body)
		}
		if pagePath == "/dex/web/" {
			script := indexScriptPattern.FindStringSubmatch(body)
			if len(script) != 2 {
				t.Fatalf("index has no script: %s", body)
			}
			if asset := getAndClose(t, baseURL+script[1]); asset.StatusCode != http.StatusOK {
				t.Fatalf("GET %s = %d", script[1], asset.StatusCode)
			}
		}
	}
	if response := getAndClose(t, baseURL+"/dex/web/api/flow-definitions"); response.StatusCode != http.StatusOK ||
		!strings.HasPrefix(response.Header.Get("Content-Type"), "application/json") {
		t.Fatalf("definitions under prefix = %d", response.StatusCode)
	}
	if response := getAndClose(t, baseURL+"/dex/web/api/no-such-route"); response.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown API route = %d", response.StatusCode)
	}
	if response := postJSON(t, baseURL+"/dex/web/api/flows/search", `{"query":""}`); response.StatusCode != http.StatusOK {
		t.Fatalf("search under prefix = %d %s", response.StatusCode, readBody(t, response))
	}
	for _, outsidePath := range []string{"/", "/v1/flows", "/api/flow-definitions", "/assets/index.js", "/dex", "/dex/webx/", "/dex/web2/api/flow-definitions"} {
		if response := getAndClose(t, baseURL+outsidePath); response.StatusCode != http.StatusNotFound {
			t.Fatalf("GET %s outside the base path = %d", outsidePath, response.StatusCode)
		}
	}
	for _, probePath := range []string{"/healthz", "/readyz"} {
		if response := getAndClose(t, baseURL+probePath); response.StatusCode != http.StatusOK {
			t.Fatalf("root probe %s = %d %s", probePath, response.StatusCode, response.body)
		}
	}
}

func TestBasePathRedirectsPrefixAndRejectsDotSegments(t *testing.T) {
	server, err := dexweb.NewServer(&dexweb.Config{BindAddress: "127.0.0.1", BasePath: "/dex"}, dexpb.NewFlowServiceClient(nil), assets.Files)
	if err != nil {
		t.Fatal(err)
	}
	redirect := httptest.NewRecorder()
	server.Handler().ServeHTTP(redirect, httptest.NewRequest(http.MethodGet, "/dex?view=session", nil))
	if redirect.Code != http.StatusMovedPermanently || redirect.Header().Get("Location") != "/dex/?view=session" {
		t.Fatalf("prefix without slash = %d %v", redirect.Code, redirect.Header())
	}
	for _, requestPath := range []string{"/dex/../api/flow-definitions", "/dex/./v1/flows", "/dex//api/flow-definitions", "/dex%2Fapi/flow-definitions"} {
		request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1"+requestPath, nil)
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusNotFound {
			t.Fatalf("GET %s = %d %v", requestPath, response.Code, response.Header())
		}
	}
}

func TestBasePathAPIWritesKeepCrossOriginProtection(t *testing.T) {
	harness := newHarnessWithConfig(t, &flowService{}, &dexweb.Config{BindAddress: "127.0.0.1", BasePath: "/dex"})
	request, err := http.NewRequest(http.MethodPost, harness.http.URL+"/dex/api/flows/stop", strings.NewReader(`{"flowId":"flow-1"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Sec-Fetch-Site", "cross-site")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-site write under prefix = %d %s", response.StatusCode, readBody(t, response))
	}
	textRequest := postText(t, harness.http.URL+"/dex/api/flows/stop")
	if textRequest.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("non-JSON write under prefix = %d", textRequest.StatusCode)
	}
}

func TestBasePathAPIKeepsHostAllowlist(t *testing.T) {
	harness := newHarnessWithConfig(t, &flowService{}, &dexweb.Config{
		BindAddress: "127.0.0.1", BasePath: "/dex", AllowedHosts: []string{"dex.example"},
	})
	request, err := http.NewRequest(http.MethodGet, harness.http.URL+"/dex/api/flow-definitions", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Host = "attacker.example"
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusForbidden || !strings.Contains(readBody(t, response), "HOST_NOT_ALLOWED") {
		t.Fatalf("rebinding Host under prefix = %d", response.StatusCode)
	}
}

func TestInvalidBasePathsAreRejected(t *testing.T) {
	for _, basePath := range []string{"dex", "//dex", "/dex//web", "/dex/../web", "/dex/./web", "/dex%2Fweb", "/dex web", "/dex?x=1", "/dex#x", `/dex\web`, "/" + strings.Repeat("a", 1100)} {
		if _, err := dexweb.NewServer(&dexweb.Config{BasePath: basePath}, dexpb.NewFlowServiceClient(nil), assets.Files); err == nil {
			t.Fatalf("base path %q was accepted", basePath)
		}
	}
	for basePath, want := range map[string]string{"": "/", "/": "/", "/dex/": "/dex", "/dex-web/server_a.1~x": "/dex-web/server_a.1~x"} {
		if got, err := dexweb.ValidateBasePath(basePath); err != nil || got != want {
			t.Fatalf("ValidateBasePath(%q) = %q, %v; want %q", basePath, got, err, want)
		}
	}
}

func TestFrameAncestorsControlFramingHeaders(t *testing.T) {
	for _, testCase := range []struct {
		name           string
		frameAncestors []string
		wantPolicy     string
		wantOptions    string
	}{
		{name: "default", wantPolicy: "frame-ancestors 'none'", wantOptions: "DENY"},
		{name: "self", frameAncestors: []string{"'self'"}, wantPolicy: "frame-ancestors 'self'", wantOptions: "SAMEORIGIN"},
		{
			name: "origins", frameAncestors: []string{"'self'", "https://host.example", "https://*.host.example:8443"},
			wantPolicy: "frame-ancestors 'self' https://host.example https://*.host.example:8443",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			server, err := dexweb.NewServer(&dexweb.Config{FrameAncestors: testCase.frameAncestors}, dexpb.NewFlowServiceClient(nil), assets.Files)
			if err != nil {
				t.Fatal(err)
			}
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/flows", nil))
			if response.Header().Get("Content-Security-Policy") != testCase.wantPolicy ||
				response.Header().Get("X-Frame-Options") != testCase.wantOptions {
				t.Fatalf("framing headers = %v", response.Header())
			}
		})
	}
	for _, source := range []string{"*", "https:", "'none'", "'unsafe-inline'", "https://a.example;script-src", "https://a.example,https://b.example", "https://a.example b", ""} {
		if err := dexweb.ValidateFrameAncestors([]string{source}); err == nil {
			t.Fatalf("frame ancestor %q was accepted", source)
		}
	}
	if got := dexweb.SplitFrameAncestors(" 'self', https://a.example  https://b.example,"); strings.Join(got, "|") != "'self'|https://a.example|https://b.example" {
		t.Fatalf("SplitFrameAncestors = %q", got)
	}
}

func TestFramingHeadersCoverRedirectsAndErrors(t *testing.T) {
	for _, testCase := range []struct {
		name           string
		frameAncestors []string
		wantPolicy     string
		wantOptions    string
	}{
		{name: "default", wantPolicy: "frame-ancestors 'none'", wantOptions: "DENY"},
		{name: "self", frameAncestors: []string{"'self'"}, wantPolicy: "frame-ancestors 'self'", wantOptions: "SAMEORIGIN"},
		{name: "allowlist", frameAncestors: []string{"https://host.example"}, wantPolicy: "frame-ancestors https://host.example"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			server, err := dexweb.NewServer(&dexweb.Config{BasePath: "/dex", FrameAncestors: testCase.frameAncestors}, dexpb.NewFlowServiceClient(nil), assets.Files)
			if err != nil {
				t.Fatal(err)
			}
			// A reverse proxy or wrapper in front of Dex Web may already deny framing.
			hostDeniesFraming := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				response.Header().Set("X-Frame-Options", "DENY")
				server.Handler().ServeHTTP(response, request)
			})
			for requestPath, wantStatus := range map[string]int{
				"/dex":                         http.StatusMovedPermanently,
				"/dex/v1/flows":                http.StatusOK,
				"/elsewhere":                   http.StatusNotFound,
				"/dex/api/no-such-route":       http.StatusNotFound,
				"/dex/api/flow-definitions":    http.StatusOK,
				"/dex/../api/flow-definitions": http.StatusNotFound,
			} {
				response := httptest.NewRecorder()
				hostDeniesFraming.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://127.0.0.1"+requestPath, nil))
				if response.Code != wantStatus ||
					response.Header().Get("Content-Security-Policy") != testCase.wantPolicy ||
					response.Header().Get("X-Frame-Options") != testCase.wantOptions {
					t.Fatalf("GET %s = %d %v", requestPath, response.Code, response.Header())
				}
			}
		})
	}
}

func TestConnectorRoutesReportUnavailableWithoutSetup(t *testing.T) {
	harness := newHarnessWithConfig(t, &flowService{}, &dexweb.Config{BindAddress: "127.0.0.1", BasePath: "/dex"})
	response := getAndClose(t, harness.http.URL+"/dex/api/v2/connector-connections")
	if response.StatusCode != http.StatusNotFound || !strings.Contains(response.body, "CONNECTOR_SETUP_UNAVAILABLE") {
		t.Fatalf("connector listing without setup = %d", response.StatusCode)
	}
}

type closedResponse struct {
	*http.Response
	body string
}

// getAndClose reads the whole body so large assets do not hold the test server's connection open.
func getAndClose(t *testing.T, url string) closedResponse {
	t.Helper()
	response := get(t, url)
	body := readBody(t, response)
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	return closedResponse{Response: response, body: body}
}

func postText(t *testing.T, url string) *http.Response {
	t.Helper()
	response, err := http.Post(url, "text/plain", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	return response
}
