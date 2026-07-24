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

package integ

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/superdurable/iwf/cmd/server/iwf"
	"github.com/superdurable/iwf/gen/iwfidl"
	"github.com/superdurable/iwf/integ/helpers"
	"github.com/superdurable/iwf/integ/workflow/common"
	"github.com/superdurable/iwf/service"
	"github.com/superdurable/iwf/service/api"
	uclient "github.com/superdurable/iwf/service/client"
	cadenceapi "github.com/superdurable/iwf/service/client/cadence"
	temporalapi "github.com/superdurable/iwf/service/client/temporal"
	"github.com/superdurable/iwf/service/common/blobstore"
	iwfconverter "github.com/superdurable/iwf/service/common/converter"
	"github.com/superdurable/iwf/service/common/log/loggerimpl"
	"github.com/superdurable/iwf/service/interpreter/cadence"
	"github.com/superdurable/iwf/service/interpreter/temporal"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
)

const testNamespace = "default"

func createTemporalClient(dataConverter converter.DataConverter) client.Client {
	temporalClient, err := client.Dial(client.Options{
		HostPort:      *temporalHostPort,
		Namespace:     testNamespace,
		DataConverter: dataConverter,
	})
	if err != nil {
		log.Fatalf("unable to connect to Temporal %v", err)
	}
	return temporalClient
}

func startWorkflowWorkerWithRpc(handler common.WorkflowHandlerWithRpc, t *testing.T) (closeFunc func()) {
	router := gin.Default()
	router.POST(service.WorkflowWorkerRpcApi, func(c *gin.Context) {
		handler.ApiV1WorkflowWorkerRpc(c, t)
	})
	return doStartWorkflowWorker(handler, t, router)
}

func startWorkflowWorker(handler common.WorkflowHandler, t *testing.T) (closeFunc func()) {
	router := gin.Default()
	return doStartWorkflowWorker(handler, t, router)
}
func doStartWorkflowWorker(handler common.WorkflowHandler, t *testing.T, router *gin.Engine) (closeFunc func()) {
	router.POST(service.StateStartApi, func(c *gin.Context) {
		handler.ApiV1WorkflowStateStart(c, t)
	})
	router.POST(service.StateDecideApi, func(c *gin.Context) {
		handler.ApiV1WorkflowStateDecide(c, t)
	})

	wfServer := &http.Server{
		Addr:    ":" + testWorkflowServerPort,
		Handler: router,
	}
	go func() {
		if err := wfServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %s\n", err)
		}
	}()
	return func() { wfServer.Close() }
}

type IwfServiceTestConfig struct {
	BackendType     service.BackendType
	MemoEncryption  bool
	DefaultHeaders  map[string]string
	S3TestThreshold int
}

func startIwfService(backendType service.BackendType) (closeFunc func()) {
	_, cf := startIwfServiceWithClient(backendType)
	return cf
}

func startIwfServiceByConfig(config IwfServiceTestConfig) (uclient uclient.UnifiedClient, closeFunc func()) {
	return doStartIwfServiceWithClient(config)
}

func startIwfServiceWithClient(backendType service.BackendType) (uclient uclient.UnifiedClient, closeFunc func()) {
	return doStartIwfServiceWithClient(IwfServiceTestConfig{BackendType: backendType})

	//if backendType == service.BackendTypeTemporal {
	//if integTemporalUclientCached == nil {
	//	return doStartIwfServiceWithClient(backendType)
	//}
	//return integTemporalUclientCached, func() {}
	//}
	//if integCadenceUclientCached == nil {
	//	return doStartIwfServiceWithClient(backendType)
	//}
	//return integCadenceUclientCached, func() {}
}

// disable caching for now as it makes it difficult to test memo
//var integCadenceUclientCached api.UnifiedClient
//var integTemporalUclientCached api.UnifiedClient

// globalBlobStore is a global var in this package for testing
var globalBlobStore blobstore.BlobStore

func doStartIwfServiceWithClient(config IwfServiceTestConfig) (uclient uclient.UnifiedClient, closeFunc func()) {
	if config.BackendType == service.BackendTypeTemporal {
		dataConverter := iwfconverter.NewTemporalDataConverter()
		if config.MemoEncryption {
			dataConverter = encryptionDataConverter
		}

		temporalClient := createTemporalClient(dataConverter)
		logger, err := loggerimpl.NewDevelopment()
		if err != nil {
			panic(err)
		}

		testCfg := createTestConfig(config)
		s3Client := iwf.CreateS3Client(testCfg, context.Background())
		globalBlobStore = blobstore.NewBlobStore(s3Client, testNamespace, testCfg.ExternalStorage, logger, client.MetricsNopHandler)

		uclient = temporalapi.NewTemporalClient(temporalClient, testNamespace, dataConverter, config.MemoEncryption, &testCfg.Api.QueryWorkflowFailedRetryPolicy)
		iwfGrpc := api.NewServer(
			&testCfg.Api,
			&testCfg.ExternalStorage,
			&testCfg.Interpreter,
			uclient,
			logger,
			globalBlobStore,
			func(ctx context.Context) error { return nil },
		)
		go func() {
			if err := iwfGrpc.Run(); err != nil {
				log.Fatalf("iwf gRPC listen: %s\n", err)
			}
		}()

		// start iwf interpreter worker
		interpreter := temporal.NewInterpreterWorker(
			&testCfg.Api,
			&testCfg.ExternalStorage,
			&testCfg.Interpreter,
			testCfg.Interpreter.Temporal,
			temporalClient,
			service.TaskQueue,
			dataConverter,
			uclient,
			globalBlobStore,
		)
		if *disableStickyCache {
			interpreter.StartWithStickyCacheDisabledForTest()
		} else {
			interpreter.Start()
		}
		return uclient, func() {
			iwfGrpc.GracefulStop(2 * time.Second)
			interpreter.Close()
		}
	} else if config.BackendType == service.BackendTypeCadence {
		serviceClient, closeFunc, err := iwf.BuildCadenceServiceClient(iwf.DefaultCadenceHostPort)
		if err != nil {
			log.Fatalf("cannot connnect to Cadence %v", err)
		}

		dataConverter := iwfconverter.NewCadenceDataConverter()
		cadenceClient, err := iwf.BuildCadenceClient(serviceClient, iwf.DefaultCadenceDomain, dataConverter)

		logger, err := loggerimpl.NewDevelopment()
		if err != nil {
			panic(err)
		}

		testCfg := createTestConfig(config)
		s3Client := iwf.CreateS3Client(testCfg, context.Background())
		globalBlobStore = blobstore.NewBlobStore(s3Client, iwf.DefaultCadenceDomain, testCfg.ExternalStorage, logger, client.MetricsNopHandler)

		uclient = cadenceapi.NewCadenceClient(
			iwf.DefaultCadenceDomain,
			cadenceClient,
			serviceClient,
			dataConverter,
			closeFunc,
			&testCfg.Api.QueryWorkflowFailedRetryPolicy,
		)
		iwfGrpc := api.NewServer(
			&testCfg.Api,
			&testCfg.ExternalStorage,
			&testCfg.Interpreter,
			uclient,
			logger,
			globalBlobStore,
			func(ctx context.Context) error { return nil },
		)
		go func() {
			if err := iwfGrpc.Run(); err != nil {
				log.Fatalf("iwf gRPC listen: %s\n", err)
			}
		}()

		// start iwf interpreter worker
		interpreter := cadence.NewInterpreterWorker(
			&testCfg.Api,
			&testCfg.ExternalStorage,
			&testCfg.Interpreter,
			testCfg.Interpreter.Cadence,
			serviceClient,
			iwf.DefaultCadenceDomain,
			service.TaskQueue,
			closeFunc,
			dataConverter,
			uclient,
			globalBlobStore,
		)
		if *disableStickyCache {
			interpreter.StartWithStickyCacheDisabledForTest()
		} else {
			interpreter.Start()
		}
		return uclient, func() {
			iwfGrpc.GracefulStop(2 * time.Second)
			interpreter.Close()
		}
	} else {
		panic("not supported backend type " + config.BackendType)
	}
}

func failTestAtError(err error, t *testing.T) {
	if err != nil {
		helpers.FailTestWithError(err, t)
	}
}

func failTestAtHttpError(err error, httpResp *http.Response, t *testing.T) {
	if err != nil {
		helpers.FailTestWithError(err, t)
	}
	if httpResp.StatusCode != http.StatusOK {
		helpers.FailTestWithErrorMessage(fmt.Sprintf("HTTP status not success: %v", httpResp.Status), t)
	}
}

func failTestAtHttpErrorOrWorkflowUncompleted(err error, httpResp *http.Response, resp *iwfidl.WorkflowGetResponse, t *testing.T) {
	if err != nil {
		helpers.FailTestWithError(err, t)
	}
	if httpResp.StatusCode != http.StatusOK {
		helpers.FailTestWithErrorMessage(fmt.Sprintf("HTTP status not success: %v", httpResp.Status), t)
	}
	if resp.WorkflowStatus != iwfidl.COMPLETED {
		helpers.FailTestWithErrorMessage(fmt.Sprintf("Workflow uncompleted: %v", resp.WorkflowStatus), t)
	}
}

func smallWaitForFastTest() {
	du := time.Millisecond * time.Duration(*repeatInterval)
	if *repeatIntegTest == 0 {
		du = time.Millisecond
	}
	time.Sleep(du)
}

func minimumContinueAsNewConfig(optimizeActivity bool) *iwfidl.WorkflowConfig {
	return &iwfidl.WorkflowConfig{
		ContinueAsNewThreshold: iwfidl.PtrInt32(1),
		OptimizeActivity:       iwfidl.PtrBool(optimizeActivity),
	}
}

func minimumGreedyTimerConfig() *iwfidl.WorkflowConfig {
	return greedyTimerConfig(false)
}

func greedyTimerConfig(continueAsNew bool) *iwfidl.WorkflowConfig {
	if continueAsNew {
		return &iwfidl.WorkflowConfig{
			ContinueAsNewThreshold: iwfidl.PtrInt32(1),
			OptimizeTimer:          iwfidl.PtrBool(true),
		}
	}

	return &iwfidl.WorkflowConfig{
		OptimizeTimer: iwfidl.PtrBool(true),
	}
}

func minimumContinueAsNewConfigV0() *iwfidl.WorkflowConfig {
	return minimumContinueAsNewConfig(false)
}

func getBackendTypes() []service.BackendType {
	backendTypesToTest := []service.BackendType{}

	if *temporalIntegTest {
		backendTypesToTest = append(backendTypesToTest, service.BackendTypeTemporal)
	}

	if *cadenceIntegTest {
		backendTypesToTest = append(backendTypesToTest, service.BackendTypeCadence)
	}

	return backendTypesToTest
}
