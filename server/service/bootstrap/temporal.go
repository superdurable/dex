// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

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
		r.workerPool,
	)
	return unifiedClient, store, worker, nil
}
