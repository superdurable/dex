// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package api

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/superdurable/iwf/config"
	"github.com/superdurable/iwf/gen/iwfpb"
	uclient "github.com/superdurable/iwf/service/client"
	"github.com/superdurable/iwf/service/common/blobstore"
	"github.com/superdurable/iwf/service/common/log"
	"github.com/superdurable/iwf/service/common/log/tag"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

// Server hosts FlowService, InternalService, and grpc.health.
type Server struct {
	cfg        *config.ApiConfig
	grpcServer *grpc.Server
	healthSrv  *health.Server
	handler    *handler
	logger     log.Logger
	readyCheck func(context.Context) error
}

func NewServer(
	apiCfg *config.ApiConfig,
	extStore *config.ExternalStorageConfig,
	interpreterCfg *config.Interpreter,
	client uclient.UnifiedClient,
	logger log.Logger,
	store blobstore.BlobStore,
	readyCheck func(context.Context) error,
) *Server {
	if apiCfg == nil {
		panic("apiCfg must not be nil")
	}
	if client == nil {
		panic("client must not be nil")
	}
	if logger == nil {
		panic("logger must not be nil")
	}
	if store == nil {
		panic("store must not be nil")
	}
	if extStore == nil {
		panic("extStore must not be nil")
	}
	if interpreterCfg == nil {
		panic("interpreterCfg must not be nil")
	}
	if readyCheck == nil {
		readyCheck = func(context.Context) error { return nil }
	}

	handler := newHandler(apiCfg, extStore, interpreterCfg, client, logger, store)
	maxMsg := apiCfg.EffectiveGrpcMaxMessageBytes()
	healthSrv := health.NewServer()
	healthSrv.SetServingStatus("", healthpb.HealthCheckResponse_NOT_SERVING)
	healthSrv.SetServingStatus(iwfpb.FlowService_ServiceDesc.ServiceName, healthpb.HealthCheckResponse_NOT_SERVING)
	healthSrv.SetServingStatus(iwfpb.InternalService_ServiceDesc.ServiceName, healthpb.HealthCheckResponse_NOT_SERVING)

	grpcServer := grpc.NewServer(
		grpc.MaxRecvMsgSize(maxMsg),
		grpc.MaxSendMsgSize(maxMsg),
		grpc.ChainUnaryInterceptor(unaryRecover(logger), unaryLog(logger)),
	)
	iwfpb.RegisterFlowServiceServer(grpcServer, handler)
	iwfpb.RegisterInternalServiceServer(grpcServer, handler)
	healthpb.RegisterHealthServer(grpcServer, healthSrv)

	return &Server{
		cfg:        apiCfg,
		grpcServer: grpcServer,
		healthSrv:  healthSrv,
		handler:    handler,
		logger:     logger,
		readyCheck: readyCheck,
	}
}

// Run listens on the configured port.
func (s *Server) Run() error {
	port := s.cfg.Port
	if port == 0 {
		port = config.DefaultApiPort
	}
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return err
	}
	s.logger.Info("FlowService gRPC listening", tag.Value(lis.Addr().String()))

	if err := s.readyCheck(context.Background()); err != nil {
		s.logger.Error("initial readiness check failed", tag.Error(err))
	} else {
		s.setServing(true)
	}

	return s.grpcServer.Serve(lis)
}

// GracefulStop stops accepting requests before closing dependencies.
func (s *Server) GracefulStop(timeout time.Duration) {
	s.setServing(false)
	done := make(chan struct{})
	go func() {
		s.grpcServer.GracefulStop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		s.grpcServer.Stop()
	}
	s.handler.close()
}

func (s *Server) setServing(serving bool) {
	st := healthpb.HealthCheckResponse_NOT_SERVING
	if serving {
		st = healthpb.HealthCheckResponse_SERVING
	}
	s.healthSrv.SetServingStatus("", st)
	s.healthSrv.SetServingStatus(iwfpb.FlowService_ServiceDesc.ServiceName, st)
	s.healthSrv.SetServingStatus(iwfpb.InternalService_ServiceDesc.ServiceName, st)
}

func unaryRecover(logger log.Logger) grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req interface{},
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (resp interface{}, err error) {
		defer func() {
			if recovered := recover(); recovered != nil {
				logger.Error(
					"gRPC handler panic",
					tag.Value(fmt.Sprintf("%v", recovered)),
					tag.Value(info.FullMethod),
				)
				err = status.Error(codes.Internal, "internal panic")
			}
		}()
		return handler(ctx, req)
	}
}

func unaryLog(logger log.Logger) grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req interface{},
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (interface{}, error) {
		start := time.Now()
		resp, err := handler(ctx, req)
		logger.Debug(
			"gRPC call",
			tag.Value(info.FullMethod),
			tag.Value(time.Since(start).String()),
			tag.Error(err),
		)
		return resp, err
	}
}
