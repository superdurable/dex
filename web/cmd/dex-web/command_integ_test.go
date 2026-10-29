// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/pem"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"github.com/superdurable/dex/web"
	"github.com/superdurable/dex/web/assets"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

type authenticatedFlowService struct {
	dexpb.UnimplementedFlowServiceServer
	expectedToken string
}

func (s *authenticatedFlowService) HealthCheck(ctx context.Context, _ *emptypb.Empty) (*dexpb.HealthInfo, error) {
	if s.expectedToken != "" {
		requestMetadata, ok := metadata.FromIncomingContext(ctx)
		if !ok || strings.Join(requestMetadata.Get("authorization"), "") != "Bearer "+s.expectedToken {
			return nil, status.Error(codes.Unauthenticated, "credential required")
		}
	}
	return &dexpb.HealthInfo{}, nil
}

func (s *authenticatedFlowService) SearchFlows(ctx context.Context, _ *dexpb.SearchFlowsRequest) (*dexpb.SearchFlowsResponse, error) {
	if _, err := s.HealthCheck(ctx, &emptypb.Empty{}); err != nil {
		return nil, err
	}
	return &dexpb.SearchFlowsResponse{}, nil
}

func TestStandaloneBridgeWithTLSAndRotatingBearerToken(t *testing.T) {
	var diagnosticLog bytes.Buffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&diagnosticLog, nil)))
	t.Cleanup(func() { slog.SetDefault(previousLogger) })
	certificateServer := httptest.NewTLSServer(http.NotFoundHandler())
	defer certificateServer.Close()
	certificate := certificateServer.TLS.Certificates[0]
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	grpcServer := grpc.NewServer(grpc.Creds(credentials.NewTLS(&tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12})))
	dexpb.RegisterFlowServiceServer(grpcServer, &authenticatedFlowService{expectedToken: "current-token"})
	grpcErrors := make(chan error, 1)
	go func() { grpcErrors <- grpcServer.Serve(listener) }()
	t.Cleanup(func() {
		grpcServer.Stop()
		if err := <-grpcErrors; err != nil {
			t.Error(err)
		}
	})
	tokenPath := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenPath, []byte(" previous-token \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &commandConfig{flowServiceAddress: listener.Addr().String(), credentialFile: tokenPath}
	certificatePath := filepath.Join(t.TempDir(), "ca.pem")
	certificatePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Certificate[0]})
	if err := os.WriteFile(certificatePath, certificatePEM, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg.isTLSEnabled, cfg.tlsCAFile, cfg.tlsServerName = true, certificatePath, "example.com"
	connection, err := newFlowServiceConnection(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := connection.Close(); err != nil {
			t.Error(err)
		}
	}()
	bridge, err := web.NewServer(&web.Config{}, dexpb.NewFlowServiceClient(connection), assets.Files)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(bridge.Handler())
	defer httpServer.Close()
	response, err := http.Get(httpServer.URL + "/v1/flows")
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(response.Body)
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if readErr != nil {
		t.Fatal(readErr)
	}
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), `<div id="root"></div>`) {
		t.Fatalf("SPA response: %d %s", response.StatusCode, body)
	}
	response, err = http.Post(httpServer.URL+"/api/flows/search", "application/json", strings.NewReader(`{"pageSize":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusBadGateway {
		t.Fatalf("missing credential rejection: %d", response.StatusCode)
	}
	if err := os.WriteFile(tokenPath, []byte(" current-token \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		response, err := http.Post(httpServer.URL+"/api/flows/search", "application/json", strings.NewReader(`{"pageSize":1}`))
		if err != nil {
			t.Fatal(err)
		}
		body, readErr := io.ReadAll(response.Body)
		if err := response.Body.Close(); err != nil {
			t.Fatal(err)
		}
		if readErr != nil {
			t.Fatal(readErr)
		}
		if response.StatusCode == http.StatusOK && strings.Contains(string(body), `"flows"`) {
			break
		}
		select {
		case <-deadline.C:
			t.Fatalf("bridge response: %d %s", response.StatusCode, body)
		case <-ticker.C:
		}
	}
	if err := os.Remove(tokenPath); err != nil {
		t.Fatal(err)
	}
	for {
		response, err := http.Post(httpServer.URL+"/api/flows/search", "application/json", strings.NewReader(`{"pageSize":1}`))
		if err != nil {
			t.Fatal(err)
		}
		body, readErr := io.ReadAll(response.Body)
		if err := response.Body.Close(); err != nil {
			t.Fatal(err)
		}
		if readErr != nil {
			t.Fatal(readErr)
		}
		if response.StatusCode == http.StatusBadGateway {
			if strings.Contains(string(body), tokenPath) || !strings.Contains(string(body), "SearchFlows failed") {
				t.Fatalf("credential failure response: %s", body)
			}
			if !strings.Contains(diagnosticLog.String(), tokenPath) ||
				!strings.Contains(diagnosticLog.String(), `"operation":"SearchFlows"`) ||
				!strings.Contains(diagnosticLog.String(), "no such file") {
				t.Fatalf("missing server-side credential diagnostic: %s", &diagnosticLog)
			}
			break
		}
		select {
		case <-deadline.C:
			t.Fatal("deleted credential did not fail RPC")
		case <-ticker.C:
		}
	}
}

func TestStandaloneCommandEnvironmentFlagsAndShutdown(t *testing.T) {
	t.Setenv("DEX_WEB_PORT", "tcp://10.0.0.1:8802")
	t.Setenv("DEX_WEB_SERVICE_HOST", "10.0.0.1")
	t.Setenv("DEX_WEB_HTTP_PORT", "tcp://10.0.0.2:8802")
	t.Setenv("DEX_WEB_UNRELATED", "ignored")
	t.Setenv("DEX_WEB_HTTP_PORT_NUMBER", "19002")
	t.Setenv("DEX_WEB_FLOW_RENDERING_DIRECTORY", "definitions")
	defaults, err := parseCommandConfig(nil, io.Discard)
	if err != nil || defaults.web.Port != 19002 || defaults.web.FlowRenderingDirectory != "definitions" {
		t.Fatalf("environment config: %+v, %v", defaults, err)
	}
	t.Setenv("DEX_WEB_FLOW_RENDERING_DIRECTORY", "")
	t.Setenv("DEX_WEB_START_FLOW_WORKER_TARGET_PULL", "true")
	cfg, err := parseCommandConfig([]string{"--port", "19003"}, io.Discard)
	if err != nil || cfg.web.Port != 19003 || !cfg.web.IsStartFlowWorkerTargetPull {
		t.Fatalf("config: %+v, %v", cfg, err)
	}
	for _, arguments := range [][]string{{"--port", "0"}, {"--tls-ca-file", "ca.pem"}, {"unexpected"}} {
		if _, err := parseCommandConfig(arguments, io.Discard); err == nil {
			t.Fatalf("accepted invalid arguments: %v", arguments)
		}
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	commandErrors := make(chan error, 1)
	go func() {
		commandErrors <- runDexWeb(ctx, []string{"--port", strconv.Itoa(port)}, io.Discard, io.Discard)
	}()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		response, err := http.Get("http://127.0.0.1:" + strconv.Itoa(port) + "/healthz")
		if err == nil {
			if err := response.Body.Close(); err != nil {
				t.Fatal(err)
			}
			if response.StatusCode == http.StatusOK {
				break
			}
		}
		select {
		case err := <-commandErrors:
			t.Fatalf("early exit: %v", err)
		case <-deadline.C:
			t.Fatal("command did not listen")
		case <-ticker.C:
		}
	}
	cancel()
	select {
	case err := <-commandErrors:
		if err != nil {
			t.Fatal(err)
		}
	case <-deadline.C:
		t.Fatal("command did not stop")
	}
}
