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
	"fmt"
	"log"
	"sync"

	"github.com/superdurable/dex/service"
	uclient "github.com/superdurable/dex/service/client"
	cadenceapi "github.com/superdurable/dex/service/client/cadence"
	"github.com/superdurable/dex/service/common/blobstore"
	dexconverter "github.com/superdurable/dex/service/common/converter"
	"github.com/superdurable/dex/service/interpreter/cadence"
	apiv1 "github.com/uber/cadence-idl/go/proto/api/v1"
	"go.temporal.io/sdk/client"
	"go.uber.org/cadence/.gen/go/cadence/workflowserviceclient"
	cadenceclient "go.uber.org/cadence/client"
	"go.uber.org/cadence/compatibility"
	"go.uber.org/cadence/encoded"
	"go.uber.org/yarpc"
	"go.uber.org/yarpc/transport/grpc"
)

const DefaultCadenceDomain = "default"
const DefaultCadenceHostPort = "127.0.0.1:7833"

const cadenceFrontendService = "cadence-frontend"
const cadenceClientName = "cadence-client"

func (r *Runtime) createCadenceServices() (
	uclient.UnifiedClient,
	blobstore.BlobStore,
	interpreterWorker,
	error,
) {
	hostPort := r.cfg.Interpreter.Cadence.HostPort
	if hostPort == "" {
		hostPort = DefaultCadenceHostPort
	}
	domain := r.cfg.Interpreter.Cadence.Domain
	if domain == "" {
		domain = DefaultCadenceDomain
	}
	serviceClient, closeServiceClient, err := BuildCadenceServiceClient(hostPort)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("connect to Cadence: %w", err)
	}
	var closeOnce sync.Once
	closeClient := func() {
		closeOnce.Do(closeServiceClient)
	}
	dataConverter := dexconverter.NewCadenceDataConverter()
	cadenceClient, err := BuildCadenceClient(serviceClient, domain, dataConverter)
	if err != nil {
		closeClient()
		return nil, nil, nil, fmt.Errorf("create Cadence client: %w", err)
	}
	unifiedClient := cadenceapi.NewCadenceClient(
		domain,
		cadenceClient,
		serviceClient,
		dataConverter,
		closeClient,
		&r.cfg.Api.QueryWorkflowFailedRetryPolicy,
	)
	store, err := r.createBlobStore(context.Background(), domain, client.MetricsNopHandler)
	if err != nil {
		unifiedClient.Close()
		return nil, nil, nil, err
	}
	if !r.options.Services.Interpreter {
		return unifiedClient, store, nil, nil
	}
	worker := cadence.NewInterpreterWorker(
		r.cfg,
		serviceClient,
		domain,
		service.TaskQueue,
		closeClient,
		dataConverter,
		unifiedClient,
		store,
		r.workerPool,
	)
	return unifiedClient, store, worker, nil
}

func BuildCadenceClient(
	serviceClient workflowserviceclient.Interface,
	domain string,
	dataConverter encoded.DataConverter,
) (cadenceclient.Client, error) {
	return cadenceclient.NewClient(
		serviceClient,
		domain,
		&cadenceclient.Options{
			DataConverter: dataConverter,
			FeatureFlags: cadenceclient.FeatureFlags{
				WorkflowExecutionAlreadyCompletedErrorEnabled: true,
			},
		},
	), nil
}

func BuildCadenceServiceClient(hostPort string) (workflowserviceclient.Interface, func(), error) {
	dispatcher := yarpc.NewDispatcher(yarpc.Config{
		Name: cadenceClientName,
		Outbounds: yarpc.Outbounds{
			cadenceFrontendService: {Unary: grpc.NewTransport().NewSingleOutbound(hostPort)},
		},
	})
	if err := dispatcher.Start(); err != nil {
		return nil, nil, fmt.Errorf("start Cadence transport: %w", err)
	}
	clientConfig := dispatcher.ClientConfig(cadenceFrontendService)
	return compatibility.NewThrift2ProtoAdapter(
			apiv1.NewDomainAPIYARPCClient(clientConfig),
			apiv1.NewWorkflowAPIYARPCClient(clientConfig),
			apiv1.NewWorkerAPIYARPCClient(clientConfig),
			apiv1.NewVisibilityAPIYARPCClient(clientConfig),
		), func() {
			if err := dispatcher.Stop(); err != nil {
				log.Printf("stop Cadence transport: %v", err)
			}
		}, nil
}
