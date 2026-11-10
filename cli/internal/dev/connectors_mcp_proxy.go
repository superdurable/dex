// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package dev

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync/atomic"
	"time"

	dexweb "github.com/superdurable/dex/web"
)

// connectorsMCPProxy forwards /mcp to the current Connectors MCP server, or answers HTTP 503 while there is none.
type connectorsMCPProxy struct {
	state                 atomic.Pointer[connectorsMCPProxyState]
	crossOriginProtection *http.CrossOriginProtection
}

type connectorsMCPProxyState struct {
	// reverseProxy is nil while no server is available.
	reverseProxy      *httputil.ReverseProxy
	unavailableReason string
}

func newConnectorsMCPProxy() *connectorsMCPProxy {
	proxy := &connectorsMCPProxy{crossOriginProtection: http.NewCrossOriginProtection()}
	proxy.setUnavailable("the Connectors MCP server is starting")
	return proxy
}

// setUpstream sends later requests to target, the server's /mcp URL.
func (p *connectorsMCPProxy) setUpstream(target *url.URL) {
	reverseProxy := &httputil.ReverseProxy{
		Rewrite: func(request *httputil.ProxyRequest) {
			request.SetURL(target)
			// SetURL joins paths; the endpoint is exactly the server's URL path.
			request.Out.URL.Path = target.Path
			request.Out.URL.RawPath = target.RawPath
			// ServeHTTP admits only loopback Host names, which the server accepts, and its Origin check compares them.
			request.Out.Host = request.In.Host
		},
		// Streamable HTTP responses may be event streams, which must not be buffered.
		FlushInterval: -1,
		ErrorHandler: func(response http.ResponseWriter, _ *http.Request, err error) {
			writeConnectorsMCPProxyError(response, http.StatusBadGateway, "the Connectors MCP server did not answer: "+err.Error())
		},
	}
	p.state.Store(&connectorsMCPProxyState{reverseProxy: reverseProxy})
}

func (p *connectorsMCPProxy) setUnavailable(reason string) {
	p.state.Store(&connectorsMCPProxyState{unavailableReason: reason})
}

// ServeHTTP accepts only loopback Host and Origin names, so neither DNS rebinding nor a web page reaches the tools.
func (p *connectorsMCPProxy) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if !isLoopbackRequestHost(requestHostName(request.Host)) {
		writeConnectorsMCPProxyError(response, http.StatusForbidden, "/mcp accepts only loopback Host names")
		return
	}
	if origin := request.Header.Get("Origin"); origin != "" && !isLoopbackHTTPOrigin(origin) {
		writeConnectorsMCPProxyError(response, http.StatusForbidden, "/mcp refuses cross-origin requests")
		return
	}
	if err := p.crossOriginProtection.Check(request); err != nil {
		writeConnectorsMCPProxyError(response, http.StatusForbidden, "/mcp refuses cross-origin requests")
		return
	}
	state := p.state.Load()
	if state.reverseProxy == nil {
		response.Header().Set("Retry-After", "1")
		writeConnectorsMCPProxyError(response, http.StatusServiceUnavailable, state.unavailableReason)
		return
	}
	state.reverseProxy.ServeHTTP(response, request)
}

func requestHostName(hostHeader string) string {
	if host, _, err := net.SplitHostPort(hostHeader); err == nil {
		return host
	}
	return hostHeader
}

func isLoopbackHTTPOrigin(origin string) bool {
	parsed, err := url.Parse(origin)
	return err == nil && parsed.Scheme == "http" && isLoopbackRequestHost(parsed.Hostname())
}

func writeConnectorsMCPProxyError(response http.ResponseWriter, status int, message string) {
	body, err := json.Marshal(map[string]string{"error": message})
	if err != nil {
		panic("encode a string map: " + err.Error())
	}
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	// The status line is already sent; a failed body write leaves nothing to report.
	_, _ = response.Write(body)
}

// webListenerServer serves Dex Web and /mcp on one listener, with Dex Web's timeouts.
type webListenerServer struct {
	httpServer            *http.Server
	cancelRequestContexts context.CancelFunc
}

// newWebListenerServer mounts mcpHandler at /mcp and passes every other request to Dex Web.
func newWebListenerServer(web *dexweb.Server, mcpHandler http.Handler) *webListenerServer {
	mux := http.NewServeMux()
	mux.Handle(connectorsMCPMountPath, mcpHandler)
	mux.Handle("/", web.Handler())
	requestBaseContext, cancelRequestContexts := context.WithCancel(context.Background())
	return &webListenerServer{
		httpServer: &http.Server{
			Handler:           mux,
			ReadTimeout:       30 * time.Second,
			ReadHeaderTimeout: 10 * time.Second,
			IdleTimeout:       90 * time.Second,
			BaseContext:       func(net.Listener) context.Context { return requestBaseContext },
		},
		cancelRequestContexts: cancelRequestContexts,
	}
}

func (server *webListenerServer) Serve(listener net.Listener) error {
	return server.httpServer.Serve(listener)
}

// Shutdown cancels in-flight request contexts, then waits for handlers to return or ctx to expire.
func (server *webListenerServer) Shutdown(ctx context.Context) error {
	server.cancelRequestContexts()
	return server.httpServer.Shutdown(ctx)
}
