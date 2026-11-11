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
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"github.com/superdurable/dex/web/api"
	"google.golang.org/protobuf/types/known/emptypb"
)

const DefaultPort = 8802

type Config struct {
	// BindAddress defaults to 127.0.0.1 and controls the HTTP bind IP.
	BindAddress string
	// Port defaults to 8802 and controls the HTTP bind port.
	Port int
	// FlowRenderingDirectory defaults empty and supplies Flow Definition Graph JSON files to Dex Web.
	FlowRenderingDirectory string
	// ConnectorSetupEnabled defaults false and enables Connector configuration APIs.
	ConnectorSetupEnabled bool
	// ConnectorConfigDirectory defaults empty and stores local Connector configuration and verified UI artifacts.
	ConnectorConfigDirectory string
	// ConnectorReleaseOverrides maps Connector IDs to local release artifact directories for loopback development.
	ConnectorReleaseOverrides map[string]string
	// AllowedHosts defaults empty and lists more host names that /api requests may name in their Host header, such
	// as a reverse proxy's public name; an entry may carry a port, which is ignored. When it has an entry, or when
	// ConnectorSetupEnabled is true, Dex Web checks the Host of every /api request: loopback names, IP addresses,
	// BindAddress, and these entries are accepted, and any other Host is refused with HTTP 403, which stops DNS
	// rebinding. Otherwise every Host is accepted.
	AllowedHosts []string
	// BasePath defaults to "/" and is the URL path prefix that serves the pages, assets, and /api routes, such as
	// "/dex". An empty value means "/". ValidateBasePath lists the accepted form. Requests outside the prefix get
	// HTTP 404, except /healthz and /readyz, which also stay at the root for probes.
	BasePath string
	// FrameAncestors defaults empty, which sends "frame-ancestors 'none'" and "X-Frame-Options: DENY" with every
	// response, including redirects and errors, so no page may frame Dex Web. Each entry is a CSP source that may
	// frame it, such as 'self' or https://host.example; ValidateFrameAncestors lists the accepted form. A non-empty
	// list replaces the CSP sources and drops X-Frame-Options, except that the list 'self' alone sends
	// "X-Frame-Options: SAMEORIGIN". Connector configuration UI assets, which Dex Web frames, send no
	// X-Frame-Options.
	FrameAncestors []string
}

type Server struct {
	cfg        *Config
	httpServer *http.Server
	// cancelRequestContexts ends in-flight long polls so Shutdown finishes promptly.
	cancelRequestContexts context.CancelFunc
}

func NewServer(cfg *Config, client dexpb.FlowServiceClient, assets fs.FS) (*Server, error) {
	if cfg == nil {
		panic("Web config must not be nil")
	}
	if client == nil {
		panic("Dex FlowService client must not be nil")
	}
	if assets == nil {
		panic("Web assets must not be nil")
	}
	flowDefinitions, err := newFlowDefinitionSources(cfg.FlowRenderingDirectory, client)
	if err != nil {
		return nil, err
	}
	return newServer(cfg, client, assets, flowDefinitions)
}

// NewFlowRenderingServer serves one in-memory Flow Definition Graph without a Dex FlowService connection.
func NewFlowRenderingServer(cfg *Config, graph []byte, assets fs.FS) (*Server, error) {
	if cfg == nil {
		panic("Web config must not be nil")
	}
	if assets == nil {
		panic("Web assets must not be nil")
	}
	flowDefinitions, err := newVisualizeFlowDefinitionSources(graph)
	if err != nil {
		return nil, err
	}
	return newServer(cfg, nil, assets, flowDefinitions)
}

func newServer(cfg *Config, client dexpb.FlowServiceClient, assets fs.FS, flowDefinitions flowDefinitionLookup) (*Server, error) {
	basePath, err := ValidateBasePath(cfg.BasePath)
	if err != nil {
		return nil, err
	}
	pages, err := newSitePages(assets, cfg.FrameAncestors)
	if err != nil {
		return nil, err
	}
	connectorSetup, err := newConnectorSetup(cfg, flowDefinitions)
	if err != nil {
		return nil, err
	}
	routes := pages.newTargetRouteMux(targetRoutes{
		client: client, flowDefinitions: flowDefinitions, connectorSetup: connectorSetup, basePath: basePath,
	})
	protectedRoutes := newAPIRequestProtection(newRequestHostPolicy(cfg)).wrap(routes)
	requestBaseContext, cancelRequestContexts := context.WithCancel(context.Background())
	return &Server{
		cfg: cfg,
		httpServer: &http.Server{
			Handler:           newStandaloneBasePathHandler(basePath, pages.framePolicy, protectedRoutes),
			ReadTimeout:       30 * time.Second,
			ReadHeaderTimeout: 10 * time.Second,
			IdleTimeout:       90 * time.Second,
			BaseContext:       func(net.Listener) context.Context { return requestBaseContext },
		},
		cancelRequestContexts: cancelRequestContexts,
	}, nil
}

// newStandaloneBasePathHandler serves next under basePath and keeps the probes at the root for orchestrators.
// Every response, including redirects and 404s outside basePath, carries the framing headers.
func newStandaloneBasePathHandler(basePath string, framing framePolicy, next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		framing.apply(response.Header())
		if basePath != "/" && (request.URL.Path == "/healthz" || request.URL.Path == "/readyz") {
			next.ServeHTTP(response, request)
			return
		}
		serveUnderBasePath(response, request, basePath, next)
	})
}

// targetRoutes is everything the routes of one Dex Web need: one FlowService client and its definitions.
type targetRoutes struct {
	// client is nil for a Flow rendering server, which has no FlowService.
	client          dexpb.FlowServiceClient
	flowDefinitions flowDefinitionLookup
	// connectorSetup is nil when local Connector setup is disabled.
	connectorSetup *connectorSetup
	basePath       string
}

// newTargetRouteMux registers the API, probe, and page routes; paths are relative to the base path.
func (pages *sitePages) newTargetRouteMux(routes targetRoutes) *http.ServeMux {
	mux := http.NewServeMux()
	if routes.client != nil {
		api.RegisterHandlers(mux, routes.client)
		api.RegisterDynamicV2Handlers(mux, routes.client, api.V2DefinitionLoader(func(ctx context.Context) (api.V2DefinitionSnapshot, error) {
			snapshot, loadErr := routes.flowDefinitions.Load(ctx)
			if loadErr != nil {
				return api.V2DefinitionSnapshot{}, loadErr
			}
			return v2DefinitionSnapshot(snapshot), nil
		}), api.V2HandlerConfig{
			FlowTypeDefinitionLoader: func(ctx context.Context, flowTypes []string) (api.V2DefinitionSnapshot, error) {
				snapshot, loadErr := routes.flowDefinitions.loadFlowTypes(ctx, flowTypes)
				if loadErr != nil {
					return api.V2DefinitionSnapshot{}, loadErr
				}
				return v2DefinitionSnapshot(snapshot), nil
			},
		})
	}
	mux.HandleFunc("GET /api/flow-definitions", serveFlowDefinitions(routes.flowDefinitions))
	mux.HandleFunc("GET /api/flow-definitions/{flowType}", serveFlowTypeDefinitions(routes.flowDefinitions))
	mux.HandleFunc("GET /api/flow-definition-types", serveFlowDefinitionTypes(routes.flowDefinitions))
	if routes.connectorSetup != nil {
		routes.connectorSetup.registerHandlers(mux)
	}
	mux.HandleFunc("GET /readyz", readinessHandler(routes.client, routes.flowDefinitions))
	mux.Handle("/", pages.spaHandler(routes.basePath, routes.connectorSetup != nil))
	return mux
}

// apiRequestProtection guards /api routes against DNS rebinding and cross-origin writes.
type apiRequestProtection struct {
	crossOriginProtection *http.CrossOriginProtection
	hostPolicy            requestHostPolicy
}

func newAPIRequestProtection(hostPolicy requestHostPolicy) apiRequestProtection {
	return apiRequestProtection{crossOriginProtection: http.NewCrossOriginProtection(), hostPolicy: hostPolicy}
}

// wrap checks requests whose path, relative to the base path, starts with /api/.
func (protection apiRequestProtection) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		isAPIRequest := strings.HasPrefix(request.URL.Path, "/api/")
		if isAPIRequest && !protection.hostPolicy.allows(request.Host) {
			api.WriteCodedError(response, http.StatusForbidden, "HOST_NOT_ALLOWED", "Dex Web does not accept this Host; add it to the allowed hosts")
			return
		}
		if !isAPIRequest ||
			request.Method == http.MethodGet || request.Method == http.MethodHead || request.Method == http.MethodOptions {
			next.ServeHTTP(response, request)
			return
		}
		if err := protection.crossOriginProtection.Check(request); err != nil {
			api.WriteError(response, http.StatusForbidden, "API requests must be same-origin", nil)
			return
		}
		mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
		if err != nil || mediaType != "application/json" {
			api.WriteError(response, http.StatusUnsupportedMediaType, "Content-Type must be application/json", nil)
			return
		}
		next.ServeHTTP(response, request)
	})
}

func (s *Server) Run() error {
	address := s.cfg.BindAddress
	if address == "" {
		address = "127.0.0.1"
	}
	port := s.cfg.Port
	if port == 0 {
		port = DefaultPort
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(address, fmt.Sprintf("%d", port)))
	if err != nil {
		return err
	}
	return s.Serve(listener)
}

func (s *Server) Serve(listener net.Listener) error {
	return s.httpServer.Serve(listener)
}

// Shutdown cancels in-flight request contexts, then waits for handlers to return or ctx to expire.
func (s *Server) Shutdown(ctx context.Context) error {
	s.cancelRequestContexts()
	return s.httpServer.Shutdown(ctx)
}

func (s *Server) Handler() http.Handler {
	return s.httpServer.Handler
}

func readinessHandler(client dexpb.FlowServiceClient, provider flowDefinitionLookup) http.HandlerFunc {
	var serverEngineResolver *api.ServerEngineResolver
	if client != nil {
		serverEngineResolver = api.NewServerEngineResolver(client)
	}
	return func(response http.ResponseWriter, request *http.Request) {
		// Readiness lists Flow types instead of reading every definition, so a probe stays cheap.
		list, err := provider.listFlowTypes(request.Context())
		if err != nil {
			writeFlowDefinitionSourceError(response, err)
			return
		}
		if client != nil {
			if err := checkFlowServiceReadiness(request.Context(), client, serverEngineResolver); err != nil {
				api.WriteCodedError(response, http.StatusServiceUnavailable, "FLOW_SERVICE_UNAVAILABLE", "Dex FlowService is unavailable")
				return
			}
		}
		revision := list.revision()
		setSnapshotETag(response, revision)
		writeWebJSON(response, http.StatusOK, map[string]interface{}{
			"status":             "ready",
			"definitionRevision": revision,
			"source":             list.source,
			"definitionCount":    len(list.FlowTypes),
		})
	}
}

// checkFlowServiceReadiness searches on the Go Server because its HealthCheck does not reach Temporal or Cadence.
func checkFlowServiceReadiness(
	ctx context.Context,
	client dexpb.FlowServiceClient,
	serverEngineResolver *api.ServerEngineResolver,
) error {
	if serverEngineResolver.ResolveServerEngine(ctx) == dexpb.ServerEngine_SERVER_ENGINE_ZD {
		// A server-zd search can exceed a builtin visibility scan budget.
		_, err := client.HealthCheck(ctx, &emptypb.Empty{})
		return err
	}
	_, err := client.SearchFlows(ctx, &dexpb.SearchFlowsRequest{PageSize: 1})
	return err
}

func writeFlowDefinitionSourceError(response http.ResponseWriter, err error) {
	code := flowDefinitionSourceUnavailable
	message := "Flow Definition source is unavailable"
	var coded interface{ DefinitionErrorCode() string }
	if errors.As(err, &coded) {
		code = coded.DefinitionErrorCode()
		if code == flowDefinitionSourceInvalid {
			message = "Flow Definition source is invalid"
		}
	}
	api.WriteCodedError(response, http.StatusServiceUnavailable, code, message)
}

func setSnapshotETag(response http.ResponseWriter, revision string) {
	if revision != "" {
		response.Header().Set("ETag", fmt.Sprintf("%q", revision))
	}
}

func writeWebJSON(response http.ResponseWriter, statusCode int, value interface{}) {
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.WriteHeader(statusCode)
	if err := json.NewEncoder(response).Encode(value); err != nil {
		panic(fmt.Sprintf("encode HTTP response: %v", err))
	}
}
