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

	"github.com/superdurable/dex/config"
	uclient "github.com/superdurable/dex/service/client"
	"github.com/superdurable/dex/service/common/blobstore"
	"github.com/superdurable/dex/service/common/event"
	"github.com/superdurable/dex/service/common/workerclient"
	"github.com/superdurable/dex/service/interpreter"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/worker"
)

type InterpreterWorker struct {
	temporalClient client.Client
	worker         worker.Worker
	taskQueue      string
	internalClient *workerclient.InternalServiceClient
	unifiedClient  uclient.UnifiedClient
	activities     *interpreter.Activities
	workflow       *interpreter.Interpreter
	cfg            *config.Config
	dataConverter  converter.DataConverter
}

func NewInterpreterWorker(
	cfg *config.Config,
	temporalClient client.Client,
	taskQueue string,
	dataConverter converter.DataConverter,
	unifiedClient uclient.UnifiedClient,
	store blobstore.BlobStore,
	workerPool *workerclient.WorkerClientPool,
) *InterpreterWorker {
	if cfg == nil {
		panic("Temporal InterpreterWorker requires non-nil config sections")
	}
	if temporalClient == nil || dataConverter == nil || unifiedClient == nil ||
		workerPool == nil || taskQueue == "" {
		panic("Temporal InterpreterWorker requires non-nil dependencies and task queue")
	}
	internal := interpreter.NewInternalServiceClient(cfg)
	eventHandler := event.Handle
	activities := interpreter.NewActivities(
		&activityProvider{},
		workerPool,
		internal,
		unifiedClient,
		store,
		eventHandler,
		cfg,
	)
	workflowInterpreter := interpreter.NewInterpreter(cfg, activities)

	return &InterpreterWorker{
		temporalClient: temporalClient,
		taskQueue:      taskQueue,
		internalClient: internal,
		unifiedClient:  unifiedClient,
		activities:     activities,
		workflow:       workflowInterpreter,
		cfg:            cfg,
		dataConverter:  dataConverter,
	}
}

// NewWorkerForReplay builds a worker that can register Engine for Temporal history replay.
// Activity results come from history; Activities is never invoked for real.
func NewWorkerForReplay() *InterpreterWorker {
	return &InterpreterWorker{
		workflow: interpreter.NewInterpreter(&config.Config{}, &interpreter.Activities{}),
	}
}

func (iw *InterpreterWorker) Close() {
	if iw.worker != nil {
		iw.worker.Stop()
	}
	if iw.internalClient != nil {
		iw.internalClient.Close()
	}
	iw.temporalClient.Close()
}

// StartWithStickyCacheDisabledForTest can harm performance; should not be used in production environment
func (iw *InterpreterWorker) StartWithStickyCacheDisabledForTest() {
	iw.start(true)
}

func (iw *InterpreterWorker) Start() {
	iw.start(false)
}

func (iw *InterpreterWorker) start(disableStickyCache bool) {
	var options worker.Options

	if iw.cfg.Interpreter.Temporal.WorkerOptions != nil {
		options = *iw.cfg.Interpreter.Temporal.WorkerOptions
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
	worker.EnableVerboseLogging(iw.cfg.Interpreter.VerboseDebug)

	iw.worker.RegisterWorkflow(iw.Engine)
	iw.worker.RegisterWorkflow(iw.BlobStoreCleanup)
	iw.worker.RegisterActivity(iw.activities.InvokeWaitForMethod)
	iw.worker.RegisterActivity(iw.activities.InvokeExecuteMethod)
	iw.worker.RegisterActivity(iw.activities.DumpFlowForContinueAsNew)
	iw.worker.RegisterActivity(iw.activities.InvokeWorkerRPC)
	iw.worker.RegisterActivity(iw.activities.CleanupBlobsAfterAllRunsDeleted)

	err := iw.worker.Start()
	if err != nil {
		log.Fatalln("Unable to start worker", err)
	}

	if iw.cfg.ExternalStorage.Enabled {
		for _, storeCfg := range iw.cfg.ExternalStorage.SupportedStorages {
			cronSchedule, scheduleErr := storeCfg.CleanupStrategy.CronSchedule()
			if scheduleErr != nil {
				log.Fatalln("Invalid blobstore cleanup strategy", scheduleErr)
			}
			if cronSchedule != "" {
				err = iw.unifiedClient.StartBlobStoreCleanupWorkflow(
					context.Background(), iw.taskQueue,
					"blobstore-cleanup-"+storeCfg.StorageId,
					cronSchedule,
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
