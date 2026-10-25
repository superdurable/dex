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
	// Invalid PEM fails NewClient or NewWorker.
	AdditionalRootCAsPEM []byte
	// ServerName overrides the FlowServiceAddress host for certificate verification and SNI.
	// Empty uses the FlowServiceAddress host. Use a DNS name or IP address without a port.
	// An invalid value fails NewClient or NewWorker.
	ServerName string
}

func flowServiceTransportCredentials(address string, options *FlowServiceTLSOptions) (credentials.TransportCredentials, error) {
	if options == nil {
		return insecure.NewCredentials(), nil
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil || host == "" {
		return nil, fmt.Errorf("dex: FlowServiceTLS requires FlowServiceAddress as host:port, got %q", address)
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
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: options.ServerName}
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
