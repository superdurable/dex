package metrics

// NOTE: always use "_" and lower case for naming -- Prometheus doesn't allow "-" or ".".
// All the names will be lower-cased in metrics provider.

// general
var CounterCriticalError Counter = internalCounter(MetricTierCritical, "critical_error_counter")

const (
	// Required for monitoring and understand the health of the system
	MetricTierCritical = 1
	// Required for minimum production grade operation
	MetricTierInfo = 2
	// Needed for basic debug & troubleshooting
	MetricTierDebug = 3
	// Needed for very deep debug & troubleshooting
	MetricTierDeepDebug = 4
)

// grpc API metrics(emitted by clientStreamWrapper in grpc_interceptor.go)
var (
	// inbound regular
	CounterInboundGrpcApiError Counter = internalCounter(MetricTierInfo, "inbound_grpc_api_error_counter")
	LatencyInboundGrpcApi      Latency = internalLatency(MetricTierInfo, "inbound_grpc_api_latency")

	// outbound regular
	CounterOutboundGrpcApiError Counter = internalCounter(MetricTierInfo, "outbound_grpc_api_error_counter")
	LatencyOutboundGrpcApi      Latency = internalLatency(MetricTierDebug, "outbound_grpc_api_latency")

	// inbound stream
	CounterInboundGrpcStreamApiError      Counter = internalCounter(MetricTierInfo, "inbound_grpc_stream_api_error_counter")
	LatencyInboundGrpcStreamApi           Latency = internalLatency(MetricTierInfo, "inbound_grpc_stream_api_latency")
	LatencyInboundGrpcStreamApiFirstChunk Latency = internalLatency(MetricTierDebug, "inbound_grpc_stream_api_first_chunk_latency")

	// outbound stream
	CounterOutboundGrpcStreamApiError      Counter = internalCounter(MetricTierInfo, "outbound_grpc_stream_api_error_counter")
	LatencyOutboundGrpcStreamApi           Latency = internalLatency(MetricTierDebug, "outbound_grpc_stream_api_latency")
	CounterOutboundGrpcStreamApiChunkCount Counter = internalCounter(MetricTierDebug, "outbound_grpc_stream_api_chunk_counter")
	LatencyOutboundGrpcStreamApiFirstChunk Latency = internalLatency(MetricTierDebug, "outbound_grpc_stream_api_first_chunk_latency")

	// grpc size metrics
	// These metrics are for Unary + Stream

	HistogramInboundGRPCInputSize   Histogram = internalHistogram(MetricTierDebug, "inbound_grpc_input_size")
	HistogramInboundGRPCOutputSize  Histogram = internalHistogram(MetricTierDebug, "inbound_grpc_output_size")
	HistogramOutboundGRPCInputSize  Histogram = internalHistogram(MetricTierDebug, "outbound_grpc_input_size")
	HistogramOutboundGRPCOutputSize Histogram = internalHistogram(MetricTierDebug, "outbound_grpc_output_size")
)

// run metrics
//
// These run-lifecycle counters/latencies are intentionally separate from the
// generic inbound gRPC API metrics (LatencyInboundGrpcApi /
// CounterInboundGrpcApiError above). Reasons:
//   - StartRun is forwarded to the shard owner via tryForward, so the
//     gRPC API metric is recorded on whichever node served the public
//     request — not on the node that actually persisted the run. The
//     per-shard "real attempts" view requires an engine-side counter.
//   - Run lifecycle spans many RPCs (Start, ProcessStep*, Stop) and timer
//     firings; gRPC metrics can't aggregate that into success / latency
//     per run.
//
// Invariant (modulo metric loss on crash):
//
//	started = success + canceled + failed retriable + failed after retry + exceed max attempts
var (
	CounterRunAttemptStarted Counter = internalCounter(MetricTierCritical, "run_attempt_started_counter")
	CounterRunSuccess        Counter = internalCounter(MetricTierCritical, "run_success_counter")
	LatencyRunExecution      Latency = internalLatency(MetricTierCritical, "run_execution_latency")
)

// WaitFor / channel / unblock metrics
//
// Emitted by the engine for the worker-driven WaitFor protocol. See
// docs/wait-for-conditions-design.md.
//
// Cardinality is constrained: tagged by namespace + error_kind only.
// channel_name is intentionally NOT a tag because dynamic channel families
// (e.g. "order-update-{id}") would explode cardinality.
var (
	// Per StepUnblocked entry committed via ProcessStepsUnblocked.
	// Tagged by namespace.
	CounterStepsUnblockedCommitted Counter = internalCounter(MetricTierDeepDebug, "steps_unblocked_committed_counter")
	// Tagged by namespace + error_kind.
	CounterProcessStepsUnblockedError Counter = internalCounter(MetricTierDeepDebug, "process_steps_unblocked_error_counter")
	// Tagged by namespace.
	LatencyProcessStepsUnblocked Latency = internalLatency(MetricTierDeepDebug, "process_steps_unblocked_latency")
	// Sum of consumed_count applied via ReplaceUnconsumedChannels across
	// completion RPCs and ProcessStepsUnblocked. Tagged by namespace only;
	// channel_name is not tagged for cardinality reasons.
	CounterChannelMessagesConsumed Counter = internalCounter(MetricTierDeepDebug, "channel_messages_consumed_counter")
	// PublishToChannel calls accepted by the engine.
	CounterChannelExternalPublish Counter = internalCounter(MetricTierDeepDebug, "channel_external_publish_counter")
	// tryProcessStepWaitForTimerFired drops the fire when status != AllStepsWaiting.
	// Counts how often the SDK-local-timer path is the one that rescues latency.
	CounterStepWaitForTimerFiredDroppedNotAllWaiting Counter = internalCounter(MetricTierDebug, "step_wait_for_timer_fired_dropped_not_all_waiting_counter")
	// A Running → AllStepsWaitingForConditions transition (in
	// ProcessStepExecuteCompleted / ProcessStepWaitForCompleted) was
	// upgraded to Pending in the same commit because UnconsumedChannelMessages
	// already satisfied a waiting step. Indicates either a stale buffered
	// publish from a Running-branch enqueue, or that the SDK exit-drain
	// missed a late-arriving externalMsgCh push. Tagged by namespace.
	CounterRunningToAllStepsWaitingDispatchedFromUnconsumed Counter = internalCounter(MetricTierInfo, "running_to_all_steps_waiting_dispatched_from_unconsumed_counter")
	CounterProcessReleaseRunAllStepsWaitingParked           Counter = internalCounter(MetricTierInfo, "process_release_run_all_steps_waiting_parked_counter")
	CounterWorkerSuppliedMethodExeIDCount                   Counter = internalCounter(MetricTierDeepDebug, "worker_supplied_method_exe_id_count_counter")
	// One per cancelled step_exe_id committed by the engine via
	// StepExecuteCompletedRequest.canceled_step_executions (the field
	// populated by the SDK's StepDecision.WithCancelingSiblingStepExecution
	// API). Tagged by namespace; not tagged by flow_type so cardinality
	// stays bounded. Already-absent (idempotent) cancellation requests
	// are NOT counted — only IDs the engine actually deleted.
	CounterStepExecutionCancelled Counter = internalCounter(MetricTierDebug, "step_execution_cancelled_counter")
)

// ForkRun metrics — tagged by outcome only.
var (
	CounterForkRunRequests Counter = internalCounter(MetricTierInfo, "fork_run_requests_counter")
	LatencyForkRun           Latency = internalLatency(MetricTierInfo, "fork_run_latency")
)

// task processing metrics
// Tagged by task_type (e.g. "immediate_initial_dispatch", "timer_heartbeat")
var (
	CounterTaskStarted   Counter = internalCounter(MetricTierInfo, "task_started_counter")
	CounterTaskSucceeded Counter = internalCounter(MetricTierInfo, "task_succeeded_counter")
	CounterTaskFailed    Counter = internalCounter(MetricTierInfo, "task_failed_counter")
	LatencyTaskExecution Latency = internalLatency(MetricTierInfo, "task_execution_latency")

	// LatencyWorkerPoolSubmitBlocked measures how long a batch reader blocked
	// waiting for a free slot in the worker pool channel. Non-zero values indicate
	// the worker pool is at capacity and batch readers are stalling.
	LatencyWorkerPoolSubmitBlocked Latency = internalLatency(MetricTierInfo, "worker_pool_submit_blocked_latency")
)

// batch read/delete metrics
// Tagged by task_kind: "immediate" or "timer"
var (
	CounterBatchReadSuccess    Counter   = internalCounter(MetricTierInfo, "batch_read_success_counter")
	CounterBatchReadFailed     Counter   = internalCounter(MetricTierInfo, "batch_read_failed_counter")
	HistogramBatchReadCount    Histogram = internalHistogram(MetricTierInfo, "batch_read_count")
	CounterRangeDeleteSuccess  Counter   = internalCounter(MetricTierInfo, "range_delete_success_counter")
	CounterRangeDeleteFailed   Counter   = internalCounter(MetricTierInfo, "range_delete_failed_counter")
	CounterShutdownDeleteBatch Counter   = internalCounter(MetricTierInfo, "shutdown_delete_batch_counter")

	// LatencyTaskScheduledToPickup measures the delay between when a task was
	// supposed to be processed and when it was actually picked up by the worker
	// pool. For immediate tasks: created_at → pickup. For timer tasks: fire_at → pickup.
	LatencyTaskScheduledToPickup Latency = internalLatency(MetricTierInfo, "task_scheduled_to_pickup_latency")
)

// watermark / offset metrics
var (
	GaugeWatermark       Gauge = internalGauge(MetricTierInfo, "task_watermark")
	GaugeCommittedOffset Gauge = internalGauge(MetricTierInfo, "task_committed_offset")
	GaugePendingSetSize  Gauge = internalGauge(MetricTierDebug, "task_pending_set_size")
)

// task sequence metrics
var (
	CounterTaskSeqAllocated Counter = internalCounter(MetricTierDebug, "task_seq_allocated_counter")
)

// OpsFIFO outbox metrics. Tagged by:
//   - task_queue_type=ops_fifo on the read/delete counters (shared with immediate/timer)
//   - ops_fifo_task_target=history|visibility on the per-target executed/duration metrics
var (
	// Bumped on every BatchInsertHistory / BatchUpsertVisibility success
	// (one increment per group within a batch). Use for a "batches /sec
	// processed" panel. Tagged by ops_fifo_task_target.
	CounterOpsTaskBatchExecuted Counter = internalCounter(MetricTierInfo, "ops_fifo_task_batch_executed_counter")
	// Bumped on EVERY failed batch attempt — alert directly on the rate.
	// FIFO can't DLQ-and-skip, so a sustained non-zero rate means writes
	// are stalled and the run-state queue is moving while observability
	// falls behind.
	CounterOpsTaskBatchStuck Counter = internalCounter(MetricTierInfo, "ops_fifo_task_batch_stuck_counter")
	// Histogram of how many tasks were pulled by a single read.
	HistogramOpsTaskBatchSize Histogram = internalHistogram(MetricTierInfo, "ops_fifo_task_batch_size")
	// Per-target wall-clock of the batch execution call (BatchInsertHistory
	// or BatchUpsertVisibility). Tagged by ops_fifo_task_target.
	LatencyOpsTaskBatchExecution Latency = internalLatency(MetricTierInfo, "ops_fifo_task_batch_execution_latency")
	// FIFO lag: now - earliest pending task created_at, sampled per batch.
	// Climbs when the OpsFIFO falls behind run state.
	LatencyOpsTaskFIFOLag Latency = internalLatency(MetricTierInfo, "ops_fifo_task_lag_latency")
)

// shard management metrics
var (
	CounterShardClaimed     Counter = internalCounter(MetricTierInfo, "shard_claimed_counter")
	CounterShardClaimFailed Counter = internalCounter(MetricTierInfo, "shard_claim_failed_counter")
	CounterShardReleased    Counter = internalCounter(MetricTierInfo, "shard_released_counter")
	CounterShardLost        Counter = internalCounter(MetricTierInfo, "shard_lost_counter")
	CounterShardRebalance   Counter = internalCounter(MetricTierDebug, "shard_rebalance_counter")
	GaugeShardOwnedCount    Gauge   = internalGauge(MetricTierCritical, "shard_owned_count")

	// LatencyShardClaimSkewWait records the time a freshly-claimed shard
	// spends waiting for the local clock to surpass the previous owner's
	// committed timer watermark before StartComponents is fired. Recorded
	// only when the wait is non-zero. p99 trending into the tens of seconds
	// indicates serious NTP regression on this node.
	LatencyShardClaimSkewWait Latency = internalLatency(MetricTierInfo, "shard_claim_skew_wait_latency")
)

// tasklist metrics — placeholders for the tasklist subsystem to wire up
// when its observability hooks land. The legacy "group_*" / "matcher_*" /
// "batch_async_match_*" metric blocks were removed alongside the
// group-matching subsystem they tracked.
var (
	CounterTasklistClaimed     Counter = internalCounter(MetricTierInfo, "tasklist_claimed_counter")
	CounterTasklistClaimFailed Counter = internalCounter(MetricTierInfo, "tasklist_claim_failed_counter")
	GaugeTasklistOwnedCount    Gauge   = internalGauge(MetricTierCritical, "tasklist_owned_count")

	CounterTasklistRangeIDMismatch Counter = internalCounter(MetricTierInfo, "tasklist_range_id_mismatch_counter")

	// Partition fan-in (non-root → root) metrics. ForwardPoll is the read
	// fan-in; ForwardTask is the write-path stream relay.
	CounterForwardPollAttempt Counter = internalCounter(MetricTierDebug, "tasklist_forward_poll_attempt_counter")
	CounterForwardPollHit     Counter = internalCounter(MetricTierInfo, "tasklist_forward_poll_hit_counter")
	CounterForwardPollEmpty   Counter = internalCounter(MetricTierDebug, "tasklist_forward_poll_empty_counter")
	CounterForwardPollError   Counter = internalCounter(MetricTierInfo, "tasklist_forward_poll_error_counter")
	CounterForwardTaskAttempt Counter = internalCounter(MetricTierInfo, "tasklist_forward_task_attempt_counter")
	CounterForwardTaskMatched Counter = internalCounter(MetricTierInfo, "tasklist_forward_task_matched_counter")
	CounterDispatchLocalWrite Counter = internalCounter(MetricTierDebug, "tasklist_dispatch_local_write_counter")
)

// dead letter queue metrics
var (
	CounterTaskDLQWritten     Counter = internalCounter(MetricTierInfo, "task_dlq_written_counter")
	CounterTaskDLQWriteFailed Counter = internalCounter(MetricTierCritical, "task_dlq_write_failed_counter")
)

// persistence metrics
var (
	// store method metrics (used by metered store wrappers)
	CounterStoreMethodError Counter = internalCounter(MetricTierInfo, "store_method_error_counter")
	LatencyStoreMethod      Latency = internalLatency(MetricTierInfo, "store_method_latency")
)
