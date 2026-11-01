// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package command

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"

	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/emptypb"
)

const defaultMaxMessageBytes = 32 << 20

type flowService struct {
	connection *grpc.ClientConn
	service    dexpb.FlowServiceClient
}

func withFlowService(
	ctx context.Context,
	options options,
	call func(context.Context, *flowService) error,
) error {
	client, err := newFlowService(options)
	if err != nil {
		return newOperationError("connect", err)
	}
	callCtx, cancel := requestContext(ctx, options)
	callErr := call(callCtx, client)
	cancel()
	closeErr := client.connection.Close()
	if callErr != nil {
		return callErr
	}
	if closeErr != nil {
		return newOperationError("close", closeErr)
	}
	return nil
}

func withStreamingFlowService(
	ctx context.Context,
	options options,
	call func(*flowService) error,
) error {
	client, err := newFlowService(options)
	if err != nil {
		return newOperationError("connect", err)
	}
	callErr := call(client)
	closeErr := client.connection.Close()
	if callErr != nil {
		return callErr
	}
	if closeErr != nil {
		return newOperationError("close", closeErr)
	}
	return nil
}

// withServerConnection runs call with the request timeout over a new connection to --server.
func withServerConnection(
	ctx context.Context,
	options options,
	serviceName string,
	call func(context.Context, *grpc.ClientConn) error,
) error {
	connection, err := newServerConnection(options, serviceName)
	if err != nil {
		return newOperationError("connect", err)
	}
	callCtx, cancel := requestContext(ctx, options)
	callErr := call(callCtx, connection)
	cancel()
	closeErr := connection.Close()
	if callErr != nil {
		return callErr
	}
	if closeErr != nil {
		return newOperationError("close", closeErr)
	}
	return nil
}

func newFlowService(options options) (*flowService, error) {
	connection, err := newServerConnection(options, "FlowService")
	if err != nil {
		return nil, err
	}
	return &flowService{
		connection: connection,
		service:    dexpb.NewFlowServiceClient(connection),
	}, nil
}

// newServerConnection dials --server with the configured transport and per-call credentials.
func newServerConnection(options options, serviceName string) (*grpc.ClientConn, error) {
	transportCredential, err := newTransportCredential(options)
	if err != nil {
		return nil, err
	}
	perRPCCredential, err := newPerRPCCredential(options)
	if err != nil {
		return nil, err
	}
	dialOptions := []grpc.DialOption{
		grpc.WithTransportCredentials(transportCredential),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(defaultMaxMessageBytes),
			grpc.MaxCallSendMsgSize(defaultMaxMessageBytes),
		),
	}
	if perRPCCredential != nil {
		dialOptions = append(dialOptions, grpc.WithPerRPCCredentials(perRPCCredential))
	}
	connection, err := grpc.NewClient(
		options.server,
		dialOptions...,
	)
	if err != nil {
		return nil, fmt.Errorf("create %s client: %w", serviceName, err)
	}
	return connection, nil
}

// newTransportCredential returns plaintext unless TLS is enabled; gRPC adds the h2 ALPN protocol and,
// without --tls-server-name, verifies the --server host.
func newTransportCredential(options options) (credentials.TransportCredentials, error) {
	if err := options.validateTLSSettings(); err != nil {
		return nil, newUsageError("dexcli", err)
	}
	if !options.isTLSEnabled {
		return insecure.NewCredentials(), nil
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: options.tlsServerName}
	if options.tlsCAFile != "" {
		rootCAs, err := loadTLSRootCAs(options.tlsCAFile)
		if err != nil {
			return nil, err
		}
		tlsConfig.RootCAs = rootCAs
	}
	return credentials.NewTLS(tlsConfig), nil
}

// loadTLSRootCAs returns the system roots plus every certificate in the PEM file.
func loadTLSRootCAs(path string) (*x509.CertPool, error) {
	pemData, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read TLS CA file: %w", err)
	}
	rootCAs, err := x509.SystemCertPool()
	if err != nil {
		return nil, fmt.Errorf("load system root CAs: %w", err)
	}
	if !rootCAs.AppendCertsFromPEM(pemData) {
		return nil, fmt.Errorf("TLS CA file %s contains no PEM certificate", path)
	}
	return rootCAs, nil
}

func newPerRPCCredential(options options) (credentials.PerRPCCredentials, error) {
	if err := options.validateAPIKeySettings(); err != nil {
		return nil, newUsageError("dexcli", err)
	}
	switch {
	case options.hasAPIKeySettings():
		apiKey, err := options.readAPIKey()
		if err != nil {
			return nil, err
		}
		return newAPIKeyCredential(options.tokenURL, options.serverID, apiKey)
	case options.hasFile || options.credentialFile != "":
		return newFileBearerCredential(options.credentialFile)
	case options.hasCredential || options.credential != "":
		return newStaticBearerCredential(options.credential)
	default:
		return nil, nil
	}
}

func requestContext(ctx context.Context, options options) (context.Context, context.CancelFunc) {
	if options.timeout == 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, options.timeout)
}

func closeWithError(primary error, closer func() error) error {
	return errors.Join(primary, closer())
}

func emptyRequest() *emptypb.Empty {
	return &emptypb.Empty{}
}
