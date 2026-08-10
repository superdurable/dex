// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package cadence

import (
	"context"
	"fmt"

	"github.com/superdurable/dex/config"
	uclient "github.com/superdurable/dex/service/client"
	"github.com/superdurable/dex/service/common/attributestore"
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
	attributeStore *attributestore.Manager,
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
		attributeStore,
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
func (iw *InterpreterWorker) StartWithStickyCacheDisabledForTest() error {
	return iw.doStart(true)
}

func (iw *InterpreterWorker) Start() error {
	return iw.doStart(false)
}

func (iw *InterpreterWorker) doStart(disableStickyCache bool) error {
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
	iw.worker.RegisterActivity(iw.activities.SyncAttributeBatch)

	err := iw.worker.Start()
	if err != nil {
		return fmt.Errorf("start Cadence interpreter worker: %w", err)
	}

	if iw.cfg.BlobStore.Enabled {
		for _, storeCfg := range iw.cfg.BlobStore.SupportedStorages {
			cronSchedule, scheduleErr := storeCfg.CleanupStrategy.CronSchedule()
			if scheduleErr != nil {
				return fmt.Errorf("invalid blobstore cleanup strategy: %w", scheduleErr)
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
					return fmt.Errorf("start blobstore cleanup workflow: %w", err)
				}
			}
		}
	}
	return nil
}
