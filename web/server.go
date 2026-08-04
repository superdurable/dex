// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

package web

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/web/api"
)

const DefaultPort = 8802

type Config struct {
	// BindAddress defaults to 127.0.0.1 and controls the HTTP bind IP.
	BindAddress string
	// Port defaults to 8802 and controls the HTTP bind port.
	Port int
}

type Server struct {
	cfg        *Config
	httpServer *http.Server
}

func NewServer(cfg *Config, client dexpb.FlowServiceClient, assets fs.FS) *Server {
	if cfg == nil {
		panic("Web config must not be nil")
	}
	if client == nil {
		panic("Dex FlowService client must not be nil")
	}
	if assets == nil {
		panic("Web assets must not be nil")
	}
	assetRoot, err := fs.Sub(assets, "dist")
	if err != nil {
		panic(fmt.Sprintf("open embedded Web assets: %v", err))
	}
	mux := http.NewServeMux()
	api.RegisterHandlers(mux, client)
	mux.Handle("/", spaHandler(assetRoot))
	return &Server{
		cfg: cfg,
		httpServer: &http.Server{
			Handler:           mux,
			ReadHeaderTimeout: 10 * time.Second,
			IdleTimeout:       90 * time.Second,
		},
	}
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

func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

func (s *Server) Handler() http.Handler {
	return s.httpServer.Handler
}

func spaHandler(assets fs.FS) http.Handler {
	files := http.FileServer(http.FS(assets))
	index, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		panic(fmt.Sprintf("read embedded Web index: %v", err))
	}
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet && request.Method != http.MethodHead {
			response.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		requestPath := strings.TrimPrefix(path.Clean(request.URL.Path), "/")
		if requestPath == "." || requestPath == "" {
			serveIndex(response, request, index)
			return
		}
		file, err := assets.Open(requestPath)
		if err == nil {
			if closeErr := file.Close(); closeErr != nil {
				http.Error(response, closeErr.Error(), http.StatusInternalServerError)
				return
			}
			files.ServeHTTP(response, request)
			return
		}
		if strings.HasPrefix(request.URL.Path, "/api/") {
			api.WriteError(response, http.StatusNotFound, "API route not found", nil)
			return
		}
		serveIndex(response, request, index)
	})
}

func serveIndex(response http.ResponseWriter, request *http.Request, index []byte) {
	http.ServeContent(response, request, "index.html", time.Time{}, bytes.NewReader(index))
}
