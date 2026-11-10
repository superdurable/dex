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
