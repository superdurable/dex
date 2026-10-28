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
	"google.golang.org/grpc/keepalive"
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
	// FlowServiceAddress is the Dex gRPC endpoint used for startup synchronization and blob hydration. Default: "localhost:8801".
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
}

// PullWorkerOptions configures outbound polling of Dex task queues.
//
// Use it with WorkerOptions.Pull to tune outbound task streams.
// MaxConcurrentTasks defaults to runtime.NumCPU; IdleStreamsPerQueue defaults to one.
// StopDrainTimeout defaults to 30 seconds.
// Each stream runs one task at a time. Negative values and an invalid AppWorkerID
// cause NewWorker to return an error.
//
//	worker, err := dex.NewWorker(registry, cache, dex.WorkerOptions{
//		Pull: &dex.PullWorkerOptions{MaxConcurrentTasks: 8},
//	})
//	if err != nil { return err }
//	defer worker.Stop(context.Background())
type PullWorkerOptions struct {
	// StopOnPermanentStreamRejection makes Start return a permanent rejection when its queue has no registered stream. Default: false retries indefinitely.
	// A rejection while another stream holds the same queue is recorded and retried without stopping.
	// This applies before and after initial registration. Start begins draining all queues and bounds
	// the drain with StopDrainTimeout, then cancels streams and waits for handlers to return.
	// Handlers must respond to cancellation. Other queues may still hold streams while draining.
	// Always call Stop before exiting; it remains safe after Start returns.
	StopOnPermanentStreamRejection bool
	// MaxConcurrentTasks bounds simultaneous handlers. Zero uses runtime.NumCPU().
	MaxConcurrentTasks int
	// ReservedRPCTasks protects synchronous RPC latency from long-running Steps.
	// Nil reserves one slot when resolved MaxConcurrentTasks is at least two, otherwise zero.
	// With the CPU-count default, Steps therefore get one fewer slot than before. Set ptr.Any(0) to opt out.
	// With one slot and nil reserve, Start warns once that RPCs may wait behind Steps.
	// Flows must stay responsive because RPCs share Worker task capacity with Steps.
	// WaitFor and Execute tasks, including local attempts, are declined when total busy handlers reach
	// MaxConcurrentTasks minus the reserve. API-direct and transactional RPC tasks may use the full limit.
	// NewWorker returns an error unless 0 <= reserve < resolved MaxConcurrentTasks.
	// Declines reassign without consuming an attempt. If all Workers stay at their Step limit until the
	// dispatch wait expires, NO_WORKER_AVAILABLE consumes an attempt and follows the Step retry policy.
	// Local attempts wait the local dispatch wait, then fall back according to their policy.
	// Declines emit a Flow-type debug log, increment PullReservedRPCDeclines, and preserve readiness.
	// A queue keeps one extra idle stream for 30 seconds after its last reserve decline.
	// The Worker does not open that stream while all task slots are busy.
	// For example: PullWorkerOptions{MaxConcurrentTasks: 8, ReservedRPCTasks: ptr.Any(2)}.
	ReservedRPCTasks *int
	// IdleStreamsPerQueue sets waiting streams for each Flow type and the sticky queue. Zero uses one.
	// A queue keeps one more after a recent reserve decline. Use at least two for heavy Step backlogs.
	IdleStreamsPerQueue int
	// AppWorkerID overrides the generated process identity. Empty generates identity plus eight base32 characters.
	// Set a new value after each process restart. It must not equal a registered Flow type.
	// Valid values contain 1-63 ASCII letters, digits, dots, underscores, or hyphens.
	AppWorkerID string
	// StopDrainTimeout bounds Stop after it begins draining. Zero uses 30 seconds; negative values are invalid.
	StopDrainTimeout time.Duration
}

// PullRegistrationError identifies a permanent rejection that leaves a pull queue without a registered stream.
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
	// Queue names the rejected Flow type queue or app Worker ID queue.
	Queue string
	// Err is the non-nil gRPC status error returned by the rejected stream.
	Err error
}

// Error returns the queue name and underlying rejection for diagnostics.
// It does not change the error; concurrent calls are safe on immutable values.
func (rejection *PullRegistrationError) Error() string {
	return fmt.Sprintf("dex: pull queue %q registration rejected: %v", rejection.Queue, rejection.Err)
}

// Unwrap returns Err so errors.Is, errors.As, and gRPC status inspection retain the rejection.
// SDK-produced errors return a non-nil error. See PullRegistrationError for usage.
func (rejection *PullRegistrationError) Unwrap() error { return rejection.Err }

// PullRegistrationState is an independent snapshot for a pull Worker's readiness probe.
//
// Obtain it with Worker.PullRegistrationState, including concurrently with Start,
// Stop, or WaitPullRegistered. Its zero value has IsReady false and Queues nil.
// Modifying Queues does not reconfigure the Worker.
// Treat errors inside entries as immutable. See WaitPullRegistered for a complete
// startup and HTTP readiness probe example.
type PullRegistrationState struct {
	// IsReady becomes true after every queue has registered at least once.
	// Afterwards it is false only if a queue has no registered stream and a permanent error.
	// Transient failures and ownership moves preserve readiness. Successful registration clears that queue's error.
	IsReady bool
	// Queues contains the app Worker ID queue followed by Flow type queues in sorted order.
	// The Worker returns a fresh non-nil slice.
	Queues []PullQueueRegistrationState
}

// PullQueueRegistrationState describes one queue in a PullRegistrationState snapshot.
//
// Inspect these entries to diagnose a failing readiness probe. A queue may have
// both a registered stream and LastPermanentError when another stream was rejected.
// The snapshot owns its values; errors are shared immutable errors. See
// WaitPullRegistered for the startup and readiness probe example.
type PullQueueRegistrationState struct {
	// Queue is the Flow type name or app Worker ID identifying this queue.
	Queue string
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
// registry or cache is nil. It validates the FlowService address, FlowServiceTLS,
// synchronization timeout, GrpcMaxMessageBytes, and pull options, then creates an
// outbound FlowService connection without opening task streams. The returned
// Worker owns that connection; Start or Stop closes it.
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
	flowServiceAddress, syncTimeout, err := resolveWorkerOptions(options)
	if err != nil {
		return nil, err
	}
	messageLimits, err := resolveGrpcMessageLimits(options.GrpcMaxMessageBytes)
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

	logger := resolveLogger(options.Logger, cache.Logger())
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
// Start may be called exactly once. Before serving, it negotiates a Server
// protocol, synchronizes Attribute indexes, and waits up to
// WorkerOptions.AttributeIndexSyncTimeout for each startup request.
// It returns nil after a normal Stop, or an error for synchronization, polling, or invalid lifecycle state failures. Call Start on a dedicated goroutine
// when the application must continue doing other work.
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
	worker.state = workerRunning
	worker.lifecycleMu.Unlock()
	startErr = worker.pull.run()
	worker.lifecycleMu.Lock()
	worker.state = workerStopped
	worker.lifecycleMu.Unlock()
	worker.finish()
	return startErr
}

// WaitPullRegistered waits until every pull queue has received a Registered acknowledgement at least once.
//
// Use it to gate application startup and to start replacement Workers before
// stopping old Workers during a rolling deployment. Call Start in another
// goroutine. WaitPullRegistered is safe before or after Start and from multiple
// goroutines. Once all queues have registered, every later call returns nil,
// including after Stop; use PullRegistrationState for subsequent readiness probes.
// A successful wait establishes registration, not available handler capacity.
//
// Before initial readiness, it returns the first *PullRegistrationError immediately
// on a permanent rejection when that queue has no other registered stream, or
// Start's error if startup fails. Surplus stream rejections are recorded for
// diagnostics and retried while another stream holds the queue. Wrapped startup
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

// PullReservedRPCDeclines returns the cumulative number of Step tasks declined for reserved RPC capacity.
// It counts decline events; Dex may decline the same Step dispatch repeatedly while retrying it.
// Read its rate to assess reserve pressure. It excludes full-capacity rejections and unknown names.
// It never resets during this Worker's lifetime.
// Calls are safe alongside Start, Stop, and handlers.
// Export the value to a monitoring counter to distinguish reserve pressure from full capacity:
//
//	declines := worker.PullReservedRPCDeclines()
//	log.Printf("reserved RPC capacity declines: %d", declines)
func (worker *Worker) PullReservedRPCDeclines() uint64 {
	worker.pull.mu.Lock()
	defer worker.pull.mu.Unlock()
	return worker.pull.reservedRPCDeclines
}

// PullRegistrationState returns an independent snapshot of all pull queue registrations.
//
// Use IsReady for a readiness probe and Queues for diagnostics. It is safe to call
// concurrently before, during, or after Start and Stop. The snapshot does not flap
// for reconnects or transient errors after initial registration; only a permanent
// rejection on a queue with no registered stream makes it unready. Re-registration
// clears that queue's error. Stop alone does not clear latched readiness, so remove
// a stopping Worker from service separately.
// See WaitPullRegistered for a complete HTTP probe and startup gating example.
func (worker *Worker) PullRegistrationState() PullRegistrationState {
	return worker.pull.registrationState()
}

// Stop stops accepting tasks and drains handlers until ctx or StopDrainTimeout expires.
//
// Stop accepts calls before Start and repeated calls after shutdown. A nil context is
// invalid. When the drain deadline expires, Stop requests cancellation, waits for
// handlers and connection cleanup, and returns the context error. A handler ignoring
// cancellation can block Stop. PullWorkerOptions.StopDrainTimeout defaults to 30 seconds.
// Stop closes the Worker's internal FlowService connection before returning.
func (worker *Worker) Stop(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("dex: Worker Stop context must not be nil")
	}

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
		worker.pull.stop()
	case workerStopping:
	case workerStopped:
		worker.lifecycleMu.Unlock()
		return nil
	}
	done := worker.done
	worker.lifecycleMu.Unlock()
	var cancelDrain context.CancelFunc
	ctx, cancelDrain = context.WithTimeout(ctx, worker.pull.stopDrainTimeout)
	defer cancelDrain()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		worker.pull.cancel()
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
