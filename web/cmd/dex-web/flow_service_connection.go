// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package main

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

func newFlowServiceConnection(cfg *commandConfig) (*grpc.ClientConn, error) {
	transportCredential := insecure.NewCredentials()
	if cfg.isTLSEnabled {
		tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: cfg.tlsServerName}
		if cfg.tlsCAFile != "" {
			certificatePEM, err := os.ReadFile(cfg.tlsCAFile)
			if err != nil {
				return nil, fmt.Errorf("read TLS CA file: %w", err)
			}
			certificatePool, err := x509.SystemCertPool()
			if err != nil {
				return nil, fmt.Errorf("load system CA certificates: %w", err)
			}
			if !certificatePool.AppendCertsFromPEM(certificatePEM) {
				return nil, fmt.Errorf("TLS CA file contains no PEM certificates")
			}
			tlsConfig.RootCAs = certificatePool
		}
		transportCredential = credentials.NewTLS(tlsConfig)
	}
	dialOptions := []grpc.DialOption{
		grpc.WithTransportCredentials(transportCredential),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(32<<20), grpc.MaxCallSendMsgSize(32<<20)),
	}
	if cfg.credentialFile != "" {
		credential, err := newFlowServiceBearerCredential(cfg.credentialFile)
		if err != nil {
			return nil, err
		}
		dialOptions = append(dialOptions, grpc.WithPerRPCCredentials(credential))
	}
	connection, err := grpc.NewClient(cfg.flowServiceAddress, dialOptions...)
	if err != nil {
		return nil, fmt.Errorf("create FlowService client: %w", err)
	}
	return connection, nil
}
