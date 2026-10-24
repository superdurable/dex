// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//go:build integration

package web_test

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/superdurable/dex/gen/dexpb"
	dexweb "github.com/superdurable/dex/web"
	"github.com/superdurable/dex/web/assets"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func TestRootMountIgnoresForwardedHeadersWithRealDex(t *testing.T) {
	address := os.Getenv("DEX_WEB_ROOT_MOUNT_TEST_GRPC_ADDRESS")
	if address == "" {
		t.Fatal("DEX_WEB_ROOT_MOUNT_TEST_GRPC_ADDRESS must identify a real Dex Server")
	}
	connection, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := connection.Close(); err != nil {
			t.Error(err)
		}
	})
	server, err := dexweb.NewServer(&dexweb.Config{BindAddress: "127.0.0.1"}, dexpb.NewFlowServiceClient(connection), assets.Files)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { finished <- server.Serve(listener) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			t.Error(err)
		}
		select {
		case err := <-finished:
			if !errors.Is(err, http.ErrServerClosed) {
				t.Errorf("Web Serve: %v", err)
			}
		case <-ctx.Done():
			t.Error("Web Serve did not stop")
		}
	})
	client := &http.Client{Timeout: 15 * time.Second}
	baseURL := "http://" + listener.Addr().String()
	request := func(path string) (*http.Response, string) {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, baseURL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		for name, value := range map[string]string{
			"Forwarded":                          "proto=https;host=untrusted.invalid",
			"X-Forwarded-Proto":                  "https",
			"X-Forwarded-Host":                   "untrusted.invalid",
			"X-Forwarded-Prefix":                 "/ignored-mount",
			"X-Dex-Web-Embedded":                 "true",
			"X-Dex-Web-CSRF-Token":               "ignored-context",
			"X-CSRF-Token":                       "different-browser-context",
			"X-Dex-Connector-OAuth-Redirect-URI": "https://untrusted.invalid/callback",
		} {
			req.Header.Set(name, value)
		}
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, readErr := io.ReadAll(response.Body)
		closeErr := response.Body.Close()
		if err := errors.Join(readErr, closeErr); err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != http.StatusOK {
			t.Fatalf("GET %s: status=%d body=%s", path, response.StatusCode, body)
		}
		return response, string(body)
	}
	for _, path := range []string{"/", "/v1/flows", "/v2", "/v2/work-queue", "/v2/connectors", "/v2/debug"} {
		response, body := request(path)
		if !strings.Contains(body, `src="/assets/`) || strings.Contains(body, "__DEX_WEB_CONFIG__") ||
			strings.Contains(body, "ignored-mount") || strings.Contains(body, "untrusted.invalid") {
			t.Fatalf("GET %s does not serve the root SPA: %s", path, body)
		}
		if response.Header.Get("Cache-Control") != "no-store" ||
			response.Header.Get("Content-Security-Policy") != "frame-ancestors 'none'" ||
			response.Header.Get("X-Frame-Options") != "DENY" {
			t.Fatalf("GET %s: unsafe HTML headers: %v", path, response.Header)
		}
		if path == "/" {
			asset := regexp.MustCompile(`src="(/assets/[^" ]+\.js)"`).FindStringSubmatch(body)
			if len(asset) != 2 {
				t.Fatal("root JavaScript asset missing")
			}
			request(asset[1])
		}
	}
	request("/api/flow-definitions")
	// Readiness performs a real FlowService search through this Web instance.
	request("/readyz")
}
