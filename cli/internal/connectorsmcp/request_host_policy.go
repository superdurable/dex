// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package connectorsmcp

import (
	"net"
	"net/http"
	"strings"
)

// requestHostPolicy refuses a request whose Host header names another site, so DNS rebinding cannot call tools.
// It accepts the same names as Dex Web's /api: loopback names, IP addresses, and the allowed hosts.
type requestHostPolicy struct {
	allowedHosts map[string]bool
}

func newRequestHostPolicy(allowedHosts []string) requestHostPolicy {
	canonicalHosts := make(map[string]bool)
	for _, host := range allowedHosts {
		if canonical := canonicalRequestHost(host); canonical != "" {
			canonicalHosts[canonical] = true
		}
	}
	return requestHostPolicy{allowedHosts: canonicalHosts}
}

func (policy requestHostPolicy) allows(hostHeader string) bool {
	host := canonicalRequestHost(hostHeader)
	if host == "" {
		return false
	}
	return isLoopbackHostName(host) || net.ParseIP(host) != nil || policy.allowedHosts[host]
}

// protect wraps next with the Host check and cross-origin protection against browser pages.
func (policy requestHostPolicy) protect(next http.Handler) http.Handler {
	crossOriginProtection := http.NewCrossOriginProtection()
	protected := crossOriginProtection.Handler(next)
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if !policy.allows(request.Host) {
			http.Error(response, "Dex Connectors MCP does not accept this Host; add it to the allowed hosts", http.StatusForbidden)
			return
		}
		protected.ServeHTTP(response, request)
	})
}

// canonicalRequestHost lowercases a host, drops its port, brackets, and trailing dot, and returns "" for no host.
func canonicalRequestHost(value string) string {
	host := strings.TrimSpace(value)
	if splitHost, _, err := net.SplitHostPort(host); err == nil {
		host = splitHost
	}
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	return strings.TrimSuffix(strings.ToLower(host), ".")
}

func isLoopbackHostName(host string) bool {
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// IsLoopbackAddress reports whether host, an IP address or a name, is a loopback address.
func IsLoopbackAddress(host string) bool {
	return isLoopbackHostName(canonicalRequestHost(host))
}
