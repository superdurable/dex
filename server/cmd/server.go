package cmd

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
	"github.com/superdurable/dex/server/common/log"
	"github.com/superdurable/dex/server/common/log/tag"
	"github.com/superdurable/dex/server/config"
	"github.com/superdurable/dex/server/internal/api"
	"github.com/superdurable/dex/server/internal/cluster"
	"github.com/superdurable/dex/server/internal/engine"
	"github.com/superdurable/dex/server/internal/historynotify"
	"github.com/superdurable/dex/server/internal/metrics"
	p "github.com/superdurable/dex/server/internal/persistence"
	storefactory "github.com/superdurable/dex/server/internal/persistence/factory"
	"github.com/superdurable/dex/server/internal/persistence/wrappers"
	"github.com/superdurable/dex/server/internal/routing"
	"github.com/superdurable/dex/server/internal/shardmanager"
	"github.com/superdurable/dex/server/internal/taskprocessor"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
)

// ServerApp is the fully wired dex server. It is exported so integration
// tests can create and use it without duplicating bootstrap logic.
type ServerApp struct {
	// Core components (always created)
	RunStore        p.RunStore
	BlobStore       p.BlobStore
	ShardStore      p.ShardStore
	TasklistStore   p.TasklistStore
	VisibilityStore p.VisibilityStore
	HistoryStore    p.HistoryStore
	Logger          log.Logger

	// Run-service components.
	RunEngine    engine.RunEngine
	ShardManager shardmanager.ShardManager
	ShardMapper  shardmanager.ShardMapper
	WorkerPool   *taskprocessor.WorkerPool
	RunGRPC      *grpc.Server

	// Matching-service components.
	MatchingMembership *cluster.Membership
	MatchingGRPC       *grpc.Server

	// Ops-service components.
	OpsGRPC *grpc.Server

	// localMatchingClient is this node's loopback to its own matching service
	// (run -> matching calls: dispatch, stop-notify).
	localMatchingClient pb.MatchingServiceClient
	matchingConn        *grpc.ClientConn

	// localRunServiceClient is this node's loopback to its own runs service
	// (matching -> run calls: ProcessAsyncMatch).
	localRunServiceClient pb.RunsServiceClient
	runsConn              *grpc.ClientConn

	taskHandler     *taskprocessor.DefaultTaskHandler
	matchingHandler *api.MatchingServiceHandler

	cfg config.Config
	// remoteClient dials OTHER cluster members for same-service cross-node
	// forwarding (run -> run shard owner, matching -> matching partition owner).
	remoteClient  *routing.RemoteClient
	runListener   net.Listener
	matchListener net.Listener
	opsListener   net.Listener
	metricsServer *http.Server
	metricsListen net.Listener
}

// NewServerApp creates a fully wired ServerApp. Call Start() to begin serving.
// Listeners are bound eagerly so the real addresses (important for :0) are
// available at construction time — no Set*Address hacks needed.
func NewServerApp(cfg config.Config, logger log.Logger) (*ServerApp, error) {
	ctx := context.Background()

	// Create stores via the persistence factory. The factory switches on
	// cfg.Persistence.Backend (postgres by default, mongo selectable) and
	// resolves each store's own URI / database / timeouts. Out-of-the-box
	// defaults route every store to the same server but distinct databases.
	storeSet, storeErr := storefactory.BuildStoreSet(ctx, cfg.Persistence)
	if storeErr != nil {
		return nil, fmt.Errorf("create stores: %w", storeErr)
	}

	app := &ServerApp{
		RunStore:        wrappers.NewRunStoreWithMetrics(storeSet.Run, logger),
		BlobStore:       wrappers.NewBlobStoreWithMetrics(storeSet.Blob, logger),
		ShardStore:      wrappers.NewShardStoreWithMetrics(storeSet.Shard, logger),
		TasklistStore:   storeSet.Tasklist,
		VisibilityStore: wrappers.NewVisibilityStoreWithMetrics(storeSet.Visibility, logger),
		HistoryStore:    wrappers.NewHistoryStoreWithMetrics(storeSet.History, logger),
		Logger:          logger,
		cfg:             cfg,
	}

	if err := app.initializeMetrics(ctx); err != nil {
		app.closeStores()
		return nil, fmt.Errorf("initialize metrics: %w", err)
	}

	// Bind listeners early so resolved addresses are known before wiring.
	if err := app.bindListeners(cfg); err != nil {
		app.closeResources(ctx)
		return nil, err
	}

	// The cross-node forwarding pool is shared by run-service (shard owner
	// forwarding) and matching-service (partition owner forwarding).
	app.remoteClient = routing.NewRemoteClient(logger)

	// Dial the local loopback clients BEFORE wiring so they can be passed via
	// constructor (no Set*Client setters — see no-setter-injection.mdc).
	// grpc.NewClient is non-blocking; the dial happens lazily on the first
	// RPC, by which time both gRPC servers will have started in Start().
	if err := app.dialLocalMatchingClient(cfg); err != nil {
		app.closeResources(ctx)
		return nil, err
	}
	if err := app.dialLocalRunsClient(cfg); err != nil {
		app.closeResources(ctx)
		return nil, err
	}

	if err := app.wireRunService(cfg, logger); err != nil {
		app.closeResources(ctx)
		return nil, err
	}
	if err := app.wireMatchingService(cfg, logger); err != nil {
		app.closeResources(ctx)
		return nil, err
	}
	app.wireOpsService(cfg, logger)

	return app, nil
}

// bindListeners binds TCP listeners for the run, matching, and ops gRPC
// servers. Called before wiring so the actual addresses (especially when
// using :0 for dynamic ports) are known at construction time.
func (app *ServerApp) bindListeners(cfg config.Config) error {
	if err := app.bindMetricsListener(cfg); err != nil {
		return err
	}
	runLis, err := net.Listen("tcp", cfg.GRPCListenAddress)
	if err != nil {
		return fmt.Errorf("listen run-service on %s: %w", cfg.GRPCListenAddress, err)
	}
	app.runListener = runLis

	matchLis, err := net.Listen("tcp", cfg.MatchingGRPCListenAddress)
	if err != nil {
		return fmt.Errorf("listen matching on %s: %w", cfg.MatchingGRPCListenAddress, err)
	}
	app.matchListener = matchLis

	opsLis, err := net.Listen("tcp", cfg.OpsGRPCListenAddress)
	if err != nil {
		return fmt.Errorf("listen ops on %s: %w", cfg.OpsGRPCListenAddress, err)
	}
	app.opsListener = opsLis
	return nil
}

func (app *ServerApp) initializeMetrics(ctx context.Context) error {
	return metrics.Initialize(ctx, &app.cfg.Metrics, app.Logger)
}

func (app *ServerApp) bindMetricsListener(cfg config.Config) error {
	registry := metrics.PrometheusRegistry()
	if registry == nil || cfg.Metrics.Prometheus == nil || cfg.Metrics.Prometheus.ListenAddress == "" {
		return nil
	}

	listener, err := net.Listen("tcp", cfg.Metrics.Prometheus.ListenAddress)
	if err != nil {
		return fmt.Errorf("listen metrics on %s: %w", cfg.Metrics.Prometheus.ListenAddress, err)
	}

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))

	app.metricsListen = listener
	app.metricsServer = &http.Server{Handler: mux}
	return nil
}

func (app *ServerApp) wireRunService(cfg config.Config, logger log.Logger) error {
	mapper := shardmanager.NewShardMapper(cfg.Shard)

	advertiseGRPC := resolveAdvertisedGRPCAddress(app.runListener, &cfg.Shard.Cluster)
	localTaskNotifier := taskprocessor.NewLocalTaskNotifier()
	historyNotifier := historynotify.NewNotifierManager(app.HistoryStore)
	factory := taskprocessor.NewShardTaskProcessorFactory(cfg.TaskProcessor, app.RunStore, app.HistoryStore, app.VisibilityStore, historyNotifier, localTaskNotifier, logger)

	sm := shardmanager.NewShardManager(
		cfg.Shard, app.ShardStore, logger, cfg.MemberID, factory, advertiseGRPC,
		app.remoteClient.Evict,
	)

	shardedStore := shardmanager.NewShardedRunStore(app.RunStore, sm, localTaskNotifier)

	eng := engine.NewRunEngine(&cfg.RunService, shardedStore, app.HistoryStore, app.BlobStore, mapper, sm, logger)

	taskHandler := &taskprocessor.DefaultTaskHandler{
		RunEngine:           eng,
		ShardManager:        sm,
		Logger:              logger,
		LocalMatchingClient: app.localMatchingClient,
	}
	dlqStore, dlqErr := storefactory.BuildDLQStore(context.Background(), cfg.Persistence)
	if dlqErr != nil {
		return fmt.Errorf("create DLQ store: %w", dlqErr)
	}

	wp := taskprocessor.NewWorkerPool(cfg.TaskProcessor.NumWorkers, cfg.TaskProcessor.AttemptTimeout, cfg.TaskProcessor.ImmediateTaskRetryPolicy, cfg.TaskProcessor.TimerTaskRetryPolicy, taskHandler, dlqStore, cfg.MemberID, logger)

	factory.SetWorkerPool(wp)
	sm.SetMetadataCallback(factory.GetMetadataForShard)

	grpcServer := grpc.NewServer(serverOptions(logger)...)

	runsHandler := api.NewRunsServiceHandler(eng, mapper, sm, app.remoteClient, app.localMatchingClient, historyNotifier, &cfg.RunService, logger)
	pb.RegisterRunsServiceServer(grpcServer, runsHandler)

	app.RunEngine = eng
	app.ShardManager = sm
	app.ShardMapper = mapper
	app.WorkerPool = wp
	app.RunGRPC = grpcServer
	app.taskHandler = taskHandler

	return nil
}

func (app *ServerApp) wireOpsService(cfg config.Config, logger log.Logger) {
	opsGRPC := grpc.NewServer(serverOptions(logger)...)
	// ShardMapper computes shard_id from (namespace, run_id) for
	// BlobStore.BatchGetBlobs during history-event blob hydration. It is
	// already constructed by wireRunService; guard defensively.
	if app.ShardMapper == nil {
		app.ShardMapper = shardmanager.NewShardMapper(cfg.Shard)
	}
	opsHandler := api.NewOpsServiceHandler(app.VisibilityStore, app.HistoryStore, app.BlobStore, app.ShardMapper, logger)
	pb.RegisterOpsServiceServer(opsGRPC, opsHandler)
	app.OpsGRPC = opsGRPC
}

func (app *ServerApp) wireMatchingService(cfg config.Config, logger log.Logger) error {
	matchAddr := resolveAdvertisedGRPCAddress(app.matchListener, &cfg.Tasklist.Cluster)

	// Matching needs the RemoteClient for forwarding PollForRun/DispatchRun
	// to other matching instances, and Membership for per-partition ownership
	// routing. The membership closes a departed peer's pooled conn via Evict.
	membership := cluster.NewMembership(cfg.Tasklist.Cluster, logger, cfg.MemberID, matchAddr, nil, app.remoteClient.Evict)

	matchingGRPC := grpc.NewServer(serverOptions(logger)...)

	matchingHandler := api.NewMatchingServiceHandler(api.HandlerDeps{
		Config:       cfg.MatchingService,
		Tasklist:     cfg.Tasklist,
		Logger:       logger,
		Store:        app.TasklistStore,
		Membership:   membership,
		RemoteClient: app.remoteClient,
		// RunsClient was dialed eagerly in dialLocalRunsClient (lazy gRPC dial,
		// real TCP connect happens on first RPC after the runs server starts).
		LocalRunsClient: app.localRunServiceClient,
	})
	pb.RegisterMatchingServiceServer(matchingGRPC, matchingHandler)

	app.MatchingMembership = membership
	app.MatchingGRPC = matchingGRPC
	app.matchingHandler = matchingHandler
	return nil
}

func resolveAdvertisedGRPCAddress(listener net.Listener, clusterCfg *config.ClusterConfig) string {
	if clusterCfg != nil && clusterCfg.AdvertiseGRPCAddress != "" {
		return clusterCfg.AdvertiseGRPCAddress
	}

	listenerAddr := listener.Addr().String()
	host, port, _ := net.SplitHostPort(listenerAddr)

	// When the listener is bound to a CONCRETE host (e.g. 127.0.0.1 for
	// local/test, or a specific NIC IP), advertise that host verbatim — it
	// is exactly where peers can reach us. Advertising os.Hostname() instead
	// breaks loopback binds: the hostname typically resolves to a routable
	// LAN IP where nothing is listening (the server only bound 127.0.0.1),
	// so cross-node forwarder dials get "connection refused". Only when bound
	// to the wildcard (0.0.0.0 / ::), where our reachable address is unknown,
	// do we fall back to the hostname (the production k8s path, where the pod
	// hostname is routable).
	if host != "" && host != "0.0.0.0" && host != "::" {
		return net.JoinHostPort(host, port)
	}

	hostname, _ := os.Hostname()
	return net.JoinHostPort(hostname, port)
}

// dialLocalMatchingClient dials this node's own matching service so the
// run-service side (RunsServiceHandler StopRun, taskHandler DispatchRun)
// can call matching. The target is cfg.Tasklist.Cluster.AdvertiseGRPCAddress
// — the same routable address peers use — which loops back to this node.
//
// grpc.NewClient is non-blocking; the actual TCP dial happens lazily on the
// first RPC, by which point Start() has spawned the matching server. This
// avoids Set*Client setter-injection (see no-setter-injection.mdc).
func (app *ServerApp) dialLocalMatchingClient(cfg config.Config) error {
	addr := cfg.Tasklist.Cluster.AdvertiseGRPCAddress
	if addr == "" {
		return fmt.Errorf("dialLocalMatchingClient: Tasklist.Cluster.AdvertiseGRPCAddress must be set")
	}
	conn, err := grpc.NewClient(addr, clientOptions(app.Logger)...)
	if err != nil {
		return fmt.Errorf("dial local matching service at %s: %w", addr, err)
	}
	app.matchingConn = conn
	app.localMatchingClient = pb.NewMatchingServiceClient(conn)
	return nil
}

// dialLocalRunsClient dials this node's own runs service so the matching
// handler can call ProcessAsyncMatch on async pickup. The target is
// cfg.Shard.Cluster.AdvertiseGRPCAddress, which loops back to this node.
// Same lazy-dial property as dialLocalMatchingClient.
func (app *ServerApp) dialLocalRunsClient(cfg config.Config) error {
	addr := cfg.Shard.Cluster.AdvertiseGRPCAddress
	if addr == "" {
		return fmt.Errorf("dialLocalRunsClient: Shard.Cluster.AdvertiseGRPCAddress must be set")
	}
	conn, err := grpc.NewClient(addr, clientOptions(app.Logger)...)
	if err != nil {
		return fmt.Errorf("dial local runs service at %s: %w", addr, err)
	}
	app.runsConn = conn
	app.localRunServiceClient = pb.NewRunsServiceClient(conn)
	return nil
}

// StartAsync starts all components and gRPC servers in the background.
// Returns once all listeners are bound and ready to accept connections.
// Use this in integration tests instead of go app.Start() + polling.
func (app *ServerApp) StartAsync(ctx context.Context) error {
	return app.start(ctx, false)
}

// Start begins serving. Blocks until stopped.
func (app *ServerApp) Start(ctx context.Context) error {
	return app.start(ctx, true)
}

func (app *ServerApp) start(ctx context.Context, blocking bool) error {
	if app.metricsServer != nil && app.metricsListen != nil {
		app.Logger.Info("Metrics HTTP server listening", tag.Address(app.metricsListen.Addr().String()))
		go app.metricsServer.Serve(app.metricsListen)
	}

	// Start matching service first (run-service may need to connect to it).
	// Listeners were already bound in bindListeners(); addresses are final.
	if err := app.MatchingMembership.Start(); err != nil {
		return fmt.Errorf("start matching membership: %w", err)
	}
	app.matchingHandler.Start()
	app.Logger.Info("Matching gRPC server listening", tag.Address(app.matchListener.Addr().String()))
	go app.MatchingGRPC.Serve(app.matchListener)

	// Start ops service. Read-only and lock-free with respect to the run /
	// matching path, so order doesn't matter.
	app.Logger.Info("OpsService gRPC server listening", tag.Address(app.opsListener.Addr().String()))
	go app.OpsGRPC.Serve(app.opsListener)

	// Start run-service components.
	app.Logger.Info("RunsService gRPC server listening", tag.Address(app.runListener.Addr().String()))
	if err := app.ShardManager.Start(ctx); err != nil {
		return fmt.Errorf("start shard manager: %w", err)
	}
	app.WorkerPool.Start(ctx)

	if blocking {
		return app.RunGRPC.Serve(app.runListener)
	}
	go app.RunGRPC.Serve(app.runListener)
	return nil
}

// RunGRPCAddress returns the actual run-service listener address.
func (app *ServerApp) RunGRPCAddress() string {
	if app.runListener != nil {
		return app.runListener.Addr().String()
	}
	return app.cfg.GRPCListenAddress
}

// MatchingGRPCAddress returns the actual matching-service listener address.
func (app *ServerApp) MatchingGRPCAddress() string {
	if app.matchListener != nil {
		return app.matchListener.Addr().String()
	}
	return app.cfg.MatchingGRPCListenAddress
}

// OpsGRPCAddress returns the actual ops-service listener address.
func (app *ServerApp) OpsGRPCAddress() string {
	if app.opsListener != nil {
		return app.opsListener.Addr().String()
	}
	return app.cfg.OpsGRPCListenAddress
}

// MetricsAddress returns the actual metrics listener address when enabled.
func (app *ServerApp) MetricsAddress() string {
	if app.metricsListen != nil {
		return app.metricsListen.Addr().String()
	}
	if app.cfg.Metrics.Prometheus != nil {
		return app.cfg.Metrics.Prometheus.ListenAddress
	}
	return ""
}

// GRPCAddress returns the run-service address (backward compatible).
func (app *ServerApp) GRPCAddress() string {
	return app.RunGRPCAddress()
}

// Stop gracefully shuts down all components.
func (app *ServerApp) Stop() {
	if app.metricsServer != nil {
		_ = app.metricsServer.Close()
	}
	if app.metricsListen != nil {
		_ = app.metricsListen.Close()
	}
	if app.RunGRPC != nil {
		app.RunGRPC.GracefulStop()
	}
	if app.MatchingGRPC != nil {
		app.MatchingGRPC.GracefulStop()
	}
	if app.OpsGRPC != nil {
		app.OpsGRPC.GracefulStop()
	}
	if app.ShardManager != nil {
		app.ShardManager.Stop()
	}
	if app.matchingHandler != nil {
		app.matchingHandler.Stop()
	}
	if app.MatchingMembership != nil {
		app.MatchingMembership.Stop()
	}
	if app.WorkerPool != nil {
		app.WorkerPool.Stop()
	}
	if app.matchingConn != nil {
		app.matchingConn.Close()
	}
	if app.runsConn != nil {
		app.runsConn.Close()
	}
	if app.remoteClient != nil {
		app.remoteClient.Close()
	}
	app.closeResources(context.Background())
}

func (app *ServerApp) closeStores() {
	app.RunStore.Close()
	app.BlobStore.Close()
	app.ShardStore.Close()
	if app.TasklistStore != nil {
		app.TasklistStore.Close()
	}
	if app.VisibilityStore != nil {
		app.VisibilityStore.Close()
	}
	if app.HistoryStore != nil {
		app.HistoryStore.Close()
	}
}

func (app *ServerApp) closeResources(ctx context.Context) {
	app.closeStores()
	_ = metrics.Close(ctx)
}

func serverOptions(logger log.Logger) []grpc.ServerOption {
	return []grpc.ServerOption{
		// Permit the ClientPool's keepalive (Time 10s, even on idle conns)
		// without sending GoAway "too_many_pings". MinTime must be <= the
		// client's keepalive Time, else the server tears down healthy conns.
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             5 * time.Second,
			PermitWithoutStream: true,
		}),
		// Server-initiated keepalive so a vanished client conn is reaped
		// rather than lingering and pinning resources.
		grpc.KeepaliveParams(keepalive.ServerParameters{
			Time:    30 * time.Second,
			Timeout: 10 * time.Second,
		}),
		grpc.ChainUnaryInterceptor(
			metrics.UnaryServerMetricsReportingInterceptor(),
			metrics.UnaryServerErrorLoggingInterceptor(logger),
		),
		grpc.ChainStreamInterceptor(
			metrics.StreamServerMetricsReportingInterceptor(),
			metrics.StreamServerErrorLoggingInterceptor(logger),
		),
	}
}

func clientOptions(logger log.Logger) []grpc.DialOption {
	return []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithChainUnaryInterceptor(
			metrics.UnaryClientMetricsReportingInterceptor(),
			metrics.UnaryClientErrorLoggingInterceptor(logger),
		),
		grpc.WithChainStreamInterceptor(
			metrics.StreamClientMetricsReportingInterceptor(),
			metrics.StreamClientErrorLoggingInterceptor(logger),
		),
	}
}
