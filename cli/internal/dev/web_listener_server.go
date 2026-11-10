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
	"io"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"time"

	"github.com/superdurable/dex/cli/internal/connectorsmcp"
	dexweb "github.com/superdurable/dex/web"
)

// webListenerServer serves Dex Web with the Connectors MCP endpoint mounted beside it on one listener.
// It copies Dex Web's HTTP timeouts and cancels in-flight requests, such as long polls, on Shutdown.
type webListenerServer struct {
	httpServer            *http.Server
	cancelRequestContexts context.CancelFunc
}

// newWebListenerServer mounts mcpHandler at the MCP path and passes every other request to Dex Web.
func newWebListenerServer(web *dexweb.Server, mcpHandler http.Handler) *webListenerServer {
	mux := http.NewServeMux()
	mux.Handle(connectorsmcp.MountPath, mcpHandler)
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

// newConnectorsMCPServer returns the Connectors MCP server that dexcli dev mounts for cfg.
func newConnectorsMCPServer(cfg *Config, logs io.Writer) (*connectorsmcp.Server, error) {
	return connectorsmcp.NewServer(
		&connectorsmcp.Config{
			ConnectorConfigDirectory: cfg.ConnectorConfigDirectory,
			StateDirectory:           filepath.Join(cfg.StateDirectory, connectorsmcp.StateDirectoryName),
			Connections:              cfg.MCPConnections,
			AllowedHosts:             dexweb.SplitAllowedHosts(cfg.AllowedHosts),
			Version:                  cfg.version,
		},
		slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelInfo})),
	)
}
