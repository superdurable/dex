package metrics

const (
	MetricTierCritical  = 1
	MetricTierInfo      = 2
	MetricTierDebug     = 3
	MetricTierDeepDebug = 4
)

// general grpc API metrics
var (
	CounterInboundGrpcApiError Counter = registerCounter(MetricTierInfo, "inbound_grpc_api_error_counter")
	LatencyInboundGrpcApi      Latency = registerLatency(MetricTierInfo, "inbound_grpc_api_latency")

	CounterOutboundGrpcApiError Counter = registerCounter(MetricTierInfo, "outbound_grpc_api_error_counter")
	LatencyOutboundGrpcApi      Latency = registerLatency(MetricTierDebug, "outbound_grpc_api_latency")

	CounterInboundGrpcStreamApiError      Counter = registerCounter(MetricTierInfo, "inbound_grpc_stream_api_error_counter")
	LatencyInboundGrpcStreamApi           Latency = registerLatency(MetricTierInfo, "inbound_grpc_stream_api_latency")
	LatencyInboundGrpcStreamApiFirstChunk Latency = registerLatency(MetricTierDebug, "inbound_grpc_stream_api_first_chunk_latency")

	CounterOutboundGrpcStreamApiError      Counter = registerCounter(MetricTierInfo, "outbound_grpc_stream_api_error_counter")
	LatencyOutboundGrpcStreamApi           Latency = registerLatency(MetricTierDebug, "outbound_grpc_stream_api_latency")
	CounterOutboundGrpcStreamApiChunkCount Counter = registerCounter(MetricTierDebug, "outbound_grpc_stream_api_chunk_counter")
	LatencyOutboundGrpcStreamApiFirstChunk Latency = registerLatency(MetricTierDebug, "outbound_grpc_stream_api_first_chunk_latency")

	HistogramInboundGRPCInputSize   Histogram = registerHistogram(MetricTierDebug, "inbound_grpc_input_size")
	HistogramInboundGRPCOutputSize  Histogram = registerHistogram(MetricTierDebug, "inbound_grpc_output_size")
	HistogramOutboundGRPCInputSize  Histogram = registerHistogram(MetricTierDebug, "outbound_grpc_input_size")
	HistogramOutboundGRPCOutputSize Histogram = registerHistogram(MetricTierDebug, "outbound_grpc_output_size")
)

// run metrics
var (
	CounterRunStarted   Counter = registerCounter(MetricTierCritical, "run_attempt_started_counter")
	LatencyRunExecution Latency = registerLatency(MetricTierCritical, "run_execution_latency")
)
