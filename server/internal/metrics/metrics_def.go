// Copyright (c) 2023-2026 Super Durable, Inc.
//
// This file is part of Dex
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program. If not, see <https://www.gnu.org/licenses/>.

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
