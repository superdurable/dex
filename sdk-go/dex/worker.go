// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Sustainable Use License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package dex

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/superdurable/dex/blob-cache-go/blobcache"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

const (
	defaultFlowServiceTarget         = "localhost:8801"
	defaultAttributeIndexSyncTimeout = 2 * time.Minute
)

// WorkerOptions configures a Worker that receives tasks over outbound streams.
//
// Zero values connect to the local Dex Server with plaintext and default pull settings.
// Workers need only an outbound connection to the Dex Server.
type WorkerOptions struct {
	// Pull configures task capacity, registration, and shutdown. Nil uses PullWorkerOptions defaults.
	Pull *PullWorkerOptions
	// FlowServiceAddress is the Dex gRPC endpoint used for startup synchronization, task streams, and blob hydration. Default: "localhost:8801".
	// With FlowServiceTLS, use host:port, dns:///host:port, or dns://authority/host:port, such as "dex.example.com:443".
	// Startup synchronization registers Attribute indexes.
	FlowServiceAddress string
	// FlowServiceTLS enables TLS for this Worker's FlowService connection. Nil uses plaintext.
	// See FlowServiceTLSOptions for trust roots, server name, and a complete example.
	FlowServiceTLS *FlowServiceTLSOptions
	// AttributeIndexSyncTimeout bounds index registration at startup. Default: two minutes.
	// Negative durations are invalid.
	AttributeIndexSyncTimeout time.Duration
	// Logger defaults to the shared BlobCache logger.
	// It receives concurrent structured Worker logs and falls back to slog.Default.
	Logger Logger
	// Credential is sent as a bearer credential with the Worker's FlowService calls: startup index
	// verification, task streams, and blob hydration. Default: nil sends none. A Dex server with authentication enabled requires it.
	Credential Credential
	// GrpcMaxMessageBytes limits the gRPC messages the Worker receives, in bytes: Step and RPC
	// invocations on outbound task streams and FlowService responses such as each LoadBlobs
	// response used to hydrate values. Zero uses DefaultGrpcMaxMessageBytes (32 MiB) and keeps
	// gRPC's unlimited send limit. A positive value also limits the messages the Worker sends,
	// including Step and RPC results. Negative values are invalid. The Dex server applies its own
	// api.grpcMaxMessageBytes, and the smaller limit wins.
	// Keep it above the largest application value plus headroom for the message envelope.
	GrpcMaxMessageBytes int
	// FlowDefinitionPaths lists Flow Definition Graph (FDG 2.0) JSON files or directories to upload at Start.
	// Default: nil uploads none. Directories are read recursively, and only their ".json" files are read.
	// ReadFlowDefinitionFiles describes how each file becomes a FlowDefinitionDocument.
	// NewWorker reads the files and returns an error for an unreadable or invalid file.
	// Dex Web then draws the graph and offers the Actions of these Flow types without a local
	// Flow Rendering directory. See Start for the upload rules.
	//
	//	worker, err := dex.NewWorker(registry, cache, dex.WorkerOptions{
	//		FlowDefinitionPaths: []string{"flow-definitions"},
	//	})
	//
	// Since 1.45.0.
	FlowDefinitionPaths []string
	// FlowDefinitions lists in-memory FDG 2.0 documents to upload at Start, alongside FlowDefinitionPaths.
	// Default: nil uploads none. Each document needs a FlowType equal to its flow.name and a non-zero
	// GeneratedAt; NewWorker returns an error otherwise. NewWorker also returns an error when a Flow type
	// appears twice, including once here and once in FlowDefinitionPaths.
	// See FlowDefinitionDocument for an example.
	//
	// Since 1.45.0.
	FlowDefinitions []FlowDefinitionDocument
}

// PullWorkerOptions configures outbound execution slot streams.
//
// Use it with WorkerOptions.Pull to tune outbound task streams.
// MaxConcurrentTasks defaults to runtime.NumCPU; each task slot owns one long-lived stream.
// StopDrainTimeout defaults to 30 seconds.
// Changed in SDK v1.35.0: streams represent execution slots instead of per-queue idle capacity.
// The total stream count stays MaxConcurrentTasks while idle or busy, including RPC-only slots.
// Budget server matching.maxStreams for all Worker slots, including rolling-deployment overlap.
// Each stream runs one task at a time. Negative task limits or drain timeouts and an invalid
// AppWorkerID cause NewWorker to return an error.
//
//	worker, err := dex.NewWorker(registry, cache, dex.WorkerOptions{
//		Pull: &dex.PullWorkerOptions{MaxConcurrentTasks: 8},
//	})
//	if err != nil { return err }
//	defer worker.Stop(context.Background())
type PullWorkerOptions struct {
	// StopOnPermanentStreamRejection makes Start return a permanent rejection when its slot kind has no registered stream.
	// Default: false retries indefinitely. Slot kinds are STEP_AND_RPC and, when reserved, RPC_ONLY.
	// A rejection while another stream of that kind remains registered is recorded and retried.
	// Before or after readiness, a fatal rejection drains all streams within StopDrainTimeout,
	// then cancels them and waits for handlers. Handlers must respond to cancellation.
	// Always call Stop before exiting; it remains safe after Start returns.
	StopOnPermanentStreamRejection bool
	// MaxConcurrentTasks sets the number of long-lived execution slot streams and bounds simultaneous handlers.
	// Zero uses runtime.NumCPU(); negative values are invalid. Each idle stream can accept one task.
	// Start fails when this exceeds the Server's reported matching.maxConcurrentTasksCap per Worker.
	// The server cap defaults to 256. Raise it before starting a Worker that needs more slots.
	// Changed in SDK v1.35.0: exceeding the first registration's cap fails startup without background retry.
	// A smaller cap on reconnect logs a warning and lets the Server reject surplus streams; the Worker keeps running.
	MaxConcurrentTasks int
	// ReservedRPCTasks sets the number of execution slot streams that accept only RPCs.
	// Nil reserves one when resolved MaxConcurrentTasks is at least two, otherwise zero.
	// Set ptr.Any(0) to opt out. With one slot and nil reserve, Start warns that RPCs may wait behind Steps.
	// The remaining streams accept Steps and RPCs. The Worker executes every received task without capacity declines.
	// Changed in SDK v1.35.0: reserved slots use RPC_ONLY streams; Steps wait in the task queue for a shared slot.
	// To keep eight simultaneous Step handlers with one reserved slot, set MaxConcurrentTasks to at least nine.
	// A reserve provides capacity, not a latency guarantee: RPC-only slots can themselves be busy.
	// NewWorker returns an error unless 0 <= reserve < resolved MaxConcurrentTasks.
	// For example: PullWorkerOptions{MaxConcurrentTasks: 8, ReservedRPCTasks: ptr.Any(2)} creates six shared and two RPC-only slots.
	ReservedRPCTasks *int
	// IdleStreamsPerQueue is accepted for source compatibility and has no effect. Default: zero.
	//
	// Changed in SDK v1.35.0: this option no longer changes stream counts.
	//
	// Deprecated: MaxConcurrentTasks determines the fixed number of slot streams.
	IdleStreamsPerQueue int
	// AppWorkerID overrides the generated process identity. Empty generates identity plus eight base32 characters.
	// Set a new value after each process restart. It must not equal a registered Flow type.
	// Valid values contain 1-63 ASCII letters, digits, dots, underscores, or hyphens.
	AppWorkerID string
	// StopDrainTimeout bounds Stop after it begins draining. Zero uses 30 seconds; negative values are invalid.
	StopDrainTimeout time.Duration
	// TaskQueue names the one task queue this Worker serves. Empty, the default, serves the default task queue.
	//
	// The Worker receives the Steps and RPCs only of Flows started with the same
	// StartFlowOptions.TaskQueue, or inherited by a SubFlow. A Flow on another task queue
	// never reaches it, even when the Worker registers that Flow type. To serve several task
	// queues, run one Worker per task queue, each with its own AppWorkerID; Workers can share
	// one process and Registry.
	//
	// A name has 1 to 63 characters from A-Z, a-z, 0-9, '.', '_', and '-'. Names starting with
	// "dex.", including DefaultTaskQueue, are reserved; NewWorker returns an error for them and
	// for any other invalid name.
	// A named task queue requires Server protocol version 5 or later. Start returns
	// *TaskQueueUnsupportedError for an older Server. When a Server registers a stream on another
	// task queue, for example after a rollback to an older Server, the Worker closes that stream
	// and records a permanent rejection with codes.InvalidArgument. PullRegistrationState and
	// WaitPullRegistered report it like any other permanent rejection.
	//
	//	worker, err := dex.NewWorker(registry, cache, dex.WorkerOptions{
	//		Pull: &dex.PullWorkerOptions{TaskQueue: "tenant-42"},
	//	})
	//
	// Since 1.46.0.
	TaskQueue string
}

// PullRegistrationError identifies a permanent rejection that leaves a slot kind without a registered stream.
//
// WaitPullRegistered returns this error for such a rejection before startup readiness.
// Start also returns it when StopOnPermanentStreamRejection is enabled. Use
// errors.As to inspect Queue and status.Code on Err to inspect the gRPC status:
//
//	var rejection *dex.PullRegistrationError
//	if errors.As(err, &rejection) {
//		log.Printf("queue %s rejected: %s", rejection.Queue, status.Code(rejection.Err))
//	}
//
// SDK-produced values always have a nonempty Queue and non-nil Err. Treat returned
// errors as immutable; they can be shared by concurrent readiness callers.
type PullRegistrationError struct {
	// Queue contains POLL_WORKER_SLOT_KIND_STEP_AND_RPC or POLL_WORKER_SLOT_KIND_RPC_ONLY.
	// It identifies the rejected slot kind; slot streams serve all registered Flow types and have no individual queue.
	Queue string
	// TaskQueue is the Worker's PullWorkerOptions.TaskQueue; empty means the default task queue.
	//
	// Since 1.46.0.
	TaskQueue string
	// Err is the non-nil gRPC status error of the rejected stream. Usually the Server returned it.
	// When the Server registered the stream on another task queue than TaskQueue, the SDK closes the
	// stream and creates this error itself, with codes.InvalidArgument.
	Err error
}

// Error returns the slot kind, the named task queue if any, and the underlying rejection for diagnostics.
// It does not change the error; concurrent calls are safe on immutable values.
func (rejection *PullRegistrationError) Error() string {
	if rejection.TaskQueue != "" {
		return fmt.Sprintf("dex: pull slot kind %q on task queue %q registration rejected: %v", rejection.Queue, rejection.TaskQueue, rejection.Err)
	}
	return fmt.Sprintf("dex: pull slot kind %q registration rejected: %v", rejection.Queue, rejection.Err)
}

// Unwrap returns Err so errors.Is, errors.As, and gRPC status inspection retain the rejection.
// SDK-produced errors return a non-nil error. See PullRegistrationError for usage.
func (rejection *PullRegistrationError) Unwrap() error { return rejection.Err }

// PullRegistrationState is an independent snapshot for a pull Worker's readiness probe.
//
// Obtain it with Worker.PullRegistrationState, including concurrently with Start,
// Stop, or WaitPullRegistered. Its zero value has IsReady false and Queues nil.
// Modifying Queues does not reconfigure the Worker.
// Changed in SDK v1.35.0: Queues identifies slot kinds, not individual Flow type or app Worker ID queues.
// Treat errors inside entries as immutable. See WaitPullRegistered for a complete
// startup and HTTP readiness probe example.
type PullRegistrationState struct {
	// IsReady becomes true after each required slot kind has registered at least once.
	// Afterwards it is false only if a required slot kind has no registered stream and a permanent error.
	// Transient failures and ownership moves preserve readiness. Successful registration clears that slot kind's error.
	IsReady bool
	// Queues contains STEP_AND_RPC followed by RPC_ONLY when a reservation is configured.
	// Each Queue uses the full POLL_WORKER_SLOT_KIND_ enum name for compatibility with registration errors.
	// The Worker returns a fresh non-nil slice.
	Queues []PullQueueRegistrationState
}

// PullQueueRegistrationState describes one slot kind in a PullRegistrationState snapshot.
//
// Inspect these entries to diagnose a failing readiness probe. A slot kind may have
// both a registered stream and LastPermanentError when another stream was rejected.
// The snapshot owns its values; errors are shared immutable errors. See
// WaitPullRegistered for the startup and readiness probe example.
type PullQueueRegistrationState struct {
	// Queue is POLL_WORKER_SLOT_KIND_STEP_AND_RPC or POLL_WORKER_SLOT_KIND_RPC_ONLY.
	Queue string
	// TaskQueue is the task queue the slot kind serves, from PullWorkerOptions.TaskQueue.
	// Empty means the default task queue. Every entry of one Worker has the same TaskQueue.
	//
	// Since 1.46.0.
	TaskQueue string
	// HasRegisteredStream reports whether at least one acknowledged stream is currently held.
	HasRegisteredStream bool
	// LastPermanentError is the last permanent gRPC status error, or nil if none remains.
	// A successful registration clears it. Transient failures do not overwrite it.
	LastPermanentError error
}

// Worker hosts registered Flows through outbound pull streams.
//
// A Worker is one-shot: construct it, call Start once, then call Stop during
// shutdown. Start blocks while serving. Step and RPC handlers may execute
// concurrently, so application handler state must provide its own synchronization.
//
//	worker, err := dex.NewWorker(registry, cache, dex.WorkerOptions{})
//	if err != nil {
//		return err
//	}
//	go func() { serveErr <- worker.Start() }()
//	defer worker.Stop(context.Background())
type Worker struct {
	registry                  *Registry
	flowConn                  *grpc.ClientConn
	flowService               dexpb.FlowServiceClient
	attributeIndexSyncTimeout time.Duration
	sdkVersion                string
	serverProtocol            *negotiatedServerProtocol
	logger                    Logger
	pull                      *pullWorker
	flowDefinitions           []FlowDefinitionDocument
	// cancelFlowDefinitionUpload is set under lifecycleMu before Start uploads; Stop calls it when set.
	cancelFlowDefinitionUpload context.CancelFunc

	lifecycleMu sync.Mutex
	state       workerState
	done        chan struct{}
	finishOnce  sync.Once
}

type workerState uint8

const (
	workerCreated workerState = iota
	workerRunning
	workerStopping
	workerStopped
)

// NewWorker constructs a one-shot Worker from shared dependencies.
//
// registry supplies definitions and cache hydrates large values. It panics when
// registry or cache is nil. It returns ClientOnlyFlowError, before it opens any
// connection, when registry holds a Flow that embeds ClientOnlyFlow; see
// ClientOnlyFlow. It validates the FlowService address, FlowServiceTLS,
// synchronization timeout, GrpcMaxMessageBytes, and pull options, then creates an
// outbound FlowService connection without opening task streams. The returned
// Worker owns that connection; Start or Stop closes it.
//
// Changed in 1.46.0: NewWorker returns ClientOnlyFlowError for a client-only Flow.
func NewWorker(
	registry *Registry,
	cache *blobcache.Cache,
	options WorkerOptions,
) (*Worker, error) {
	if registry == nil {
		panic("dex.NewWorker requires Registry")
	}
	if cache == nil {
		panic("dex.NewWorker requires BlobCache")
	}
	if flowType, hasClientOnlyFlow := registry.firstClientOnlyFlowType(); hasClientOnlyFlow {
		return nil, &ClientOnlyFlowError{FlowType: flowType}
	}
	flowServiceAddress, syncTimeout, err := resolveWorkerOptions(options)
	if err != nil {
		return nil, err
	}
	messageLimits, err := resolveGrpcMessageLimits(options.GrpcMaxMessageBytes)
	if err != nil {
		return nil, err
	}
	logger := resolveLogger(options.Logger, cache.Logger())
	flowDefinitions, err := resolveWorkerFlowDefinitions(options, logger)
	if err != nil {
		return nil, err
	}
	dialOptions, err := messageLimits.flowServiceDialOptions(flowServiceAddress, options.Credential, options.FlowServiceTLS)
	if err != nil {
		return nil, err
	}
	dialOptions = append(dialOptions, grpc.WithKeepaliveParams(keepalive.ClientParameters{
		Time: 30 * time.Second, Timeout: 10 * time.Second, PermitWithoutStream: false,
	}))
	flowConn, err := grpc.NewClient(flowServiceAddress, dialOptions...)
	if err != nil {
		return nil, fmt.Errorf("dex: create FlowService client: %w", err)
	}

	flowService := dexpb.NewFlowServiceClient(flowConn)
	serverProtocol := &negotiatedServerProtocol{}
	service := newWorkerService(
		registry,
		newValueHydrator(
			flowService,
			cache,
			logger,
			flowServiceAddress,
		),
		logger,
		serverProtocol,
	)
	pullOptions := PullWorkerOptions{}
	if options.Pull != nil {
		pullOptions = *options.Pull
	}
	pull, err := newPullWorker(registry, service, flowConn, pullOptions, currentGoSDKVersion())
	if err != nil {
		if closeErr := flowConn.Close(); closeErr != nil {
			logger.Error("close Worker FlowService connection", "error", closeErr)
		}
		return nil, err
	}
	return &Worker{
		registry:                  registry,
		flowConn:                  flowConn,
		flowService:               flowService,
		attributeIndexSyncTimeout: syncTimeout,
		sdkVersion:                currentGoSDKVersion(),
		serverProtocol:            serverProtocol,
		logger:                    logger,
		pull:                      pull,
		flowDefinitions:           flowDefinitions,
		state:                     workerCreated,
		done:                      make(chan struct{}),
	}, nil
}

func resolveWorkerOptions(
	options WorkerOptions,
) (string, time.Duration, error) {
	flowServiceAddress := strings.TrimSpace(options.FlowServiceAddress)
	if flowServiceAddress == "" {
		flowServiceAddress = defaultFlowServiceTarget
	}
	if options.FlowServiceTLS == nil {
		if err := validatePlaintextTarget(flowServiceAddress); err != nil {
			return "", 0, fmt.Errorf(
				"dex: invalid FlowService address: %w",
				err,
			)
		}
	}
	syncTimeout := options.AttributeIndexSyncTimeout
	if syncTimeout < 0 {
		return "", 0, fmt.Errorf(
			"dex: Attribute index sync timeout must be positive",
		)
	}
	if syncTimeout == 0 {
		syncTimeout = defaultAttributeIndexSyncTimeout
	}
	return flowServiceAddress, syncTimeout, nil
}

func resolveWorkerFlowDefinitions(options WorkerOptions, logger Logger) ([]FlowDefinitionDocument, error) {
	var fromPaths []FlowDefinitionDocument
	if len(options.FlowDefinitionPaths) > 0 {
		documents, err := ReadFlowDefinitionFiles(options.FlowDefinitionPaths, logger)
		if err != nil {
			return nil, err
		}
		fromPaths = documents
	}
	return mergeFlowDefinitionDocuments(fromPaths, options.FlowDefinitions)
}

func validatePlaintextTarget(address string) error {
	trimmed := strings.TrimSpace(address)
	if trimmed != address {
		return fmt.Errorf("target address must not contain surrounding whitespace")
	}
	lower := strings.ToLower(trimmed)
	if lower == "" {
		return fmt.Errorf("target address must not be empty")
	}
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		return fmt.Errorf("target %q must use plaintext gRPC", address)
	}
	return nil
}

// Start serves Worker tasks and blocks until Stop or a serve failure.
//
// Changed in SDK v1.35.0: Server protocol 4 is required (server-zd v0.1.17 or later).
// Upgrade the Server before upgrading the SDK; older SDKs can continue using the upgraded Server.
//
// Start may be called exactly once. Before serving, it negotiates a Server
// protocol (version 4 or 5), synchronizes Attribute indexes, and waits up to
// WorkerOptions.AttributeIndexSyncTimeout for each startup request.
// With PullWorkerOptions.TaskQueue set, Start returns *TaskQueueUnsupportedError when the
// negotiated protocol is older than version 5. Since 1.46.0.
// It then uploads the Flow definitions of WorkerOptions.FlowDefinitionPaths and
// WorkerOptions.FlowDefinitions whose Flow type is registered in the Registry, and
// logs the skipped ones. Upload failures, including a Server without the upload API,
// are logged as warnings and never stop Start. Since 1.45.0.
// Start fails without retry if MaxConcurrentTasks exceeds the limit reported by the first
// registration after Start. A lower limit reported on a later reconnect only logs a warning;
// the Server rejects any surplus stream, and the Worker keeps running.
// It returns nil after a normal Stop, or an error for synchronization, polling,
// or invalid lifecycle state failures. Call Start on a dedicated goroutine when
// the application must continue doing other work.
// Poll stream failures are retried while the Worker runs; registration failures
// are logged with a longer retry delay unless PullWorkerOptions.StopOnPermanentStreamRejection is enabled.
func (worker *Worker) Start() (startErr error) {
	worker.lifecycleMu.Lock()
	if worker.state != workerCreated {
		state := worker.state
		worker.lifecycleMu.Unlock()
		return fmt.Errorf("dex: Worker cannot start from state %s", state)
	}
	defer func() { worker.pull.finishRegistration(startErr) }()
	serverInfoCtx, cancelServerInfo := context.WithTimeout(
		context.Background(),
		worker.attributeIndexSyncTimeout,
	)
	serverInfo, err := worker.flowService.GetServerInfo(serverInfoCtx, &emptypb.Empty{})
	cancelServerInfo()
	if err != nil {
		worker.state = workerStopped
		worker.lifecycleMu.Unlock()
		worker.finish()
		return serverProtocolRequestError(worker.sdkVersion, err)
	}
	negotiatedProtocolVersion, err := negotiateServerProtocol(serverInfo, worker.sdkVersion)
	if err != nil {
		worker.state = workerStopped
		worker.lifecycleMu.Unlock()
		worker.finish()
		return err
	}
	worker.serverProtocol.version.Store(negotiatedProtocolVersion)
	if err := worker.serverProtocol.requireTaskQueueSupport(worker.pull.taskQueue); err != nil {
		worker.state = workerStopped
		worker.lifecycleMu.Unlock()
		worker.finish()
		return err
	}
	syncCtx, cancelSync := context.WithTimeout(context.Background(), worker.attributeIndexSyncTimeout)
	_, err = worker.flowService.SyncAttributeIndexes(syncCtx, &dexpb.SyncAttributeIndexRequest{
		AttributeIndexes: worker.registry.attributeIndexes,
	})
	cancelSync()
	if err != nil {
		worker.state = workerStopped
		worker.lifecycleMu.Unlock()
		worker.finish()
		return fmt.Errorf("dex: sync attribute indexes: %w", err)
	}
	uploadCtx, cancelUpload := context.WithTimeout(context.Background(), worker.attributeIndexSyncTimeout)
	worker.cancelFlowDefinitionUpload = cancelUpload
	worker.state = workerRunning
	worker.lifecycleMu.Unlock()
	worker.uploadRegisteredFlowDefinitions(uploadCtx)
	cancelUpload()
	startErr = worker.pull.run()
	worker.lifecycleMu.Lock()
	worker.state = workerStopped
	worker.lifecycleMu.Unlock()
	worker.finish()
	return startErr
}

// uploadRegisteredFlowDefinitions logs every failure because upload must never stop Start.
// uploadCtx is one deadline for every batch; Stop cancels it.
func (worker *Worker) uploadRegisteredFlowDefinitions(uploadCtx context.Context) {
	if len(worker.flowDefinitions) == 0 {
		return
	}
	documents := make([]FlowDefinitionDocument, 0, len(worker.flowDefinitions))
	for _, document := range worker.flowDefinitions {
		if _, isRegistered := worker.registry.flows[document.FlowType]; !isRegistered {
			worker.logger.Info("dex: skip Flow definition of a Flow type this Worker does not register", "flowType", document.FlowType)
			continue
		}
		documents = append(documents, document)
	}
	if len(documents) == 0 {
		return
	}
	results, err := uploadFlowDefinitionDocuments(
		uploadCtx, worker.flowService, documents, goSDKFlowDefinitionUploader(worker.sdkVersion), false,
	)
	for index, result := range results {
		if index >= len(documents) {
			break
		}
		worker.logFlowDefinitionUploadResult(result, documents[index])
	}
	if err == nil {
		return
	}
	if status.Code(err) == codes.Unimplemented {
		worker.logger.Warn("dex: the Dex server does not support Flow definition upload; upgrade it to store Flow definitions", "error", err)
		return
	}
	worker.logger.Warn("dex: upload Flow definitions failed; the Worker continues without them", "error", err)
}

// logFlowDefinitionUploadResult relies on the server returning results in request order.
func (worker *Worker) logFlowDefinitionUploadResult(result *dexpb.FlowDefinitionUploadResult, document FlowDefinitionDocument) {
	outcome := mapFlowDefinitionUploadOutcome(result.GetOutcome())
	keyValues := []any{"flowType", result.GetFlowType(), "outcome", outcome.String(), "storedDigest", result.GetStoredDigest()}
	switch outcome {
	case FlowDefinitionUploadCreated, FlowDefinitionUploadReplaced:
		worker.logger.Info("dex: uploaded Flow definition", keyValues...)
	case FlowDefinitionUploadUnchanged:
		worker.logger.Debug("dex: Flow definition unchanged", keyValues...)
	case FlowDefinitionUploadOutcomeUnknown:
		worker.logger.Warn(fmt.Sprintf("dex: unknown upload outcome %s", result.GetOutcome()), keyValues...)
	case FlowDefinitionUploadKeptExisting:
		worker.logger.Warn(
			"dex: the Dex server kept a different Flow definition with the same or a later generated time",
			append(keyValues,
				"storedGeneratedAt", result.GetStoredGeneratedAt().AsTime(),
				"uploadedDigest", flowDefinitionContentDigest(
					rewriteFlowDefinitionSourcePath(document.Content, currentWorkingDirectory()),
				),
				"uploadedGeneratedAt", document.GeneratedAt,
			)...,
		)
	}
}

// WaitPullRegistered waits for at least one registered STEP_AND_RPC stream and, when reserved, one RPC_ONLY stream.
//
// Changed in SDK v1.35.0: readiness counts slot kinds rather than Flow type or app Worker ID queues.
//
// Use it to gate application startup and to start replacement Workers before
// stopping old Workers during a rolling deployment. Call Start in another
// goroutine. WaitPullRegistered is safe before or after Start and from multiple
// goroutines. Once each required slot kind has registered, every later call returns nil,
// including after Stop; use PullRegistrationState for subsequent readiness probes.
// A successful wait establishes registration, not available handler capacity.
//
// Before initial readiness, it returns the first *PullRegistrationError immediately
// on a permanent rejection when that slot kind has no other registered stream, or
// Start's error if startup fails. When MaxConcurrentTasks exceeds the per-Worker limit that
// the Server reports at the first registration, it returns that limit error and never reports readiness. Surplus stream rejections are recorded for
// diagnostics and retried while another stream of that kind remains registered. Wrapped startup
// errors retain errors.Is and errors.As behavior. Stop before readiness returns an error. A nil ctx returns an argument error. Otherwise ctx
// bounds only this wait; cancellation returns ctx.Err without stopping the Worker.
// A completed registration or recorded failure takes precedence over cancellation.
//
//	func servePullWorker(ctx context.Context, worker *dex.Worker, mux *http.ServeMux) error {
//		result := make(chan error, 1)
//		go func() { result <- worker.Start() }()
//		startupCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
//		registrationErr := worker.WaitPullRegistered(startupCtx)
//		cancel()
//		if registrationErr != nil {
//			return errors.Join(registrationErr, worker.Stop(context.Background()), <-result)
//		}
//		mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
//			if !worker.PullRegistrationState().IsReady {
//				http.Error(w, "pull queues unavailable", http.StatusServiceUnavailable)
//				return
//			}
//			w.WriteHeader(http.StatusOK)
//		})
//		select {
//		case err := <-result:
//			return errors.Join(err, worker.Stop(context.Background()))
//		case <-ctx.Done():
//			return errors.Join(worker.Stop(context.Background()), <-result)
//		}
//	}
func (worker *Worker) WaitPullRegistered(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("dex: WaitPullRegistered context must not be nil")
	}
	return worker.pull.waitRegistered(ctx)
}

// PullReservedRPCDeclines returns zero. Reserved RPC slots no longer decline Step tasks.
// Changed in SDK v1.35.0: slot-based dispatch makes this counter permanently zero.
// Calls are safe concurrently with Start, Stop, and handlers.
//
// Deprecated: reservations are enforced by RPC_ONLY streams, so this counter is always zero.
func (worker *Worker) PullReservedRPCDeclines() uint64 {
	return 0
}

// PullRegistrationState returns an independent snapshot of the required slot kind registrations.
//
// Use IsReady for a readiness probe and Queues for diagnostics. It is safe to call
// concurrently before, during, or after Start and Stop. The snapshot does not flap
// for reconnects or transient errors after initial registration; only a permanent
// rejection on a required slot kind with no registered stream makes it unready. Re-registration
// clears that slot kind's error. Stop alone does not clear latched readiness, so remove
// a stopping Worker from service separately.
// See WaitPullRegistered for a complete HTTP probe and startup gating example.
func (worker *Worker) PullRegistrationState() PullRegistrationState {
	return worker.pull.registrationState()
}

// Stop stops starting tasks and drains handlers until ctx or StopDrainTimeout expires.
// Changed with Server protocol 4 (server-zd v0.1.17, required by SDK v1.35.0): tasks returned during a clean shutdown
// without running consume no Step retry.
// An attempt interrupted after it may have run consumes a retry instead; normal deployments that drain finishers avoid it.
// Idle streams immediately half-close their request side. Busy streams finish their handlers,
// send final output, then half-close. Tasks arriving after Stop begins are dropped without execution.
// Streams keep reading until the Server ends them. On timeout, active streams are reset instead
// of half-closed, so the Server knows a handler may have run.
// The deadline also resets half-closed streams that the Server has not ended yet, for example
// after a network partition, and streams whose sends are blocked by transport backpressure.
// The Server treats a task it sent to such a reset stream as possibly run, so that Step attempt
// counts even though this Worker dropped the task without running it.
// A stream whose handler already produced its final output, even one still waiting behind another
// frame, is not reset at the deadline, because a reset could discard that output. It half-closes, and Stop waits up to five more seconds for the
// Server to end it before resetting it.
//
// Stop accepts calls before Start and repeated calls after shutdown. A nil context is
// invalid. The drain deadline is the earlier of ctx and StopDrainTimeout. When it expires,
// Stop requests cancellation, waits for handlers and connection cleanup, and returns the
// context error. A handler ignoring cancellation can block Stop. PullWorkerOptions.StopDrainTimeout defaults to 30 seconds.
// Stop closes the Worker's internal FlowService connection before returning.
func (worker *Worker) Stop(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("dex: Worker Stop context must not be nil")
	}

	var cancelDrain context.CancelFunc
	ctx, cancelDrain = context.WithTimeout(ctx, worker.pull.stopDrainTimeout)
	defer cancelDrain()
	worker.lifecycleMu.Lock()
	switch worker.state {
	case workerCreated:
		worker.pull.finishRegistration(nil)
		worker.state = workerStopped
		worker.lifecycleMu.Unlock()
		worker.finish()
		return nil
	case workerRunning:
		worker.state = workerStopping
		if worker.cancelFlowDefinitionUpload != nil {
			worker.cancelFlowDefinitionUpload()
		}
		worker.pull.stop()
	case workerStopping:
	case workerStopped:
		worker.lifecycleMu.Unlock()
		return nil
	}
	done := worker.done
	worker.lifecycleMu.Unlock()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		worker.pull.expireDrain()
		<-done
		return ctx.Err()
	}
}

func (worker *Worker) finish() {
	worker.finishOnce.Do(func() {
		worker.pull.cancel()
		if err := worker.flowConn.Close(); err != nil {
			worker.logger.Error("close Worker FlowService connection", "error", err)
		}
		close(worker.done)
	})
}

func (state workerState) String() string {
	switch state {
	case workerCreated:
		return "created"
	case workerRunning:
		return "running"
	case workerStopping:
		return "stopping"
	case workerStopped:
		return "stopped"
	default:
		return "unknown"
	}
}
