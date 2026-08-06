// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package bootstrap

import (
	"context"
	"crypto/tls"
	"fmt"

	"github.com/superdurable/dex/service"
	uclient "github.com/superdurable/dex/service/client"
	temporalapi "github.com/superdurable/dex/service/client/temporal"
	"github.com/superdurable/dex/service/common/blobstore"
	dexconverter "github.com/superdurable/dex/service/common/converter"
	"github.com/superdurable/dex/service/interpreter/temporal"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	ggrpc "google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

func (r *Runtime) createTemporalServices() (
	uclient.UnifiedClient,
	blobstore.BlobStore,
	interpreterWorker,
	error,
) {
	temporalConfig := r.cfg.Interpreter.Temporal
	clientOptions := client.Options{
		HostPort:  temporalConfig.HostPort,
		Namespace: temporalConfig.Namespace,
	}
	metrics := client.MetricsNopHandler
	if temporalConfig.Prometheus != nil {
		var err error
		metrics, r.metricsCloser, err = newPrometheusScope(temporalConfig.Prometheus, r.logger)
		if err != nil {
			return nil, nil, nil, err
		}
		clientOptions.MetricsHandler = metrics
	}
	if temporalConfig.CloudAPIKey != "" {
		clientOptions.Credentials = client.NewAPIKeyStaticCredentials(temporalConfig.CloudAPIKey)
		// NOTE: this connectionOptions can be removed when upgrading temporal SDK to latest
		// see https://docs.temporal.io/cloud/api-keys#sdk
		clientOptions.ConnectionOptions = client.ConnectionOptions{
			TLS: &tls.Config{},
			DialOptions: []ggrpc.DialOption{
				ggrpc.WithUnaryInterceptor(
					func(
						ctx context.Context,
						method string,
						request any,
						reply any,
						connection *ggrpc.ClientConn,
						invoker ggrpc.UnaryInvoker,
						options ...ggrpc.CallOption,
					) error {
						return invoker(
							metadata.AppendToOutgoingContext(ctx, "temporal-namespace", temporalConfig.Namespace),
							method,
							request,
							reply,
							connection,
							options...,
						)
					},
				),
			},
		}
	}

	dataConverter := dexconverter.NewTemporalDataConverter()
	clientOptions.DataConverter = dataConverter
	temporalClient, err := client.Dial(clientOptions)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("connect to Temporal: %w", err)
	}
	unifiedClient := temporalapi.NewTemporalClient(
		temporalClient,
		temporalConfig.Namespace,
		dataConverter,
		false,
		&r.cfg.Api.QueryWorkflowFailedRetryPolicy,
	)
	store, err := r.createBlobStore(
		context.Background(),
		temporalConfig.Namespace,
		metrics,
	)
	if err != nil {
		unifiedClient.Close()
		return nil, nil, nil, err
	}
	if !r.options.Services.Interpreter {
		return unifiedClient, store, nil, nil
	}
	worker := temporal.NewInterpreterWorker(
		r.cfg,
		temporalClient,
		service.TaskQueue,
		converter.DataConverter(dataConverter),
		unifiedClient,
		store,
		r.flowIndex,
		r.workerPool,
	)
	return unifiedClient, store, worker, nil
}
