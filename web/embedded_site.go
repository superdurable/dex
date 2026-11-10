// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package web

import (
	"io/fs"
	"log/slog"
	"net/http"

	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"github.com/superdurable/dex/web/api"
)

// EmbeddedSiteConfig configures Dex Web pages that a host HTTP server serves for many Dex Servers.
// It is read once by NewEmbeddedSite.
type EmbeddedSiteConfig struct {
	// FrameAncestors defaults empty, which forbids framing. It has the same meaning as Config.FrameAncestors.
	FrameAncestors []string
	// AllowedHosts defaults empty, which accepts every Host because the host server owns Host checks. When it has
	// an entry, /api requests must name a loopback name, an IP address, or one of these hosts; an entry may carry
	// a port, which is ignored. Other Hosts get HTTP 403.
	AllowedHosts []string
}

// EmbeddedSiteTarget is the Dex Server that one request reaches. Dex Web keeps no part of it after the request.
type EmbeddedSiteTarget struct {
	// FlowServiceClient is required and already carries the credentials for the Dex Server.
	FlowServiceClient dexpb.FlowServiceClient
	// BasePath is the URL path prefix of this Dex Web, such as "/dex-web/server-a"; ValidateBasePath lists its form.
	BasePath string
	// FlowRenderingDirectory defaults empty, meaning no Flow Definition Graph files; otherwise Dex Web reads the
	// JSON files under it on every definition request.
	FlowRenderingDirectory string
}

// EmbeddedSite serves Dex Web for a target chosen per request. It shares only immutable pages between targets.
type EmbeddedSite struct {
	pages      *sitePages
	protection apiRequestProtection
}

// NewEmbeddedSite builds the shared pages from assets, whose "dist" directory holds the built SPA.
// It returns an error when cfg.FrameAncestors is invalid.
func NewEmbeddedSite(cfg *EmbeddedSiteConfig, assets fs.FS) (*EmbeddedSite, error) {
	if cfg == nil {
		panic("embedded Web config must not be nil")
	}
	pages, err := newSitePages(assets, cfg.FrameAncestors)
	if err != nil {
		return nil, err
	}
	return &EmbeddedSite{pages: pages, protection: newAPIRequestProtection(newAllowedHostsPolicy(cfg.AllowedHosts))}, nil
}

// ServeTarget serves one request against target. Routes, caches, and readiness state live only for this request.
// The probes /healthz and /readyz are served under target.BasePath and report only this target.
func (site *EmbeddedSite) ServeTarget(response http.ResponseWriter, request *http.Request, target EmbeddedSiteTarget) {
	if target.FlowServiceClient == nil {
		slog.Error("Dex Web target has no FlowService client")
		api.WriteCodedError(response, http.StatusInternalServerError, "TARGET_INVALID", "Dex Web target is invalid")
		return
	}
	basePath, err := ValidateBasePath(target.BasePath)
	if err != nil {
		slog.Error("Dex Web target has an invalid base path", "error", err)
		api.WriteCodedError(response, http.StatusInternalServerError, "TARGET_INVALID", "Dex Web target is invalid")
		return
	}
	flowDefinitions, err := NewDirectoryFlowDefinitionProvider(target.FlowRenderingDirectory)
	if err != nil {
		slog.Error("Dex Web target has an invalid Flow rendering directory", "error", err)
		api.WriteCodedError(response, http.StatusInternalServerError, "TARGET_INVALID", "Dex Web target is invalid")
		return
	}
	routes := site.pages.newTargetRouteMux(targetRoutes{
		client: target.FlowServiceClient, flowDefinitions: flowDefinitions, basePath: basePath,
	})
	serveUnderBasePath(response, request, basePath, site.protection.wrap(refuseWorkerHealthProbes(routes)))
}

// refuseWorkerHealthProbes keeps callers from making the host process dial addresses inside its network.
func refuseWorkerHealthProbes(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/api/v2/worker-health" {
			api.WriteCodedError(response, http.StatusNotFound, "WORKER_HEALTH_UNAVAILABLE", "Worker health probes are not available in embedded Dex Web")
			return
		}
		next.ServeHTTP(response, request)
	})
}

// IsMutatingAPIRequest reports whether a request may change Flows or Connector configuration.
//
// apiPath is the request path relative to the Dex Web base path, such as "/api/flows/stop". Every method other
// than GET, HEAD, and OPTIONS mutates, except these read-only searches and loads:
// POST /api/flows/search, POST /api/v2/search, and POST /api/blobs/load.
// An unknown path with an unsafe method counts as mutating.
//
// It returns true when the request needs write permission.
func IsMutatingAPIRequest(method string, apiPath string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	case http.MethodPost:
		switch apiPath {
		case "/api/flows/search", "/api/v2/search", "/api/blobs/load":
			return false
		}
	}
	return true
}
