package metrics

import (
	"context"
	"io"

	"github.com/superdurable/dex/server/common/log"
	"github.com/superdurable/dex/server/common/log/tag"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// UnaryServerErrorLoggingInterceptor logs a WARN for every inbound unary RPC
// that returns a non-nil error (excluding Canceled which is normal for
// client-driven disconnects).
func UnaryServerErrorLoggingInterceptor(logger log.Logger) grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req any,
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (any, error) {
		resp, err := handler(ctx, req)
		if err != nil {
			logGRPCError(logger, "Inbound unary RPC error", info.FullMethod, err)
		}
		return resp, err
	}
}

// StreamServerErrorLoggingInterceptor logs a WARN for every inbound streaming
// RPC that returns a non-nil error (excluding Canceled and io.EOF which are
// normal stream termination signals).
func StreamServerErrorLoggingInterceptor(logger log.Logger) grpc.StreamServerInterceptor {
	return func(
		srv any,
		ss grpc.ServerStream,
		info *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) error {
		err := handler(srv, ss)
		if err != nil {
			logGRPCError(logger, "Inbound stream RPC error", info.FullMethod, err)
		}
		return err
	}
}

// UnaryClientErrorLoggingInterceptor logs a WARN for every outbound unary RPC
// that returns a non-nil error.
func UnaryClientErrorLoggingInterceptor(logger log.Logger) grpc.UnaryClientInterceptor {
	return func(
		ctx context.Context,
		method string,
		req, reply any,
		cc *grpc.ClientConn,
		invoker grpc.UnaryInvoker,
		opts ...grpc.CallOption,
	) error {
		err := invoker(ctx, method, req, reply, cc, opts...)
		if err != nil {
			logGRPCError(logger, "Outbound unary RPC error", method, err)
		}
		return err
	}
}

// StreamClientErrorLoggingInterceptor logs a WARN when an outbound streaming
// RPC fails to open.
func StreamClientErrorLoggingInterceptor(logger log.Logger) grpc.StreamClientInterceptor {
	return func(
		ctx context.Context,
		desc *grpc.StreamDesc,
		cc *grpc.ClientConn,
		method string,
		streamer grpc.Streamer,
		opts ...grpc.CallOption,
	) (grpc.ClientStream, error) {
		clientStream, err := streamer(ctx, desc, cc, method, opts...)
		if err != nil {
			logGRPCError(logger, "Outbound stream RPC open error", method, err)
			return nil, err
		}
		return clientStream, nil
	}
}

func logGRPCError(logger log.Logger, msg string, fullMethod string, err error) {
	code := status.Code(err)
	if code == codes.Canceled || err == io.EOF {
		return
	}
	logger.Warn(msg,
		tag.APIName(fullMethod),
		tag.GRPCCode(code.String()),
		tag.Error(err),
	)
}
