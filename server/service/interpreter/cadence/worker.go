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

	"github.com/superdurable/dex/config"
	uclient "github.com/superdurable/dex/service/client"
	"github.com/superdurable/dex/service/common/blobstore"
	"github.com/superdurable/dex/service/common/event"
	"github.com/superdurable/dex/service/common/workerclient"
	"github.com/superdurable/dex/service/interpreter"
	"go.uber.org/cadence/.gen/go/cadence/workflowserviceclient"
	"go.uber.org/cadence/encoded"
	"go.uber.org/cadence/worker"
)

type InterpreterWorker struct {
	service        workflowserviceclient.Interface
	closeFunc      func()
	domain         string
	worker         worker.Worker
	tasklist       string
	internalClient *workerclient.InternalServiceClient
	unifiedClient  uclient.UnifiedClient
	activities     *interpreter.Activities
	workflow       *interpreter.Interpreter
	cfg            *config.Config
	dataConverter  encoded.DataConverter
}

func NewInterpreterWorker(
	cfg *config.Config,
	serviceClient workflowserviceclient.Interface,
	domain string,
	tasklist string,
	closeFunc func(),
	dataConverter encoded.DataConverter,
	unifiedClient uclient.UnifiedClient,
	store blobstore.BlobStore,
	workerPool *workerclient.WorkerClientPool,
) *InterpreterWorker {
	if cfg == nil {
		panic("requires non-nil config sections")
	}
	if serviceClient == nil || closeFunc == nil || dataConverter == nil ||
		unifiedClient == nil || workerPool == nil {
		panic("Cadence InterpreterWorker requires non-nil dependencies")
	}
	if domain == "" || tasklist == "" {
		panic("Cadence InterpreterWorker requires domain and task list")
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
		service:        serviceClient,
		domain:         domain,
		tasklist:       tasklist,
		closeFunc:      closeFunc,
		internalClient: internal,
		unifiedClient:  unifiedClient,
		activities:     activities,
		workflow:       workflowInterpreter,
		cfg:            cfg,
		dataConverter:  dataConverter,
	}
}

func (iw *InterpreterWorker) Close() {
	if iw.worker != nil {
		iw.worker.Stop()
	}
	if iw.internalClient != nil {
		iw.internalClient.Close()
	}
	iw.closeFunc()
}

// StartWithStickyCacheDisabledForTest can harm performance; should not be used in production environment
func (iw *InterpreterWorker) StartWithStickyCacheDisabledForTest() {
	iw.doStart(true)
}

func (iw *InterpreterWorker) Start() {
	iw.doStart(false)
}

func (iw *InterpreterWorker) doStart(disableStickyCache bool) {
	var options worker.Options

	if iw.cfg.Interpreter.Cadence.WorkerOptions != nil {
		options = *iw.cfg.Interpreter.Cadence.WorkerOptions
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
					context.Background(), iw.tasklist,
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
