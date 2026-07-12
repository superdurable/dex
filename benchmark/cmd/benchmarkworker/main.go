package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/superdurable/dex/benchmark/internal/log"
	"github.com/superdurable/dex/benchmark/internal/log/tag"
	"github.com/superdurable/dex/sdk-go/dex"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type benchmarkWorkerApp struct {
	cfg       benchmarkConfig
	logger    log.Logger
	registry  *dex.Registry
	client    *dex.Client
	worker    *dex.Worker
	server    *http.Server
	runConn   *grpc.ClientConn
	matchConn *grpc.ClientConn
}

type sdkWorkerLoggerAdapter struct {
	logger log.Logger
}

type triggerResponse struct {
	Mode         string   `json:"mode"`
	Runs         int      `json:"runs"`
	NumSteps     int      `json:"num_steps"`
	StateSize    int      `json:"state_size"`
	Namespace    string   `json:"namespace"`
	TaskListName string   `json:"task_list_name"`
	RunIDs       []string `json:"run_ids"`
	StartedAt    string   `json:"started_at"`
}

func main() {
	logger := log.NewDefaultLogger()

	cfg, err := loadBenchmarkConfig()
	if err != nil {
		logger.Error("Failed to load benchmark config: " + err.Error())
		os.Exit(1)
	}
	if cfg.RunServiceAddress == "" || cfg.MatchingServiceAddress == "" {
		logger.Error("BENCHMARK_RUN_SERVICE_ADDRESS and BENCHMARK_MATCHING_SERVICE_ADDRESS are required")
		os.Exit(1)
	}

	app, err := newBenchmarkWorkerApp(cfg, logger)
	if err != nil {
		logger.Error("Failed to create benchmark worker: " + err.Error())
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		logger.Info("Received shutdown signal")
		app.stop(context.Background())
		cancel()
	}()

	if err := app.start(ctx); err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("Benchmark worker exited with error: " + err.Error())
		os.Exit(1)
	}
}

func newBenchmarkWorkerApp(cfg benchmarkConfig, logger log.Logger) (*benchmarkWorkerApp, error) {
	runConn, err := grpc.NewClient(cfg.RunServiceAddress, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("connect run service: %w", err)
	}

	matchConn, err := grpc.NewClient(cfg.MatchingServiceAddress, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		runConn.Close()
		return nil, fmt.Errorf("connect matching service: %w", err)
	}

	registry := dex.NewRegistry()
	registry.Register(&sequentialBenchmarkFlow{})
	registry.Register(&parallelBenchmarkFlow{})
	// Wait/channel/timer benchmark flows. See wait_flows.go.
	registry.Register(&channelMinMaxFlow{})
	registry.Register(&allOfTimerChannelFlow{})
	registry.Register(&anyOfTimerOnlyFlow{})
	registry.Register(&anyOfRaceFlow{})
	// Dynamic channel benchmark flow (mode=dynamicChannel). See wait_flows.go
	// — fans out per orderID to two sibling waiters (OrderUpdates +
	// OrderAcks) so the WebUI demonstrates per-key isolation across two
	// dynamic channel families plus the internal-publish round-trip.
	registry.Register(&dynamicChannelFlow{})
	registry.Register(&retryBenchmarkFlow{})
	registry.Register(&sagaWaitForBenchmarkFlow{})
	registry.Register(&sagaExecuteBenchmarkFlow{})
	// Multi-agent benchmark flows (driven by /agentTrigger,
	// /agentHumanMessage, /agentInterruptLLM). See agent_flows.go for
	// the full design + cross-run StartRun / sibling-cancel demo.
	registry.Register(&mainAgentFlow{})
	registry.Register(&subAgentFlow{})

	// One worker per benchmark process. Throughput is scaled out by
	// adding more replicas of this benchmark process and/or by tuning
	// the server-side `tasklist.numWritePartitions` /
	// `numReadPartitions` so the tasklist fan-in is not the bottleneck
	// — a single tasklist with N partitions replaces what used to need
	// N independent tasklists.
	worker := dex.NewWorker(registry, matchConn, runConn, cfg.Namespace, dex.WorkerOptions{
		TaskListName:   cfg.TaskListName,
		RunConcurrency: cfg.WorkerRunConcurrency,
		Logger:         sdkWorkerLoggerAdapter{logger: logger},
	})

	app := &benchmarkWorkerApp{
		cfg:       cfg,
		logger:    logger,
		registry:  registry,
		client:    dex.NewClient(registry, runConn, cfg.Namespace),
		worker:    worker,
		runConn:   runConn,
		matchConn: matchConn,
	}

	// Wire the cross-run client + logger + tasklist name into
	// agent_flows.go so step Execute methods can launch sub-flows and
	// emit structured logs. Steps can't easily receive runtime deps
	// via their struct (the registry instantiates one shared instance
	// per stepID), so the multi-agent flow uses package-level setters.
	// The tasklist name must match the tasklist this benchmark process
	// is polling so child subAgent runs land here.
	setAgentClient(app.client)
	setAgentLogger(logger)
	setAgentTaskListName(cfg.TaskListName)

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", app.handleHealth)
	mux.HandleFunc("/trigger", app.handleTrigger)
	mux.HandleFunc("/publish", app.handlePublish)
	mux.HandleFunc("/agentTrigger", app.handleAgentTrigger)
	mux.HandleFunc("/agentHumanMessage", app.handleAgentHumanMessage)
	mux.HandleFunc("/agentInterruptLLM", app.handleAgentInterruptLLM)
	app.server = &http.Server{
		Addr:    cfg.HTTPListenAddress,
		Handler: mux,
	}
	return app, nil
}

func (app *benchmarkWorkerApp) start(ctx context.Context) error {
	workerErrCh := make(chan error, 1)
	app.logger.Info("Starting worker", tag.TaskListName(app.cfg.TaskListName))
	go func() {
		if err := app.worker.Start(); err != nil && !errors.Is(err, context.Canceled) {
			app.logger.Error("Worker exited with error",
				tag.TaskListName(app.cfg.TaskListName), tag.Error(err))
			workerErrCh <- err
		}
	}()

	serverErrCh := make(chan error, 1)
	go func() {
		app.logger.Info("Benchmark HTTP server listening on " + app.cfg.HTTPListenAddress)
		serverErrCh <- app.server.ListenAndServe()
	}()

	select {
	case err := <-workerErrCh:
		_ = app.server.Shutdown(context.Background())
		return err
	case err := <-serverErrCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			app.worker.Stop()
			return err
		}
		return nil
	case <-ctx.Done():
		app.stop(context.Background())
		return ctx.Err()
	}
}

func (app *benchmarkWorkerApp) stop(ctx context.Context) {
	app.worker.Stop()
	_ = app.server.Shutdown(ctx)
	if app.runConn != nil {
		_ = app.runConn.Close()
	}
	if app.matchConn != nil {
		_ = app.matchConn.Close()
	}
}

func (app *benchmarkWorkerApp) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handlePublish forwards an HTTP query into RawClient.PublishToChannel so
// dev-stack.sh can deliver messages to in-flight runs without spinning up
// a separate Go client. Required params: runId, channel. Optional: value
// (string; wrapped as map{value:...} so it round-trips through JSON
// EncodedObjects to typed step inputs cleanly).
func (app *benchmarkWorkerApp) handlePublish(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if app.cfg.TriggerToken != "" && r.Header.Get("X-Benchmark-Token") != app.cfg.TriggerToken {
		writeError(w, http.StatusUnauthorized, "missing or invalid benchmark token")
		return
	}
	runID := r.URL.Query().Get("runId")
	if runID == "" {
		writeError(w, http.StatusBadRequest, "runId is required")
		return
	}
	channel := r.URL.Query().Get("channel")
	if channel == "" {
		channel = "notify"
	}
	value := r.URL.Query().Get("value")
	if value == "" {
		value = "ping"
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	payload := map[string]any{"value": value, "ts": time.Now().UnixMilli()}
	// PublishToChannelByName because the channel name comes from an HTTP
	// query parameter — we don't have a Channel[T] value at compile time
	// for an arbitrary user-supplied name.
	if err := app.client.PublishToChannelByName(ctx, runID, channel, payload); err != nil {
		app.logger.Warn("PublishToChannel failed",
			tag.RunID(runID), tag.Error(err))
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	app.logger.Info("Channel publish accepted",
		tag.RunID(runID), tag.ChannelName(channel))
	writeJSON(w, http.StatusOK, map[string]any{
		"runId":   runID,
		"channel": channel,
		"value":   value,
	})
}

func (app *benchmarkWorkerApp) handleTrigger(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if app.cfg.TriggerToken != "" && r.Header.Get("X-Benchmark-Token") != app.cfg.TriggerToken {
		writeError(w, http.StatusUnauthorized, "missing or invalid benchmark token")
		return
	}

	runs, err := parsePositiveQueryInt(r, "runs", 1)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	numSteps, err := parsePositiveQueryInt(r, "numSteps", 1)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	stateSize, err := parsePositiveQueryInt(r, "stateSize", 0)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	mode := r.URL.Query().Get("mode")
	if mode == "" {
		mode = "sequential"
	}
	if !validMode(mode) {
		writeError(w, http.StatusBadRequest, "mode must be one of: sequential, parallel, channelMinMax, allOfTimerChannel, anyOfTimerOnly, anyOfTimerVsChannel, dynamicChannel, retry, saga")
		return
	}

	// orderIds is consumed only by mode=dynamicChannel. Format:
	// comma-separated string (e.g. "ord-1,ord-2,ord-3"). Empty defaults
	// to a 3-order set so a bare /trigger?mode=dynamicChannel still produces
	// an interesting WebUI graph.
	orderIDs := parseOrderIDs(r.URL.Query().Get("orderIds"))
	retryFinalOutcome := parseRetryFinalOutcome(r.URL.Query().Get("finalOutcome"))
	sagaMethodKind := parseSagaMethodKind(r.URL.Query().Get("methodKind"))

	startConcurrency, err := parsePositiveQueryInt(r, "startConcurrency", 10)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	startedAt := time.Now().UTC()
	// Use nanosecond precision and include the mode in the run ID so
	// successive /trigger calls of different modes within the same
	// second do NOT collide on the same (runID) and lose all but one
	// flow type to StartRun's "different flow_type for existing run"
	// rejection. Pre-rewrite this used RFC3339 (1s precision) + mode-less
	// suffixes which made bursty dev-stack runs lose 3 of 4 wait flows.
	startedAtISO := startedAt.Format("2006-01-02T15:04:05.000000000Z")
	runIDs := make([]string, runs)
	for i := 0; i < runs; i++ {
		runIDs[i] = fmt.Sprintf("%s-%s-%d", startedAtISO, mode, i+1)
	}

	app.logger.Info("Benchmark trigger accepted",
		tag.Mode(mode),
		tag.Count(runs),
		tag.NumSteps(numSteps),
		tag.StateSize(stateSize),
		tag.Namespace(app.cfg.Namespace),
		tag.TaskListName(app.cfg.TaskListName),
	)

	// Start flows in background goroutine to avoid HTTP timeout issues.
	// Each flow is retried indefinitely on failure with exponential backoff.
	go app.startFlowsInBackground(mode, numSteps, stateSize, startConcurrency, runIDs, orderIDs, retryFinalOutcome, sagaMethodKind)

	resp := triggerResponse{
		Mode:         mode,
		Runs:         runs,
		NumSteps:     numSteps,
		StateSize:    stateSize,
		Namespace:    app.cfg.Namespace,
		TaskListName: app.cfg.TaskListName,
		RunIDs:       runIDs,
		StartedAt:    startedAtISO,
	}
	writeJSON(w, http.StatusOK, resp)
}

// =============================================================================
// Multi-agent benchmark HTTP handlers
//
// All three handlers are GET so they can be invoked directly from a
// browser address bar (matches the existing /trigger and /publish
// ergonomics). See benchmark/AGENT_WORKFLOW.md for the end-to-end
// scenario these endpoints drive together.
// =============================================================================

type agentTriggerResponse struct {
	RunID                  string `json:"run_id"`
	MaxConcurrentSubAgents int    `json:"max_concurrent_subagents"`
	TaskListName           string `json:"task_list_name"`
	Namespace              string `json:"namespace"`
	StartedAt              string `json:"started_at"`
}

type agentHumanMessageResponse struct {
	RunID string `json:"run_id"`
	Kind  string `json:"kind"`
	Num   int    `json:"num,omitempty"`
}

type agentInterruptResponse struct {
	RunID  string `json:"run_id"`
	Reason string `json:"reason,omitempty"`
}

// handleAgentTrigger starts a single mainAgentFlow run.
//
//	GET /agentTrigger?maxConcurrentSubAgents=2
//
// The response carries the synthesized runID so subsequent
// /agentHumanMessage and /agentInterruptLLM calls can target it.
func (app *benchmarkWorkerApp) handleAgentTrigger(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if app.cfg.TriggerToken != "" && r.Header.Get("X-Benchmark-Token") != app.cfg.TriggerToken {
		writeError(w, http.StatusUnauthorized, "missing or invalid benchmark token")
		return
	}
	maxConcurrent, err := parsePositiveQueryInt(r, "maxConcurrentSubAgents", 2)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	startedAt := time.Now().UTC()
	startedAtISO := startedAt.Format("2006-01-02T15:04:05.000000000Z")
	runID := fmt.Sprintf("%s-mainAgent-1", startedAtISO)
	taskListName := app.cfg.TaskListName

	app.logger.Info("agent benchmark trigger accepted",
		tag.RunID(runID),
		tag.MaxConcurrentSubAgents(maxConcurrent),
		tag.TaskListName(taskListName),
	)

	ctx, cancel := context.WithTimeout(r.Context(), startFlowPerCallTimeout)
	defer cancel()
	startErr := app.client.StartRunWithOptions(
		ctx, runID, &mainAgentFlow{},
		&dex.RunOptions{TaskListName: taskListName},
		mainAgentInitInput{MaxConcurrentSubAgents: maxConcurrent},
	)
	if startErr != nil {
		app.logger.Warn("agent trigger StartRun failed",
			tag.RunID(runID), tag.Error(startErr))
		writeError(w, http.StatusBadGateway, startErr.Error())
		return
	}

	writeJSON(w, http.StatusOK, agentTriggerResponse{
		RunID:                  runID,
		MaxConcurrentSubAgents: maxConcurrent,
		TaskListName:           taskListName,
		Namespace:              app.cfg.Namespace,
		StartedAt:              startedAtISO,
	})
}

// handleAgentHumanMessage publishes a typed mainAgentMessage to the
// MainAgentMessageCh channel of the given run.
//
//	GET /agentHumanMessage?runId=...&kind=start_subagents&num=3
//	GET /agentHumanMessage?runId=...&kind=start_llm_loop
//	GET /agentHumanMessage?runId=...&kind=complete
//
// `num` is read only when `kind=start_subagents` (default 1, must be > 0).
// Other kinds ignore `num`.
func (app *benchmarkWorkerApp) handleAgentHumanMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if app.cfg.TriggerToken != "" && r.Header.Get("X-Benchmark-Token") != app.cfg.TriggerToken {
		writeError(w, http.StatusUnauthorized, "missing or invalid benchmark token")
		return
	}
	runID := r.URL.Query().Get("runId")
	if runID == "" {
		writeError(w, http.StatusBadRequest, "runId is required")
		return
	}
	kindRaw := r.URL.Query().Get("kind")
	if kindRaw == "" {
		writeError(w, http.StatusBadRequest, "kind is required (one of: start_subagents, start_llm_loop, complete)")
		return
	}

	msg, num, err := buildHumanMessage(kindRaw, r.URL.Query().Get("num"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if pubErr := app.client.PublishToChannel(ctx, runID, MainAgentMessageCh.Name, msg); pubErr != nil {
		app.logger.Warn("agentHumanMessage publish failed",
			tag.RunID(runID), tag.Error(pubErr))
		writeError(w, http.StatusBadGateway, pubErr.Error())
		return
	}
	app.logger.Info("agent human message published",
		tag.RunID(runID),
		tag.ChannelName(MainAgentMessageCh.Name),
	)
	writeJSON(w, http.StatusOK, agentHumanMessageResponse{
		RunID: runID,
		Kind:  kindRaw,
		Num:   num,
	})
}

// buildHumanMessage parses a human-message kind string + optional num
// param into a typed mainAgentMessage. Pulled into a helper for
// unit-testability and because the kind allowlist is the most common
// failure mode the user will hit.
func buildHumanMessage(kindRaw, numRaw string) (mainAgentMessage, int, error) {
	switch kindRaw {
	case "start_subagents":
		num := 1
		if numRaw != "" {
			n, err := strconv.Atoi(numRaw)
			if err != nil || n <= 0 {
				return mainAgentMessage{}, 0, fmt.Errorf("num must be a positive integer for kind=start_subagents")
			}
			num = n
		}
		return mainAgentMessage{Kind: msgKindHumanStartSubAgents, NumSubAgents: num}, num, nil
	case "start_llm_loop":
		return mainAgentMessage{Kind: msgKindHumanStartLLMLoop}, 0, nil
	case "complete":
		return mainAgentMessage{Kind: msgKindHumanComplete}, 0, nil
	default:
		return mainAgentMessage{}, 0, fmt.Errorf("kind must be one of: start_subagents, start_llm_loop, complete")
	}
}

// handleAgentInterruptLLM publishes a single interruptSignal to
// InterruptLLMCh of the given run.
//
//	GET /agentInterruptLLM?runId=...&reason=...
func (app *benchmarkWorkerApp) handleAgentInterruptLLM(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if app.cfg.TriggerToken != "" && r.Header.Get("X-Benchmark-Token") != app.cfg.TriggerToken {
		writeError(w, http.StatusUnauthorized, "missing or invalid benchmark token")
		return
	}
	runID := r.URL.Query().Get("runId")
	if runID == "" {
		writeError(w, http.StatusBadRequest, "runId is required")
		return
	}
	reason := r.URL.Query().Get("reason")

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if pubErr := app.client.PublishToChannel(ctx, runID, InterruptLLMCh.Name, interruptSignal{Reason: reason}); pubErr != nil {
		app.logger.Warn("agentInterruptLLM publish failed",
			tag.RunID(runID), tag.Error(pubErr))
		writeError(w, http.StatusBadGateway, pubErr.Error())
		return
	}
	app.logger.Info("agent interrupt LLM published",
		tag.RunID(runID),
		tag.ChannelName(InterruptLLMCh.Name),
	)
	writeJSON(w, http.StatusOK, agentInterruptResponse{
		RunID:  runID,
		Reason: reason,
	})
}

const (
	startFlowInitialBackoff = 1 * time.Second
	startFlowMaxBackoff     = 30 * time.Second
	startFlowPerCallTimeout = 30 * time.Second
)

func (app *benchmarkWorkerApp) startFlowsInBackground(
	mode string, numSteps, stateSize, startConcurrency int, runIDs []string, orderIDs []string, retryFinalOutcome string, sagaMethodKind string,
) {
	sem := make(chan struct{}, startConcurrency)
	var wg sync.WaitGroup
	taskListName := app.cfg.TaskListName
	for _, runID := range runIDs {
		wg.Add(1)
		sem <- struct{}{}
		go func(runID string) {
			defer wg.Done()
			defer func() { <-sem }()
			app.startFlowWithRetry(mode, numSteps, stateSize, runID, taskListName, orderIDs, retryFinalOutcome, sagaMethodKind)
		}(runID)
	}
	wg.Wait()

	app.logger.Info("All flows started",
		tag.Mode(mode),
		tag.Count(len(runIDs)),
	)
}

func (app *benchmarkWorkerApp) startFlowWithRetry(
	mode string, numSteps, stateSize int, runID, taskListName string, orderIDs []string, retryFinalOutcome string, sagaMethodKind string,
) {
	input := benchmarkTriggerInput{
		NumSteps:  numSteps,
		StateSize: stateSize,
	}

	backoff := startFlowInitialBackoff
	for attempt := 1; ; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), startFlowPerCallTimeout)
		flowOpts := &dex.RunOptions{TaskListName: taskListName}
		var err error
		switch mode {
		case "sequential":
			err = app.client.StartRunWithOptions(ctx, runID, &sequentialBenchmarkFlow{}, flowOpts, input)
		case "parallel":
			err = app.client.StartRunWithOptions(ctx, runID, &parallelBenchmarkFlow{}, flowOpts, input)
		case "channelMinMax":
			err = app.client.StartRunWithOptions(ctx, runID, &channelMinMaxFlow{}, flowOpts, waitInput{Token: runID})
		case "allOfTimerChannel":
			err = app.client.StartRunWithOptions(ctx, runID, &allOfTimerChannelFlow{}, flowOpts, waitInput{Token: runID})
		case "anyOfTimerOnly":
			err = app.client.StartRunWithOptions(ctx, runID, &anyOfTimerOnlyFlow{}, flowOpts, waitInput{Token: runID})
		case "anyOfTimerVsChannel":
			err = app.client.StartRunWithOptions(ctx, runID, &anyOfRaceFlow{}, flowOpts, waitInput{Token: runID})
		case "dynamicChannel":
			err = app.client.StartRunWithOptions(ctx, runID, &dynamicChannelFlow{},
				flowOpts, dynamicTriggerInput{OrderIDs: orderIDs})
		case "retry":
			retryFlowCounters.Delete(runID)
			err = app.client.StartRunWithOptions(ctx, runID, &retryBenchmarkFlow{}, flowOpts,
				retryTriggerInput{FinalOutcome: retryFinalOutcome})
		case "saga":
			input := sagaTriggerInput{Token: runID, MethodKind: sagaMethodKind}
			if sagaMethodKind == "waitFor" {
				err = app.client.StartRunWithOptions(ctx, runID, &sagaWaitForBenchmarkFlow{}, flowOpts, input)
			} else {
				err = app.client.StartRunWithOptions(ctx, runID, &sagaExecuteBenchmarkFlow{}, flowOpts, input)
			}
		}
		cancel()

		if err == nil {
			app.logger.Info("Flow started",
				tag.RunID(runID),
				tag.TaskListName(taskListName),
				tag.Mode(mode),
				tag.Attempt(attempt),
			)
			return
		}

		app.logger.Warn("Flow start failed, retrying",
			tag.RunID(runID),
			tag.TaskListName(taskListName),
			tag.Mode(mode),
			tag.Attempt(attempt),
			tag.Error(err),
		)

		time.Sleep(backoff)
		backoff *= 2
		if backoff > startFlowMaxBackoff {
			backoff = startFlowMaxBackoff
		}
	}
}


// validMode returns true if mode names a registered flow.
func validMode(mode string) bool {
	switch mode {
	case "sequential", "parallel",
		"channelMinMax", "allOfTimerChannel", "anyOfTimerOnly", "anyOfTimerVsChannel",
		"dynamicChannel", "retry", "saga":
		return true
	}
	return false
}

// parseOrderIDs splits a comma-separated query value into a list of
// trimmed, non-empty IDs. Returns the default 3-order set when the input
// is blank so dev-stack's bare /trigger?mode=dynamicChannel still produces
// an interesting WebUI graph (and per-order /publish below stays in sync).
func parseOrderIDs(raw string) []string {
	if raw == "" {
		return []string{"ord-1", "ord-2", "ord-3"}
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if id := strings.TrimSpace(p); id != "" {
			out = append(out, id)
		}
	}
	if len(out) == 0 {
		// "," or "  ,  ,  " — treat as empty to land on the default.
		return []string{"ord-1", "ord-2", "ord-3"}
	}
	return out
}

func parsePositiveQueryInt(r *http.Request, key string, fallback int) (int, error) {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return fallback, nil
	}
	val, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer", key)
	}
	if val < 0 {
		return 0, fmt.Errorf("%s must be >= 0", key)
	}
	if val == 0 && fallback > 0 {
		return 0, fmt.Errorf("%s must be > 0", key)
	}
	return val, nil
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func (a sdkWorkerLoggerAdapter) Debug(msg string, keyvals ...any) {
	a.logger.Debugf("%s %v", msg, keyvals)
}

func (a sdkWorkerLoggerAdapter) Info(msg string, keyvals ...any) {
	a.logger.Info(fmt.Sprintf("%s %v", msg, keyvals))
}

func (a sdkWorkerLoggerAdapter) Warn(msg string, keyvals ...any) {
	a.logger.Warn(fmt.Sprintf("%s %v", msg, keyvals))
}

func (a sdkWorkerLoggerAdapter) Error(msg string, keyvals ...any) {
	a.logger.Error(fmt.Sprintf("%s %v", msg, keyvals))
}

