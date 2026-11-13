// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package embedded_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"github.com/superdurable/dex/web/embedded"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/emptypb"
)

// dexServer is one fake Dex Server; it answers only callers that send its own token.
type dexServer struct {
	dexpb.UnimplementedFlowServiceServer
	name         string
	engine       dexpb.ServerEngine
	searchCount  atomic.Int32
	stopRequests chan *dexpb.StopFlowRequest
}

func (s *dexServer) SearchFlows(ctx context.Context, _ *dexpb.SearchFlowsRequest) (*dexpb.SearchFlowsResponse, error) {
	if err := s.checkToken(ctx); err != nil {
		return nil, err
	}
	s.searchCount.Add(1)
	return &dexpb.SearchFlowsResponse{FlowRuns: []*dexpb.SearchFlowsResponseEntry{{
		FlowId: s.name + "-flow", RunId: "run-1", FlowType: "checkout",
	}}}, nil
}

func (s *dexServer) GetServerInfo(ctx context.Context, _ *emptypb.Empty) (*dexpb.ServerInfo, error) {
	if err := s.checkToken(ctx); err != nil {
		return nil, err
	}
	return &dexpb.ServerInfo{ServerEngine: s.engine}, nil
}

func (s *dexServer) HealthCheck(ctx context.Context, _ *emptypb.Empty) (*dexpb.HealthInfo, error) {
	return &dexpb.HealthInfo{}, s.checkToken(ctx)
}

func (s *dexServer) StopFlow(ctx context.Context, request *dexpb.StopFlowRequest) (*emptypb.Empty, error) {
	if err := s.checkToken(ctx); err != nil {
		return nil, err
	}
	s.stopRequests <- request
	return &emptypb.Empty{}, nil
}

// uploadedFlowDefinition is the one definition server-a stores; server-b stores none.
const uploadedFlowDefinition = `{"schemaVersion":"2.0","valid":true,"source":{"language":"go","path":"flow.go"},` +
	`"flow":{"name":"UploadedFlow"},"nodes":[],"edges":[],"diagnostics":[],` +
	`"groups":[{"id":"control","label":"Control","stepIds":["step:control"]}],` +
	`"v2":{"indexedAttributes":[],"summary":{"rpcName":"GetDexSummary","fields":[]},` +
	`"display":{"rpcName":"GetDexDisplay","fields":[]},"actions":[]}}`

func (s *dexServer) ListFlowDefinitions(ctx context.Context, _ *dexpb.ListFlowDefinitionsRequest) (*dexpb.ListFlowDefinitionsResponse, error) {
	if err := s.checkToken(ctx); err != nil {
		return nil, err
	}
	if s.name != "server-a" {
		return &dexpb.ListFlowDefinitionsResponse{}, nil
	}
	return &dexpb.ListFlowDefinitionsResponse{FlowTypes: []string{"UploadedFlow"}}, nil
}

func (s *dexServer) GetFlowDefinitions(ctx context.Context, request *dexpb.GetFlowDefinitionsRequest) (*dexpb.GetFlowDefinitionsResponse, error) {
	if err := s.checkToken(ctx); err != nil {
		return nil, err
	}
	response := &dexpb.GetFlowDefinitionsResponse{}
	if s.name == "server-a" && slices.Contains(request.GetFlowTypes(), "UploadedFlow") {
		response.Definitions = []*dexpb.FlowDefinition{{
			Info:    &dexpb.FlowDefinitionInfo{FlowType: "UploadedFlow", Digest: "unused"},
			Content: []byte(uploadedFlowDefinition),
		}}
	}
	return response, nil
}

func (s *dexServer) checkToken(ctx context.Context) error {
	incoming, _ := metadata.FromIncomingContext(ctx)
	if tokens := incoming.Get("authorization"); len(tokens) != 1 || tokens[0] != "Bearer "+s.name {
		return fmt.Errorf("%s received the wrong credential", s.name)
	}
	return nil
}

// startDexServer returns a client that carries the server's credential, like a host-owned connection.
func startDexServer(t *testing.T, name string, engine dexpb.ServerEngine) (*dexServer, dexpb.FlowServiceClient) {
	t.Helper()
	server := &dexServer{name: name, engine: engine, stopRequests: make(chan *dexpb.StopFlowRequest, 1)}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	grpcServer := grpc.NewServer()
	dexpb.RegisterFlowServiceServer(grpcServer, server)
	serveErrors := make(chan error, 1)
	go func() { serveErrors <- grpcServer.Serve(listener) }()
	addCredential := func(ctx context.Context, method string, request, reply any, connection *grpc.ClientConn, invoker grpc.UnaryInvoker, options ...grpc.CallOption) error {
		return invoker(metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+name), method, request, reply, connection, options...)
	}
	connection, err := grpc.NewClient(listener.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithUnaryInterceptor(addCredential))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := connection.Close(); err != nil {
			t.Error(err)
		}
		grpcServer.Stop()
		if err := <-serveErrors; err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			t.Error(err)
		}
	})
	return server, dexpb.NewFlowServiceClient(connection)
}

type hostApplication struct {
	http         *httptest.Server
	serverA      *dexServer
	serverB      *dexServer
	resolveCount atomic.Int32
}

// newHostApplication mounts one handler at /dex-web/{serverID}/ for two Dex Servers, as a host would.
func newHostApplication(t *testing.T, cfg *embedded.Config) *hostApplication {
	t.Helper()
	host := &hostApplication{}
	serverA, clientA := startDexServer(t, "server-a", dexpb.ServerEngine_SERVER_ENGINE_ZD)
	serverB, clientB := startDexServer(t, "server-b", dexpb.ServerEngine_SERVER_ENGINE_TEMPORAL)
	host.serverA, host.serverB = serverA, serverB
	clients := map[string]dexpb.FlowServiceClient{"server-a": clientA, "server-b": clientB}
	cfg.ResolveTarget = func(request *http.Request) (embedded.Target, error) {
		host.resolveCount.Add(1)
		serverID := request.PathValue("serverID")
		switch serverID {
		case "forbidden":
			return embedded.Target{}, fmt.Errorf("role check: %w", embedded.ErrTargetForbidden)
		case "down":
			return embedded.Target{}, embedded.ErrTargetUnavailable
		case "broken":
			return embedded.Target{}, errors.New("secret database password leaked")
		case "bad-base-path":
			return embedded.Target{FlowServiceClient: clientA, BasePath: "/dex-web/../x"}, nil
		case "linked":
			return embedded.Target{FlowServiceClient: clientA, BasePath: "/dex-web/linked", HostLink: &embedded.HostLink{
				Name: `test <b>`, URL: "/servers/test?tab=overview&x=1",
				Pages: []embedded.HostPage{{Name: "Settings", URL: "https://console.example/servers/test/settings?a=1&b=2"}},
			}}, nil
		case "bad-host-page":
			return embedded.Target{FlowServiceClient: clientA, BasePath: "/dex-web/bad-host-page", HostLink: &embedded.HostLink{
				Name: "test", URL: "/servers/test", Pages: []embedded.HostPage{{Name: "Settings", URL: "javascript:alert(1)"}},
			}}, nil
		case "bad-host-link":
			return embedded.Target{FlowServiceClient: clientA, BasePath: "/dex-web/bad-host-link", HostLink: &embedded.HostLink{
				Name: "test", URL: "javascript:alert(1)",
			}}, nil
		}
		client, ok := clients[serverID]
		if !ok {
			return embedded.Target{}, embedded.ErrTargetNotFound
		}
		return embedded.Target{FlowServiceClient: client, BasePath: "/dex-web/" + serverID}, nil
	}
	handler, err := embedded.NewDexWebHandler(cfg)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/dex-web/{serverID}/", handler)
	host.http = httptest.NewServer(mux)
	t.Cleanup(host.http.Close)
	return host
}

type response struct {
	status int
	header http.Header
	body   string
}

func (host *hostApplication) send(t *testing.T, method string, path string, body string, headers map[string]string) response {
	t.Helper()
	request, err := http.NewRequest(method, host.http.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	result, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	payload, readErr := io.ReadAll(result.Body)
	if err := errors.Join(readErr, result.Body.Close()); err != nil {
		t.Fatal(err)
	}
	return response{status: result.StatusCode, header: result.Header, body: string(payload)}
}

var jsonHeaders = map[string]string{"Content-Type": "application/json"}

func TestHandlerRoutesEachRequestToItsOwnDexServer(t *testing.T) {
	host := newHostApplication(t, &embedded.Config{})
	for _, serverID := range []string{"server-a", "server-b", "server-a"} {
		result := host.send(t, http.MethodPost, "/dex-web/"+serverID+"/api/flows/search", `{}`, jsonHeaders)
		if result.status != http.StatusOK || !strings.Contains(result.body, `"flowId":"`+serverID+`-flow"`) {
			t.Fatalf("search on %s = %d %s", serverID, result.status, result.body)
		}
	}
	if host.serverA.searchCount.Load() != 2 || host.serverB.searchCount.Load() != 1 {
		t.Fatalf("searches reached A=%d B=%d", host.serverA.searchCount.Load(), host.serverB.searchCount.Load())
	}
	// The server engine is looked up per request, so B must not inherit A's answer.
	for serverID, engine := range map[string]string{"server-a": "zd", "server-b": "temporal"} {
		result := host.send(t, http.MethodGet, "/dex-web/"+serverID+"/api/v2/catalog", "", nil)
		if result.status != http.StatusOK || !strings.Contains(result.body, `"serverEngine":"`+engine+`"`) {
			t.Fatalf("catalog on %s = %d %s", serverID, result.status, result.body)
		}
	}
}

// A Target without FlowRenderingDirectory reads the definitions its own Dex Server stores.
func TestHandlerServesUploadedDefinitionsWithoutADirectory(t *testing.T) {
	host := newHostApplication(t, &embedded.Config{})
	uploaded := host.send(t, http.MethodGet, "/dex-web/server-a/api/flow-definitions/by-type?flowType=UploadedFlow", "", nil)
	if uploaded.status != http.StatusOK || !strings.Contains(uploaded.body, `"source":"uploaded"`) ||
		!strings.Contains(uploaded.body, `"v2":{"flowType":"UploadedFlow"`) {
		t.Fatalf("UploadedFlow on server-a = %d %s", uploaded.status, uploaded.body)
	}
	types := host.send(t, http.MethodGet, "/dex-web/server-a/api/flow-definition-types", "", nil)
	if types.status != http.StatusOK || !strings.Contains(types.body, `{"flowType":"UploadedFlow","source":"uploaded"}`) ||
		!strings.Contains(types.body, `"configured":true`) {
		t.Fatalf("Flow types on server-a = %d %s", types.status, types.body)
	}
	catalog := host.send(t, http.MethodGet, "/dex-web/server-a/api/v2/catalog", "", nil)
	if catalog.status != http.StatusOK || !strings.Contains(catalog.body, `"flowType":"UploadedFlow"`) {
		t.Fatalf("catalog on server-a = %d %s", catalog.status, catalog.body)
	}
	other := host.send(t, http.MethodGet, "/dex-web/server-b/api/flow-definitions/by-type?flowType=UploadedFlow", "", nil)
	if other.status != http.StatusNotFound {
		t.Fatalf("UploadedFlow on server-b = %d %s", other.status, other.body)
	}
}

func TestHandlerServesPagesUnderEachBasePath(t *testing.T) {
	host := newHostApplication(t, &embedded.Config{})
	for _, serverID := range []string{"server-a", "server-b"} {
		basePath := "/dex-web/" + serverID
		for _, pagePath := range []string{"/", "/v1/flows/flow-1/run-1", "/v2/connectors"} {
			result := host.send(t, http.MethodGet, basePath+pagePath, "", nil)
			if result.status != http.StatusOK ||
				!strings.Contains(result.body, `<meta name="dex-web-base-path" content="`+basePath+`" />`) ||
				!strings.Contains(result.body, `src="`+basePath+`/assets/`) ||
				result.header.Get("Content-Security-Policy") != "frame-ancestors 'none'" ||
				result.header.Get("X-Frame-Options") != "DENY" {
				t.Fatalf("GET %s%s = %d %v %s", basePath, pagePath, result.status, result.header, result.body)
			}
		}
		if result := host.send(t, http.MethodGet, basePath+"/readyz", "", nil); result.status != http.StatusOK {
			t.Fatalf("readiness of %s = %d %s", serverID, result.status, result.body)
		}
	}
	// The host's ServeMux answers this one; Dex Web's own redirect is covered by the standalone tests.
	redirect := host.send(t, http.MethodGet, "/dex-web/server-a", "", nil)
	if redirect.status/100 != 3 || redirect.header.Get("Location") != "/dex-web/server-a/" {
		t.Fatalf("mount without slash = %d %v", redirect.status, redirect.header)
	}
}

func TestHandlerRecordsTheHostLinkForTheTrail(t *testing.T) {
	host := newHostApplication(t, &embedded.Config{})
	linked := host.send(t, http.MethodGet, "/dex-web/linked/v1/flows", "", nil)
	if linked.status != http.StatusOK ||
		!strings.Contains(linked.body, `<meta name="dex-web-host-name" content="test &lt;b&gt;" />`) ||
		!strings.Contains(linked.body, `<meta name="dex-web-host-url" content="/servers/test?tab=overview&amp;x=1" />`) ||
		!strings.Contains(linked.body, `<meta name="dex-web-host-page" content="Settings" data-url="https://console.example/servers/test/settings?a=1&amp;b=2" />`) {
		t.Fatalf("linked page = %d %s", linked.status, linked.body)
	}
	plain := host.send(t, http.MethodGet, "/dex-web/server-a/v1/flows", "", nil)
	if plain.status != http.StatusOK || strings.Contains(plain.body, "dex-web-host-") {
		t.Fatalf("page without a host link = %d %s", plain.status, plain.body)
	}
	if bad := host.send(t, http.MethodGet, "/dex-web/bad-host-link/v1/flows", "", nil); bad.status != http.StatusInternalServerError ||
		strings.Contains(bad.body, "javascript") {
		t.Fatalf("page with an unsafe host link = %d %s", bad.status, bad.body)
	}
	if bad := host.send(t, http.MethodGet, "/dex-web/bad-host-page/v1/flows", "", nil); bad.status != http.StatusInternalServerError ||
		strings.Contains(bad.body, "javascript") {
		t.Fatalf("page with an unsafe host page = %d %s", bad.status, bad.body)
	}
}

func TestHandlerMapsResolverErrorsWithoutDetails(t *testing.T) {
	host := newHostApplication(t, &embedded.Config{})
	for serverID, wantStatus := range map[string]int{
		"unknown":       http.StatusNotFound,
		"forbidden":     http.StatusForbidden,
		"down":          http.StatusServiceUnavailable,
		"broken":        http.StatusInternalServerError,
		"bad-base-path": http.StatusInternalServerError,
	} {
		result := host.send(t, http.MethodGet, "/dex-web/"+serverID+"/api/flow-definitions", "", nil)
		if result.status != wantStatus || strings.Contains(result.body, "secret") || strings.Contains(result.body, "role check") {
			t.Fatalf("%s = %d %s", serverID, result.status, result.body)
		}
	}
}

func TestHandlerLetsHostWriteResolverErrors(t *testing.T) {
	host := newHostApplication(t, &embedded.Config{
		WriteResolveError: func(response http.ResponseWriter, _ *http.Request, err error) {
			if errors.Is(err, embedded.ErrTargetForbidden) {
				http.Error(response, "host sign-in page", http.StatusUnauthorized)
				return
			}
			http.Error(response, "host error page", http.StatusTeapot)
		},
	})
	if result := host.send(t, http.MethodGet, "/dex-web/forbidden/", "", nil); result.status != http.StatusUnauthorized ||
		!strings.Contains(result.body, "host sign-in page") {
		t.Fatalf("host-written error = %d %s", result.status, result.body)
	}
}

func TestHandlerRejectsRequestsOutsideTheBasePath(t *testing.T) {
	host := newHostApplication(t, &embedded.Config{})
	handler, err := embedded.NewDexWebHandler(&embedded.Config{
		ResolveTarget: func(*http.Request) (embedded.Target, error) {
			return embedded.Target{FlowServiceClient: dexpb.NewFlowServiceClient(nil), BasePath: "/dex-web/server-a"}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, requestPath := range []string{"/dex-web/server-b/", "/elsewhere/api/flow-definitions", "/dex-web/server-a/../server-b/", "/dex-web/server-a//api/flow-definitions"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://127.0.0.1"+requestPath, nil))
		if response.Code != http.StatusNotFound {
			t.Fatalf("GET %s = %d", requestPath, response.Code)
		}
	}
	if result := host.send(t, http.MethodGet, "/dex-web/server-a/api/no-such-route", "", nil); result.status != http.StatusNotFound {
		t.Fatalf("unknown API route = %d", result.status)
	}
}

func TestHandlerKeepsCSRFProtectionForWrites(t *testing.T) {
	host := newHostApplication(t, &embedded.Config{})
	crossSite := map[string]string{"Content-Type": "application/json", "Sec-Fetch-Site": "cross-site", "Cookie": "session=host"}
	if result := host.send(t, http.MethodPost, "/dex-web/server-a/api/flows/stop", `{"flowId":"flow-1"}`, crossSite); result.status != http.StatusForbidden {
		t.Fatalf("cross-site write = %d %s", result.status, result.body)
	}
	sameSite := map[string]string{"Content-Type": "application/json", "Sec-Fetch-Site": "same-site"}
	if result := host.send(t, http.MethodPost, "/dex-web/server-a/api/flows/stop", `{"flowId":"flow-1"}`, sameSite); result.status != http.StatusForbidden {
		t.Fatalf("same-site write from another origin = %d %s", result.status, result.body)
	}
	form := map[string]string{"Content-Type": "application/x-www-form-urlencoded"}
	if result := host.send(t, http.MethodPost, "/dex-web/server-a/api/flows/stop", `flowId=flow-1`, form); result.status != http.StatusUnsupportedMediaType {
		t.Fatalf("form write = %d %s", result.status, result.body)
	}
	sameOrigin := map[string]string{"Content-Type": "application/json", "Sec-Fetch-Site": "same-origin"}
	if result := host.send(t, http.MethodPost, "/dex-web/server-a/api/flows/stop", `{"flowId":"flow-1","stopType":1}`, sameOrigin); result.status != http.StatusOK {
		t.Fatalf("same-origin write = %d %s", result.status, result.body)
	}
	if request := <-host.serverA.stopRequests; request.GetFlowId() != "flow-1" {
		t.Fatalf("stop request = %v", request)
	}
}

func TestHandlerHasNoLocalConnectorConfiguration(t *testing.T) {
	host := newHostApplication(t, &embedded.Config{})
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/api/v2/connector-connections"},
		{http.MethodGet, "/api/v2/connector-catalog"},
		{http.MethodPost, "/api/v2/connector-connections"},
		{http.MethodGet, "/api/v2/connector-oauth/callback?state=x&code=y"},
	} {
		headers := map[string]string{"Content-Type": "application/json", "Sec-Fetch-Site": "same-origin"}
		result := host.send(t, route.method, "/dex-web/server-a"+route.path, `{}`, headers)
		if result.status != http.StatusNotFound || !strings.Contains(result.body, "CONNECTOR_SETUP_UNAVAILABLE") {
			t.Fatalf("%s %s = %d %s", route.method, route.path, result.status, result.body)
		}
	}
}

func TestHandlerRefusesWorkerHealthProbes(t *testing.T) {
	host := newHostApplication(t, &embedded.Config{})
	headers := map[string]string{"Content-Type": "application/json", "Sec-Fetch-Site": "same-origin"}
	result := host.send(t, http.MethodPost, "/dex-web/server-a/api/v2/worker-health", `{"workerTargetAddress":"10.0.0.1:22"}`, headers)
	if result.status != http.StatusNotFound || !strings.Contains(result.body, "WORKER_HEALTH_UNAVAILABLE") {
		t.Fatalf("worker health probe = %d %s", result.status, result.body)
	}
}

func TestHandlerOptionsStayWithTheirHandler(t *testing.T) {
	framed := newHostApplication(t, &embedded.Config{FrameAncestors: []string{"'self'", "https://host.example"}, AllowedHosts: []string{"host.example"}})
	plain := newHostApplication(t, &embedded.Config{})
	framedPage := framed.send(t, http.MethodGet, "/dex-web/server-a/", "", nil)
	plainPage := plain.send(t, http.MethodGet, "/dex-web/server-a/", "", nil)
	if framedPage.header.Get("Content-Security-Policy") != "frame-ancestors 'self' https://host.example" ||
		framedPage.header.Get("X-Frame-Options") != "" ||
		plainPage.header.Get("Content-Security-Policy") != "frame-ancestors 'none'" {
		t.Fatalf("framing headers: framed=%v plain=%v", framedPage.header, plainPage.header)
	}
	rebinding := map[string]string{"Host": "attacker.example"}
	request, err := http.NewRequest(http.MethodGet, framed.http.URL+"/dex-web/server-a/api/flow-definitions", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Host = rebinding["Host"]
	result, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if err := result.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if result.StatusCode != http.StatusForbidden {
		t.Fatalf("allowlisted handler accepted another Host: %d", result.StatusCode)
	}
	plainRequest, err := http.NewRequest(http.MethodGet, plain.http.URL+"/dex-web/server-a/api/flow-definitions", nil)
	if err != nil {
		t.Fatal(err)
	}
	plainRequest.Host = rebinding["Host"]
	plainResult, err := http.DefaultClient.Do(plainRequest)
	if err != nil {
		t.Fatal(err)
	}
	if err := plainResult.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if plainResult.StatusCode != http.StatusOK {
		t.Fatalf("default handler checked Host: %d", plainResult.StatusCode)
	}
}

func TestNewDexWebHandlerValidatesConfig(t *testing.T) {
	if _, err := embedded.NewDexWebHandler(&embedded.Config{}); err == nil {
		t.Fatal("missing resolver was accepted")
	}
	resolve := func(*http.Request) (embedded.Target, error) { return embedded.Target{}, embedded.ErrTargetNotFound }
	if _, err := embedded.NewDexWebHandler(&embedded.Config{ResolveTarget: resolve, FrameAncestors: []string{"*"}}); err == nil {
		t.Fatal("wildcard frame ancestor was accepted")
	}
}

func TestIsMutatingRequest(t *testing.T) {
	for _, testCase := range []struct {
		method, path string
		isMutating   bool
	}{
		{http.MethodGet, "/dex-web/a/api/flows/summary", false},
		{http.MethodHead, "/dex-web/a/", false},
		{http.MethodPost, "/dex-web/a/api/flows/search", false},
		{http.MethodPost, "/dex-web/a/api/v2/search", false},
		{http.MethodPost, "/dex-web/a/api/blobs/load", false},
		{http.MethodPost, "/dex-web/a/api/flows/stop", true},
		{http.MethodPost, "/dex-web/a/api/v2/start", true},
		{http.MethodPost, "/dex-web/a/api/v2/actions", true},
		{http.MethodPost, "/dex-web/a/api/v2/agent-sessions/start", true},
		{http.MethodPost, "/dex-web/a/api/v2/agent-sessions/messages", true},
		{http.MethodPost, "/dex-web/a/api/v2/agent-sessions/settings", true},
		{http.MethodPatch, "/dex-web/a/api/v2/display", true},
		{http.MethodPut, "/dex-web/a/api/flows/attributes", true},
		{http.MethodDelete, "/dex-web/a/api/flows/channels/messages", true},
		{http.MethodPost, "/dex-web/a/api/v2/some-future-route", true},
		{http.MethodPost, "/elsewhere/api/flows/search", true},
	} {
		request := httptest.NewRequest(testCase.method, testCase.path, nil)
		if got := embedded.IsMutatingRequest(request, "/dex-web/a/"); got != testCase.isMutating {
			t.Fatalf("%s %s mutating = %v", testCase.method, testCase.path, got)
		}
	}
}

func TestHandlerKeepsItsOwnCopyOfConfig(t *testing.T) {
	_, clientA := startDexServer(t, "server-a", dexpb.ServerEngine_SERVER_ENGINE_ZD)
	_, clientB := startDexServer(t, "server-b", dexpb.ServerEngine_SERVER_ENGINE_ZD)
	resolverFor := func(client dexpb.FlowServiceClient) embedded.TargetResolver {
		return func(request *http.Request) (embedded.Target, error) {
			if request.PathValue("serverID") == "missing" {
				return embedded.Target{}, embedded.ErrTargetNotFound
			}
			return embedded.Target{FlowServiceClient: client, BasePath: "/dex-web/" + request.PathValue("serverID")}, nil
		}
	}
	errorWriterFor := func(page string) embedded.ResolveErrorWriter {
		return func(response http.ResponseWriter, _ *http.Request, _ error) {
			http.Error(response, page, http.StatusNotFound)
		}
	}
	cfg := &embedded.Config{
		ResolveTarget:     resolverFor(clientA),
		WriteResolveError: errorWriterFor("first error page"),
		FrameAncestors:    []string{"https://first.example"},
		AllowedHosts:      []string{"first.example"},
	}
	first, err := embedded.NewDexWebHandler(cfg)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ResolveTarget = resolverFor(clientB)
	cfg.WriteResolveError = errorWriterFor("second error page")
	cfg.FrameAncestors[0] = "https://second.example"
	cfg.AllowedHosts[0] = "second.example"
	second, err := embedded.NewDexWebHandler(cfg)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ResolveTarget = nil
	cfg.WriteResolveError = nil
	cfg.FrameAncestors[0] = "https://third.example"
	cfg.AllowedHosts[0] = "third.example"

	for _, testCase := range []struct {
		handler     http.Handler
		host        string
		wantFlow    string
		wantError   string
		wantFraming string
	}{
		{handler: first, host: "first.example", wantFlow: "server-a-flow", wantError: "first error page", wantFraming: "frame-ancestors https://first.example"},
		{handler: second, host: "second.example", wantFlow: "server-b-flow", wantError: "second error page", wantFraming: "frame-ancestors https://second.example"},
	} {
		mux := http.NewServeMux()
		mux.Handle("/dex-web/{serverID}/", testCase.handler)
		search := httptest.NewRequest(http.MethodPost, "http://"+testCase.host+"/dex-web/x/api/flows/search", strings.NewReader(`{}`))
		search.Header.Set("Content-Type", "application/json")
		searchResponse := httptest.NewRecorder()
		mux.ServeHTTP(searchResponse, search)
		if searchResponse.Code != http.StatusOK || !strings.Contains(searchResponse.Body.String(), testCase.wantFlow) ||
			searchResponse.Header().Get("Content-Security-Policy") != testCase.wantFraming {
			t.Fatalf("search on %s = %d %v %s", testCase.host, searchResponse.Code, searchResponse.Header(), searchResponse.Body)
		}
		missingResponse := httptest.NewRecorder()
		mux.ServeHTTP(missingResponse, httptest.NewRequest(http.MethodGet, "http://"+testCase.host+"/dex-web/missing/", nil))
		if !strings.Contains(missingResponse.Body.String(), testCase.wantError) {
			t.Fatalf("resolver error on %s = %d %s", testCase.host, missingResponse.Code, missingResponse.Body)
		}
		otherHostResponse := httptest.NewRecorder()
		mux.ServeHTTP(otherHostResponse, httptest.NewRequest(http.MethodGet, "http://third.example/dex-web/x/api/flow-definitions", nil))
		if otherHostResponse.Code != http.StatusForbidden {
			t.Fatalf("Host added to the Config after construction = %d", otherHostResponse.Code)
		}
	}
}

func TestHandlerSetsFramingHeadersOnEveryResponse(t *testing.T) {
	for _, framing := range []struct {
		name           string
		frameAncestors []string
		wantPolicy     string
		wantOptions    string
	}{
		{name: "default", wantPolicy: "frame-ancestors 'none'", wantOptions: "DENY"},
		{name: "self", frameAncestors: []string{"'self'"}, wantPolicy: "frame-ancestors 'self'", wantOptions: "SAMEORIGIN"},
		{name: "allowlist", frameAncestors: []string{"'self'", "https://host.example"}, wantPolicy: "frame-ancestors 'self' https://host.example"},
	} {
		for _, hasHostErrorPage := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/hostErrorPage=%t", framing.name, hasHostErrorPage), func(t *testing.T) {
				handler := newFramingTestHandler(t, framing.frameAncestors, hasHostErrorPage)
				// Host middleware that denies framing and scripts for its own pages runs before Dex Web.
				mux := http.NewServeMux()
				mux.Handle("/dex-web/{serverID}", handler)
				mux.Handle("/dex-web/{serverID}/", handler)
				hostDeniesFraming := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
					response.Header().Set("Content-Security-Policy", "default-src 'none'")
					response.Header().Set("X-Frame-Options", "DENY")
					mux.ServeHTTP(response, request)
				})
				for requestPath, wantStatus := range map[string]int{
					"/dex-web/server-a":                    http.StatusMovedPermanently,
					"/dex-web/server-a/":                   http.StatusOK,
					"/dex-web/forbidden/":                  http.StatusForbidden,
					"/dex-web/broken/api/flow-definitions": http.StatusInternalServerError,
					"/dex-web/bad-base-path/":              http.StatusInternalServerError,
				} {
					wantPolicy, wantOptions := framing.wantPolicy, framing.wantOptions
					if hasHostErrorPage && (requestPath == "/dex-web/forbidden/" || requestPath == "/dex-web/broken/api/flow-definitions") {
						// The host's error page keeps the host's headers in every framing mode.
						wantStatus, wantPolicy, wantOptions = http.StatusTeapot, "default-src 'none'", "DENY"
					}
					response := httptest.NewRecorder()
					hostDeniesFraming.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://127.0.0.1"+requestPath, nil))
					if response.Code != wantStatus ||
						response.Header().Get("Content-Security-Policy") != wantPolicy ||
						response.Header().Get("X-Frame-Options") != wantOptions {
						t.Fatalf("GET %s = %d %v %s", requestPath, response.Code, response.Header(), response.Body)
					}
				}
			})
		}
	}
}

// newFramingTestHandler resolves "forbidden", "broken", and "bad-base-path" to errors and every other ID to a target.
func newFramingTestHandler(t *testing.T, frameAncestors []string, hasHostErrorPage bool) http.Handler {
	t.Helper()
	cfg := &embedded.Config{
		FrameAncestors: frameAncestors,
		ResolveTarget: func(request *http.Request) (embedded.Target, error) {
			client := dexpb.NewFlowServiceClient(nil)
			switch serverID := request.PathValue("serverID"); serverID {
			case "forbidden":
				return embedded.Target{}, embedded.ErrTargetForbidden
			case "broken":
				return embedded.Target{}, errors.New("database unavailable")
			case "bad-base-path":
				return embedded.Target{FlowServiceClient: client, BasePath: "dex-web"}, nil
			default:
				return embedded.Target{FlowServiceClient: client, BasePath: "/dex-web/" + serverID}, nil
			}
		},
	}
	if hasHostErrorPage {
		cfg.WriteResolveError = func(response http.ResponseWriter, _ *http.Request, _ error) {
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			response.WriteHeader(http.StatusTeapot)
			if _, err := io.WriteString(response, "<p>host error page</p>"); err != nil {
				t.Error(err)
			}
		}
	}
	handler, err := embedded.NewDexWebHandler(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}
