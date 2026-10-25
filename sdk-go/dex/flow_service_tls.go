// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package dex

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"strconv"
	"strings"

	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

// FlowServiceTLSOptions enables TLS for a Client or Worker's FlowService connection.
//
// Set ClientOptions.FlowServiceTLS and WorkerOptions.FlowServiceTLS independently.
// A nil pointer keeps plaintext gRPC. A non-nil pointer uses TLS 1.2 or newer,
// verifies the server certificate, and negotiates HTTP/2 with ALPN h2. The
// FlowServiceAddress host is the default certificate name. The TLS settings
// apply only to FlowService, not to a Worker's listening WorkerService or an
// APIKeyCredential token endpoint.
// FlowServiceAddress accepts host:port or dns:///host:port, but not an HTTP URL.
// Connections open on demand, so certificate, hostname, and handshake failures
// appear as Unavailable on the first Client call or from Worker.Start.
// The endpoint or gateway must accept ALPN h2 and forward gRPC over HTTP/2.
// An IP address or IP ServerName sends no SNI and requires a matching IP SAN.
//
//	func run(ctx context.Context, registry *dex.Registry, cache *blobcache.Cache) error {
//		rootPEM, err := os.ReadFile("/etc/dex/root-ca.pem")
//		if err != nil {
//			return err
//		}
//		tlsOptions := &dex.FlowServiceTLSOptions{AdditionalRootCAsPEM: rootPEM}
//		worker, err := dex.NewWorker(registry, cache, dex.WorkerOptions{
//			FlowServiceAddress: "dex.example.com:443", FlowServiceTLS: tlsOptions,
//			Pull: &dex.PullWorkerOptions{},
//		})
//		if err != nil {
//			return err
//		}
//		client, err := dex.NewClient(registry, cache, dex.ClientOptions{
//			FlowServiceAddress: "dex.example.com:443", FlowServiceTLS: tlsOptions,
//			WorkerTarget: worker.WorkerTarget(),
//		})
//		if err != nil {
//			return errors.Join(err, worker.Stop(context.Background()))
//		}
//		workerResult := make(chan error, 1)
//		go func() { workerResult <- worker.Start() }()
//		if err := worker.WaitPullRegistered(ctx); err != nil {
//			return errors.Join(err, worker.Stop(context.Background()), <-workerResult, client.Close())
//		}
//		<-ctx.Done()
//		return errors.Join(worker.Stop(context.Background()), <-workerResult, client.Close())
//	}
type FlowServiceTLSOptions struct {
	// AdditionalRootCAsPEM adds PEM-encoded root CAs to the system roots.
	// Empty uses the system roots alone. The bytes are read at construction, not watched for changes.
	// At least one certificate must parse from a nonempty bundle, or NewClient or NewWorker fails.
	// Without system roots or an added trusted CA, certificate verification fails on connection.
	AdditionalRootCAsPEM []byte
	// ServerName overrides the FlowServiceAddress host for certificate verification, SNI, and HTTP/2 :authority.
	// Empty uses the FlowServiceAddress host. Use a DNS name or IP address without a port.
	// An IP address sends no SNI and requires a matching IP SAN.
	// An invalid value fails NewClient or NewWorker.
	ServerName string
}

func flowServiceTransportCredentials(address string, options *FlowServiceTLSOptions) (credentials.TransportCredentials, error) {
	if options == nil {
		return insecure.NewCredentials(), nil
	}
	targetAddress, isDNSTarget := strings.CutPrefix(address, "dns:///")
	if !isDNSTarget {
		targetAddress = address
	}
	host, port, err := net.SplitHostPort(targetAddress)
	if err != nil || host == "" {
		return nil, fmt.Errorf("dex: FlowServiceTLS requires FlowServiceAddress as host:port or dns:///host:port, got %q", address)
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return nil, fmt.Errorf("dex: FlowServiceTLS requires FlowServiceAddress port 1-65535, got %q", address)
	}
	serverName := options.ServerName
	if serverName != "" && (strings.ContainsAny(serverName, " \t\r\n/#?") ||
		(strings.Contains(serverName, ":") && net.ParseIP(serverName) == nil)) {
		return nil, fmt.Errorf("dex: FlowServiceTLS.ServerName must be a DNS name or IP address, got %q", serverName)
	}
	if serverName == "" && isDNSTarget {
		serverName = host
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: serverName}
	if len(options.AdditionalRootCAsPEM) > 0 {
		rootCAs, err := x509.SystemCertPool()
		if err != nil {
			return nil, fmt.Errorf("dex: FlowServiceTLS.AdditionalRootCAsPEM: load system root CAs: %w", err)
		}
		if !rootCAs.AppendCertsFromPEM(options.AdditionalRootCAsPEM) {
			return nil, fmt.Errorf("dex: FlowServiceTLS.AdditionalRootCAsPEM contains no PEM certificate")
		}
		tlsConfig.RootCAs = rootCAs
	}
	return credentials.NewTLS(tlsConfig), nil
}
