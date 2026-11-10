// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// Package embedded serves Dex Web from a host application's own Go HTTP server.
//
// One handler serves Dex Web for many Dex Servers. The host mounts it under a path prefix on its own origin,
// such as /dex-web/{serverID}/. For every request, the handler asks the host's TargetResolver which Dex Server
// the request reaches, and serves the pages, assets, and /api routes of that Dex Server under its base path.
//
// The handler does no authentication or authorization. The host must authenticate every request and decide
// whether the caller may use the Dex Server before the handler runs, usually in its resolver or in middleware
// in front of the handler. The resolver must return only a client the caller may use. Use IsMutatingRequest to
// allow read-only callers to view but not change Flows.
//
// The handler shares no state between Dex Servers. It keeps no client, cache, or readiness result after a
// request ends, and it never closes a client. The host owns each FlowService client: it creates, caches, and
// closes them. Local Connector configuration and Worker health probes are not available; their /api routes
// return HTTP 404.
//
// Mutating /api requests must be same-origin and must send Content-Type: application/json, which stops
// cross-site request forgery even when the host authenticates with cookies.
//
// Example:
//
//	connections := map[string]*grpc.ClientConn{} // created at startup, closed at shutdown
//	handler, err := embedded.NewDexWebHandler(&embedded.Config{
//		ResolveTarget: func(request *http.Request) (embedded.Target, error) {
//			serverID := request.PathValue("serverID")
//			connection, ok := connections[serverID]
//			if !ok {
//				return embedded.Target{}, embedded.ErrTargetNotFound
//			}
//			if !mayUseDexServer(request, serverID) {
//				return embedded.Target{}, embedded.ErrTargetForbidden
//			}
//			return embedded.Target{
//				FlowServiceClient: dexpb.NewFlowServiceClient(connection),
//				BasePath:          "/dex-web/" + serverID,
//			}, nil
//		},
//	})
//	if err != nil {
//		return err
//	}
//	mux.Handle("/dex-web/{serverID}/", authenticate(handler))
//
// Since 1.43.0.
package embedded

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"github.com/superdurable/dex/web"
	"github.com/superdurable/dex/web/api"
	"github.com/superdurable/dex/web/assets"
)

// ErrTargetNotFound tells the handler that the request names no Dex Server. The handler answers HTTP 404.
//
// Since 1.43.0.
var ErrTargetNotFound = errors.New("Dex Web target not found")

// ErrTargetForbidden tells the handler that the caller may not use the Dex Server. The handler answers HTTP 403.
//
// Since 1.43.0.
var ErrTargetForbidden = errors.New("Dex Web target forbidden")

// ErrTargetUnavailable tells the handler that the Dex Server cannot be reached now. The handler answers HTTP 503.
//
// Since 1.43.0.
var ErrTargetUnavailable = errors.New("Dex Web target unavailable")

// Target is the Dex Server that one request reaches.
//
// The handler uses a Target only while it serves the request it was resolved for.
//
// Since 1.43.0.
type Target struct {
	// FlowServiceClient is required. It is the Dex FlowService client for this Dex Server, and it must already
	// carry the credentials, for example through grpc.WithPerRPCCredentials or a client interceptor. Each RPC
	// receives a context derived from the HTTP request's context, so per-user credentials can read values that
	// host middleware stored in that context. The handler never closes the client.
	FlowServiceClient dexpb.FlowServiceClient
	// BasePath is required. It is the URL path prefix that serves this Dex Server's Dex Web, such as
	// "/dex-web/server-a". It must start with "/", a trailing "/" is ignored, and each segment may contain only
	// ASCII letters, digits, '.', '_', '~', and '-'. A request outside it gets HTTP 404; a GET of the prefix
	// without its trailing slash redirects to the prefix with one.
	BasePath string
	// FlowRenderingDirectory defaults empty, meaning no Flow Definition Graph files. Otherwise Dex Web reads the
	// JSON files under this directory on every definition request, which enables the Designs page, the Work
	// Queue, and typed Flow start forms for this Dex Server.
	FlowRenderingDirectory string
}

// TargetResolver returns the Dex Server that a request reaches.
//
// It runs once for every request, after the host's authentication. Return ErrTargetNotFound, ErrTargetForbidden,
// or ErrTargetUnavailable, or an error that wraps one, to answer HTTP 404, 403, or 503. Any other error answers
// HTTP 500. The response never contains the error text; the handler logs it with log/slog.
//
// Since 1.43.0.
type TargetResolver func(request *http.Request) (Target, error)

// ResolveErrorWriter writes the HTTP response for an error that a TargetResolver returned.
//
// Since 1.43.0.
type ResolveErrorWriter func(response http.ResponseWriter, request *http.Request, err error)

// Config configures a handler from NewDexWebHandler. The handler reads it only during construction.
//
// Since 1.43.0.
type Config struct {
	// ResolveTarget is required. It chooses the Dex Server for each request.
	ResolveTarget TargetResolver
	// WriteResolveError defaults to nil, which maps ResolveTarget errors to HTTP 404, 403, 503, or 500 with a
	// generic JSON body. Set it to write your own response, such as your application's error page.
	WriteResolveError ResolveErrorWriter
	// FrameAncestors defaults empty, which sends "Content-Security-Policy: frame-ancestors 'none'" and
	// "X-Frame-Options: DENY" with every page, so no page may frame Dex Web. Each entry is a CSP source that may
	// frame it: 'self' or an explicit host source such as https://host.example. A non-empty list replaces the CSP
	// sources and drops X-Frame-Options, except that the list 'self' alone sends "X-Frame-Options: SAMEORIGIN".
	FrameAncestors []string
	// AllowedHosts defaults empty, which accepts every Host header because the host server owns Host checks.
	// When it has an entry, /api requests must name a loopback name, an IP address, or one of these hosts in their
	// Host header; an entry may carry a port, which is ignored. Other Hosts get HTTP 403, which stops DNS rebinding.
	AllowedHosts []string
}

type dexWebHandler struct {
	cfg  *Config
	site *web.EmbeddedSite
}

// NewDexWebHandler returns an http.Handler that serves Dex Web for the Dex Server each request resolves to.
//
// Mount it at a path pattern that covers every Target.BasePath, such as "/dex-web/{serverID}/". The handler
// serves the pages, assets, /api routes, /healthz, and /readyz under each base path. /readyz reports only the
// resolved Dex Server. The handler panics when cfg is nil. It returns an error when ResolveTarget is nil or
// FrameAncestors has an invalid entry.
//
// Since 1.43.0.
func NewDexWebHandler(cfg *Config) (http.Handler, error) {
	if cfg == nil {
		panic("embedded Dex Web config must not be nil")
	}
	if cfg.ResolveTarget == nil {
		return nil, errors.New("embedded Dex Web config requires ResolveTarget")
	}
	site, err := web.NewEmbeddedSite(&web.EmbeddedSiteConfig{
		FrameAncestors: cfg.FrameAncestors,
		AllowedHosts:   cfg.AllowedHosts,
	}, assets.Files)
	if err != nil {
		return nil, err
	}
	return &dexWebHandler{cfg: cfg, site: site}, nil
}

func (handler *dexWebHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	target, err := handler.cfg.ResolveTarget(request)
	if err != nil {
		handler.writeResolveError(response, request, err)
		return
	}
	handler.site.ServeTarget(response, request, web.EmbeddedSiteTarget{
		FlowServiceClient:      target.FlowServiceClient,
		BasePath:               target.BasePath,
		FlowRenderingDirectory: target.FlowRenderingDirectory,
	})
}

func (handler *dexWebHandler) writeResolveError(response http.ResponseWriter, request *http.Request, err error) {
	if handler.cfg.WriteResolveError != nil {
		handler.cfg.WriteResolveError(response, request, err)
		return
	}
	switch {
	case errors.Is(err, ErrTargetNotFound):
		api.WriteCodedError(response, http.StatusNotFound, "TARGET_NOT_FOUND", "Dex Server not found")
	case errors.Is(err, ErrTargetForbidden):
		api.WriteCodedError(response, http.StatusForbidden, "TARGET_FORBIDDEN", "Access to this Dex Server is forbidden")
	case errors.Is(err, ErrTargetUnavailable):
		api.WriteCodedError(response, http.StatusServiceUnavailable, "TARGET_UNAVAILABLE", "Dex Server is unavailable")
	default:
		slog.Error("Dex Web target resolution failed", "error", err)
		api.WriteCodedError(response, http.StatusInternalServerError, "TARGET_RESOLUTION_FAILED", "Dex Server could not be resolved")
	}
}

// IsMutatingRequest reports whether a Dex Web request may change Flows, so the host can refuse it for a
// read-only caller before the handler runs.
//
// basePath is the request's Target.BasePath. Every method other than GET, HEAD, and OPTIONS mutates, except
// three read-only POST routes under the base path: /api/flows/search, /api/v2/search, and /api/blobs/load.
// A request with an unsafe method outside the base path also counts as mutating.
//
// It returns true when the request needs write permission.
//
// Since 1.43.0.
func IsMutatingRequest(request *http.Request, basePath string) bool {
	canonicalBasePath, err := web.ValidateBasePath(basePath)
	if err != nil {
		return web.IsMutatingAPIRequest(request.Method, "")
	}
	apiPath := request.URL.Path
	if canonicalBasePath != "/" {
		var isUnderBasePath bool
		apiPath, isUnderBasePath = strings.CutPrefix(apiPath, canonicalBasePath)
		if !isUnderBasePath {
			return web.IsMutatingAPIRequest(request.Method, "")
		}
	}
	return web.IsMutatingAPIRequest(request.Method, apiPath)
}
