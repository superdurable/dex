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

package cadence

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
	"go.uber.org/cadence/.gen/go/cadence/workflowserviceclient"
	"go.uber.org/cadence/activity"
	"go.uber.org/cadence/encoded"
	"go.uber.org/cadence/worker"
	"go.uber.org/cadence/workflow"
)

type InterpreterWorker struct {
	service        workflowserviceclient.Interface
	closeFunc      func()
	domain         string
	worker         worker.Worker
	tasklist       string
	workerPool     *workerclient.Pool
	internalClient *workerclient.InternalService
	unifiedClient  uclient.UnifiedClient
	activities     *interpreter.Activities
	cadenceCfg     *config.CadenceConfig
	interpreterCfg *config.Interpreter
	externalCfg    *config.ExternalStorageConfig
	dataConverter  encoded.DataConverter
}

func NewInterpreterWorker(
	apiCfg *config.ApiConfig,
	externalCfg *config.ExternalStorageConfig,
	interpreterCfg *config.Interpreter,
	cadenceCfg *config.CadenceConfig,
	serviceClient workflowserviceclient.Interface,
	domain string,
	tasklist string,
	closeFunc func(),
	dataConverter encoded.DataConverter,
	unifiedClient uclient.UnifiedClient,
	store blobstore.BlobStore,
) *InterpreterWorker {
	if apiCfg == nil || externalCfg == nil || interpreterCfg == nil || cadenceCfg == nil {
		panic("Cadence InterpreterWorker requires non-nil config sections")
	}
	if serviceClient == nil || closeFunc == nil || dataConverter == nil || unifiedClient == nil {
		panic("Cadence InterpreterWorker requires non-nil dependencies")
	}
	if domain == "" || tasklist == "" {
		panic("Cadence InterpreterWorker requires domain and task list")
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
		service:        serviceClient,
		domain:         domain,
		tasklist:       tasklist,
		closeFunc:      closeFunc,
		workerPool:     pool,
		internalClient: internal,
		unifiedClient:  unifiedClient,
		activities:     activities,
		cadenceCfg:     cadenceCfg,
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
	iw.closeFunc()
}

func (iw *InterpreterWorker) StartWithStickyCacheDisabledForTest() {
	iw.start(true)
}

func (iw *InterpreterWorker) Start() {
	iw.start(false)
}

func (iw *InterpreterWorker) start(disableStickyCache bool) {
	var options worker.Options

	if iw.cadenceCfg.WorkerOptions != nil {
		options = *iw.cadenceCfg.WorkerOptions
	}
	options.DataConverter = iw.dataConverter

	if options.MaxConcurrentActivityTaskPollers == 0 {
		options.MaxConcurrentActivityTaskPollers = 10
	}
	if options.MaxConcurrentDecisionTaskPollers == 0 {
		options.MaxConcurrentDecisionTaskPollers = 10
	}

	if disableStickyCache {
		options.DisableStickyExecution = true
		fmt.Println("Cadence worker: Sticky cache disabled")
	}

	iw.worker = worker.New(iw.service, iw.domain, iw.tasklist, options)
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
					context.Background(), iw.tasklist,
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
