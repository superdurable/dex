// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package web

import (
	"net"
	"strings"
)

// requestHostPolicy decides which Host header names an /api request may use, so DNS rebinding cannot reach the API.
type requestHostPolicy struct {
	// isRestricted is false without Connector setup and allowed hosts; such a server accepts every Host.
	isRestricted bool
	allowedHosts map[string]bool
}

func newRequestHostPolicy(cfg *Config) requestHostPolicy {
	bindAddress := strings.TrimSpace(cfg.BindAddress)
	if bindAddress == "" {
		bindAddress = "127.0.0.1"
	}
	allowedHosts := map[string]bool{canonicalRequestHost(bindAddress): true}
	for _, host := range cfg.AllowedHosts {
		if canonical := canonicalRequestHost(host); canonical != "" {
			allowedHosts[canonical] = true
		}
	}
	return requestHostPolicy{
		isRestricted: cfg.ConnectorSetupEnabled || len(cfg.AllowedHosts) > 0,
		allowedHosts: allowedHosts,
	}
}

// allows reports whether a request's Host header names this server. A DNS name works only when it is a loopback
// name or listed; an IP address cannot be rebound, so every one is accepted.
func (policy requestHostPolicy) allows(hostHeader string) bool {
	if !policy.isRestricted {
		return true
	}
	host := canonicalRequestHost(hostHeader)
	if host == "" {
		return false
	}
	return isLoopbackHostName(host) || net.ParseIP(host) != nil || policy.allowedHosts[host]
}

// SplitAllowedHosts turns a comma-separated --allowed-hosts value into Config.AllowedHosts entries.
// It trims spaces and drops empty entries, so an empty value returns no entry.
func SplitAllowedHosts(value string) []string {
	var hosts []string
	for _, host := range strings.Split(value, ",") {
		if host = strings.TrimSpace(host); host != "" {
			hosts = append(hosts, host)
		}
	}
	return hosts
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
