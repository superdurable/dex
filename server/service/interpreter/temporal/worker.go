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

package temporal

import (
	"context"
	"fmt"
	"log"

	"github.com/superdurable/iwf/config"
	"github.com/superdurable/iwf/service"
	uclient "github.com/superdurable/iwf/service/client"
	"github.com/superdurable/iwf/service/common/blobstore"
	"github.com/superdurable/iwf/service/common/event"
	"github.com/superdurable/iwf/service/common/workerclient"
	"github.com/superdurable/iwf/service/interpreter"
	"github.com/superdurable/iwf/service/interpreter/env"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

type InterpreterWorker struct {
	temporalClient client.Client
	worker         worker.Worker
	taskQueue      string
	workerPool     *workerclient.Pool
	internalClient *workerclient.InternalService
	unifiedClient  uclient.UnifiedClient
	activities     *interpreter.Activities
	temporalCfg    *config.TemporalConfig
	interpreterCfg *config.Interpreter
	externalCfg    *config.ExternalStorageConfig
	dataConverter  converter.DataConverter
}

func NewInterpreterWorker(
	apiCfg *config.ApiConfig,
	externalCfg *config.ExternalStorageConfig,
	interpreterCfg *config.Interpreter,
	temporalCfg *config.TemporalConfig,
	temporalClient client.Client,
	taskQueue string,
	dataConverter converter.DataConverter,
	unifiedClient uclient.UnifiedClient,
	store blobstore.BlobStore,
) *InterpreterWorker {
	if apiCfg == nil || externalCfg == nil || interpreterCfg == nil || temporalCfg == nil {
		panic("Temporal InterpreterWorker requires non-nil config sections")
	}
	if temporalClient == nil || dataConverter == nil || unifiedClient == nil || taskQueue == "" {
		panic("Temporal InterpreterWorker requires non-nil dependencies and task queue")
	}
	env.SetSharedEnv(config.Config{
		Api:             *apiCfg,
		Interpreter:     *interpreterCfg,
		ExternalStorage: *externalCfg,
	})
	pool, internal := interpreter.NewWorkerClients(apiCfg, &interpreterCfg.InterpreterActivityConfig)
	eventHandler := event.Handle
	activities := interpreter.NewActivities(
		&activityProvider{},
		pool,
		internal,
		unifiedClient,
		store,
		eventHandler,
		apiCfg,
		externalCfg,
		&interpreterCfg.InterpreterActivityConfig,
	)

	return &InterpreterWorker{
		temporalClient: temporalClient,
		taskQueue:      taskQueue,
		workerPool:     pool,
		internalClient: internal,
		unifiedClient:  unifiedClient,
		activities:     activities,
		temporalCfg:    temporalCfg,
		interpreterCfg: interpreterCfg,
		externalCfg:    externalCfg,
		dataConverter:  dataConverter,
	}
}

func (iw *InterpreterWorker) Close() {
	if iw.worker != nil {
		iw.worker.Stop()
	}
	if iw.workerPool != nil {
		iw.workerPool.Close()
	}
	if iw.internalClient != nil {
		iw.internalClient.Close()
	}
	iw.temporalClient.Close()
}

func (iw *InterpreterWorker) StartWithStickyCacheDisabledForTest() {
	iw.start(true)
}

func (iw *InterpreterWorker) Start() {
	iw.start(false)
}

func (iw *InterpreterWorker) start(disableStickyCache bool) {
	var options worker.Options

	if iw.temporalCfg.WorkerOptions != nil {
		options = *iw.temporalCfg.WorkerOptions
	}

	// override default
	if options.MaxConcurrentActivityTaskPollers == 0 {
		options.MaxConcurrentActivityTaskPollers = 10
	}

	// override default
	if options.MaxConcurrentWorkflowTaskPollers == 0 {
		// TODO: Keep this at 10; smaller values break continue-as-new persistence tests, likely due to Temporal SDK parallelism.
		options.MaxConcurrentWorkflowTaskPollers = 10
	}

	// When DisableStickyCache is true it can harm performance; should not be used in production environment
	if disableStickyCache {
		worker.SetStickyWorkflowCacheSize(0)
		fmt.Println("Temporal worker: Sticky cache disabled")
	}

	iw.worker = worker.New(iw.temporalClient, iw.taskQueue, options)
	worker.EnableVerboseLogging(iw.interpreterCfg.VerboseDebug)

	iw.worker.RegisterWorkflowWithOptions(
		iw.Interpreter,
		workflow.RegisterOptions{Name: service.InterpreterWorkflowName},
	)
	iw.worker.RegisterWorkflowWithOptions(
		BlobStoreCleanup,
		workflow.RegisterOptions{Name: service.BlobStoreCleanupWorkflowName},
	)
	iw.worker.RegisterActivityWithOptions(
		iw.activities.InvokeWaitForMethod,
		activity.RegisterOptions{Name: interpreter.InvokeWaitForMethodActivityName},
	)
	iw.worker.RegisterActivityWithOptions(
		iw.activities.InvokeExecuteMethod,
		activity.RegisterOptions{Name: interpreter.InvokeExecuteMethodActivityName},
	)
	iw.worker.RegisterActivityWithOptions(
		iw.activities.DumpFlowForContinueAsNew,
		activity.RegisterOptions{Name: interpreter.DumpFlowForContinueAsNewActivityName},
	)
	iw.worker.RegisterActivityWithOptions(
		iw.activities.InvokeWorkerRPC,
		activity.RegisterOptions{Name: interpreter.InvokeWorkerRPCActivityName},
	)
	iw.worker.RegisterActivityWithOptions(
		iw.activities.CleanupBlobStore,
		activity.RegisterOptions{Name: interpreter.CleanupBlobStoreActivityName},
	)

	err := iw.worker.Start()
	if err != nil {
		log.Fatalln("Unable to start worker", err)
	}

	if iw.externalCfg.Enabled {
		for _, storeCfg := range iw.externalCfg.SupportedStorages {
			if storeCfg.CleanupCronSchedule != "" {
				err = iw.unifiedClient.StartBlobStoreCleanupWorkflow(
					context.Background(), iw.taskQueue,
					"blobstore-cleanup-"+storeCfg.StorageId,
					storeCfg.CleanupCronSchedule,
					storeCfg.StorageId)
				if err != nil {
					if iw.unifiedClient.IsWorkflowAlreadyStartedError(err) {
						continue
					}
					log.Fatalln("Unable to start blobstore cleanup workflow", err)
				}
			}
		}
	}
}
