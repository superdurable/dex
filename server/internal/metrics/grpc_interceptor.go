package metrics

import (
	"context"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// UnaryServerMetricsReportingInterceptor reports metrics for unary gRPC calls
func UnaryServerMetricsReportingInterceptor() grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req any,
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (any, error) {
		start := time.Now()

		apiNameTag := TagApiNameFromProtoFullMethod(info.FullMethod)

		if reqMsg, ok := req.(proto.Message); ok {
			reqSize := proto.Size(reqMsg)
			HistogramInboundGRPCInputSize.Record(float64(reqSize), apiNameTag)
		}

		resp, err := handler(ctx, req)

		if err == nil {
			if respMsg, ok := resp.(proto.Message); ok {
				respSize := proto.Size(respMsg)
				HistogramInboundGRPCOutputSize.Record(float64(respSize), apiNameTag)
			}
		}

		latency := time.Since(start)

		if err != nil {
			CounterInboundGrpcApiError.Inc(apiNameTag, TagResponseCodeFromProtoError(err))

		} else {
			LatencyInboundGrpcApi.Record(latency, apiNameTag)
		}

		return resp, err
	}
}

// UnaryClientMetricsReportingInterceptor reports metrics for outbound unary gRPC calls
func UnaryClientMetricsReportingInterceptor() grpc.UnaryClientInterceptor {
	return func(
		ctx context.Context,
		method string,
		req, reply any,
		cc *grpc.ClientConn,
		invoker grpc.UnaryInvoker,
		opts ...grpc.CallOption,
	) error {
		start := time.Now()

		apiNameTag := TagApiNameFromProtoFullMethod(method)

		if reqMsg, ok := req.(proto.Message); ok {
			reqSize := proto.Size(reqMsg)
			HistogramOutboundGRPCInputSize.Record(float64(reqSize), apiNameTag)
		}

		err := invoker(ctx, method, req, reply, cc, opts...)

		if err == nil {
			if replyMsg, ok := reply.(proto.Message); ok {
				replySize := proto.Size(replyMsg)
				HistogramOutboundGRPCOutputSize.Record(float64(replySize), apiNameTag)
			}
		}

		latency := time.Since(start)

		if err != nil {
			CounterOutboundGrpcApiError.Inc(apiNameTag, TagResponseCodeFromProtoError(err))
		} else {
			LatencyOutboundGrpcApi.Record(latency, apiNameTag)
		}

		return err
	}
}

// serverStreamWrapper wraps grpc.ServerStream to intercept SendMsg and RecvMsg
type serverStreamWrapper struct {
	grpc.ServerStream
	ctx            context.Context
	apiNameTag     Tag
	startTime      time.Time
	firstChunkSent bool
}

func (w *serverStreamWrapper) RecvMsg(m any) error {
	err := w.ServerStream.RecvMsg(m)
	if err == nil {
		if msg, ok := m.(proto.Message); ok {
			size := proto.Size(msg)
			HistogramInboundGRPCInputSize.Record(float64(size), w.apiNameTag)
		}
	}
	return err
}

func (w *serverStreamWrapper) SendMsg(m any) error {
	if msg, ok := m.(proto.Message); ok {
		size := proto.Size(msg)
		HistogramInboundGRPCOutputSize.Record(float64(size), w.apiNameTag)
	}
	if !w.firstChunkSent {
		w.firstChunkSent = true
		LatencyInboundGrpcStreamApiFirstChunk.Record(time.Since(w.startTime), w.apiNameTag)
	}
	return w.ServerStream.SendMsg(m)
}

// StreamServerMetricsReportingInterceptor reports metrics for server-side streaming gRPC calls
func StreamServerMetricsReportingInterceptor() grpc.StreamServerInterceptor {
	return func(
		srv any,
		ss grpc.ServerStream,
		info *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) error {
		start := time.Now()
		ctx := ss.Context()

		apiNameTag := TagApiNameFromProtoFullMethod(info.FullMethod)

		wrappedStream := &serverStreamWrapper{
			ServerStream: ss,
			ctx:          ctx,
			apiNameTag:   apiNameTag,
			startTime:    start,
		}

		err := handler(srv, wrappedStream)

		latency := time.Since(start)

		if err != nil && status.Code(err) != codes.Canceled {
			CounterInboundGrpcStreamApiError.Inc(apiNameTag, TagResponseCodeFromProtoError(err))
		} else {
			LatencyInboundGrpcStreamApi.Record(latency, apiNameTag)
		}

		return err
	}
}

// clientStreamWrapper wraps grpc.ClientStream to intercept SendMsg, RecvMsg, and CloseSend
// for emitting outbound stream metrics (size, latency, chunk count).
type clientStreamWrapper struct {
	grpc.ClientStream
	ctx                context.Context
	apiNameTag         Tag
	startTime          time.Time
	firstChunkReceived bool
	recvCount          int
	hadRecvErr         bool
}

func (w *clientStreamWrapper) SendMsg(m any) error {
	if msg, ok := m.(proto.Message); ok {
		size := proto.Size(msg)
		HistogramOutboundGRPCOutputSize.Record(float64(size), w.apiNameTag)
	}
	err := w.ClientStream.SendMsg(m)
	if err != nil {
		CounterOutboundGrpcStreamApiError.Inc(w.apiNameTag, TagResponseCodeFromProtoError(err))
		return err
	}
	return err
}

func (w *clientStreamWrapper) RecvMsg(m any) error {
	err := w.ClientStream.RecvMsg(m)
	if err == nil {
		if msg, ok := m.(proto.Message); ok {
			size := proto.Size(msg)
			HistogramOutboundGRPCInputSize.Record(float64(size), w.apiNameTag)
		}
		w.recvCount++
		if !w.firstChunkReceived {
			w.firstChunkReceived = true
			LatencyOutboundGrpcStreamApiFirstChunk.Record(time.Since(w.startTime), w.apiNameTag)
		}
	} else {
		w.hadRecvErr = true
		CounterOutboundGrpcStreamApiError.Inc(
			w.apiNameTag,
			TagResponseCodeFromProtoError(err),
			w.errorSubCategoryTag(),
		)
	}
	return err
}

func (w *clientStreamWrapper) errorSubCategoryTag() Tag {
	md := w.Trailer()
	if md != nil {
		if vals := md.Get("error_sub_category"); len(vals) > 0 && vals[0] != "" {
			return Tag{Key: tagKeyErrorSubCategory, Value: anyTagValue(vals[0])}
		}
	}
	return Tag{Key: tagKeyErrorSubCategory, Value: anyTagValue("")}
}

func (w *clientStreamWrapper) CloseSend() error {
	if w.recvCount > 0 && !w.hadRecvErr {
		CounterOutboundGrpcStreamApiChunkCount.IncBy(w.recvCount, w.apiNameTag)
		LatencyOutboundGrpcStreamApi.Record(time.Since(w.startTime), w.apiNameTag)
	}
	return w.ClientStream.CloseSend()
}

// StreamClientMetricsReportingInterceptor reports metrics for client-side streaming gRPC calls
func StreamClientMetricsReportingInterceptor() grpc.StreamClientInterceptor {
	return func(
		ctx context.Context,
		desc *grpc.StreamDesc,
		cc *grpc.ClientConn,
		method string,
		streamer grpc.Streamer,
		opts ...grpc.CallOption,
	) (grpc.ClientStream, error) {
		apiNameTag := TagApiNameFromProtoFullMethod(method)

		clientStream, err := streamer(ctx, desc, cc, method, opts...)
		if err != nil {
			CounterOutboundGrpcStreamApiError.Inc(apiNameTag, TagResponseCodeFromProtoError(err))
			return nil, err
		}

		wrappedStream := &clientStreamWrapper{
			ClientStream: clientStream,
			ctx:          ctx,
			apiNameTag:   apiNameTag,
			startTime:    time.Now(),
		}

		return wrappedStream, nil
	}
}
