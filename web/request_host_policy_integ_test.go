// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package web_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	dexweb "github.com/superdurable/dex/web"
)

func TestDexWebRefusesRebindingHostsWhereTheHostCheckIsOn(t *testing.T) {
	connectorSetup := newHarnessWithConfig(t, &flowService{}, &dexweb.Config{
		BindAddress: "127.0.0.1", Port: dexweb.DefaultPort, ConnectorSetupEnabled: true,
		ConnectorConfigDirectory: t.TempDir(), AllowedHosts: []string{"Dex-Dev.Example.com:443"},
	})
	allowedOnly := newHarnessWithConfig(t, &flowService{}, &dexweb.Config{
		BindAddress: "127.0.0.1", Port: dexweb.DefaultPort, AllowedHosts: []string{"dex.example.com"},
	})
	unchecked := newHarness(t, &flowService{})
	for _, scenario := range []struct {
		name       string
		harness    *harness
		path       string
		host       string
		statusCode int
	}{
		{"rebinding name reads the CSRF token", connectorSetup, "/api/v2/connector-connections", "attacker.example:8802", http.StatusForbidden},
		{"rebinding name with a trailing dot", connectorSetup, "/api/v2/connector-connections", "attacker.example.", http.StatusForbidden},
		{"rebinding name posts a connection", connectorSetup, "/api/v2/connector-connections", "attacker.example", http.StatusForbidden},
		{"localhost", connectorSetup, "/api/v2/connector-connections", "localhost:8802", http.StatusOK},
		{"localhost subdomain", connectorSetup, "/api/v2/connector-connections", "dex.localhost:8802", http.StatusOK},
		{"IPv4 loopback", connectorSetup, "/api/v2/connector-connections", "127.0.0.1:8802", http.StatusOK},
		{"IPv6 loopback", connectorSetup, "/api/v2/connector-connections", "[::1]:8802", http.StatusOK},
		{"private IP address", connectorSetup, "/api/v2/connector-connections", "192.168.1.20:8802", http.StatusOK},
		{"allowed host in another case and port", connectorSetup, "/api/v2/connector-connections", "dex-dev.example.com", http.StatusOK},
		{"page assets stay served", connectorSetup, "/", "attacker.example", http.StatusOK},
		{"allowed hosts turn the check on", allowedOnly, "/api/flow-definitions", "attacker.example", http.StatusForbidden},
		{"allowed host passes", allowedOnly, "/api/flow-definitions", "dex.example.com:8802", http.StatusOK},
		{"no check without setup or allowed hosts", unchecked, "/api/flow-definitions", "proxy.example", http.StatusOK},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			request, err := http.NewRequest(http.MethodGet, scenario.harness.http.URL+scenario.path, nil)
			if err != nil {
				t.Fatal(err)
			}
			request.Host = scenario.host
			response, err := scenario.harness.http.Client().Do(request)
			if err != nil {
				t.Fatal(err)
			}
			body := readBody(t, response)
			if err := response.Body.Close(); err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != scenario.statusCode {
				t.Fatalf("status=%d body=%s", response.StatusCode, body)
			}
			if scenario.statusCode == http.StatusForbidden && (!strings.Contains(body, `"HOST_NOT_ALLOWED"`) || strings.Contains(body, "csrfToken")) {
				t.Fatalf("refusal body = %s", body)
			}
		})
	}

	request, err := http.NewRequest(http.MethodPost, connectorSetup.http.URL+"/api/v2/connector-connections",
		strings.NewReader(`{"connectorId":"slack","connectionName":"support-slack","modulePath":"x","moduleVersion":"v1.0.0"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Host = "attacker.example:8802"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", (&url.URL{Scheme: "http", Host: "attacker.example:8802"}).String())
	response, err := connectorSetup.http.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body := readBody(t, response)
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusForbidden || !strings.Contains(body, `"HOST_NOT_ALLOWED"`) {
		t.Fatalf("rebinding POST status=%d body=%s", response.StatusCode, body)
	}
}
