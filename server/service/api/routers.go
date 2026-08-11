// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package api

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/superdurable/dex/config"
	"github.com/superdurable/dex/gen/dexpb"
	uclient "github.com/superdurable/dex/service/client"
	"github.com/superdurable/dex/service/common/attributestore"
	"github.com/superdurable/dex/service/common/blobstore"
	"github.com/superdurable/dex/service/common/log"
	"github.com/superdurable/dex/service/common/log/tag"
	"github.com/superdurable/dex/service/common/workerclient"
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
	blobStoreCfg *config.BlobStoreConfig,
	interpreterCfg *config.Interpreter,
	client uclient.UnifiedClient,
	logger log.Logger,
	store blobstore.BlobStore,
	attributeStore *attributestore.Manager,
	readyCheck func(context.Context) error,
	workerPool *workerclient.WorkerClientPool,
	extraUnaryInterceptors ...grpc.UnaryServerInterceptor,
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
	if blobStoreCfg == nil {
		panic("blobStoreCfg must not be nil")
	}
	if blobStoreCfg.EffectiveEnabled() && store == nil {
		panic("store must not be nil when blob storage is enabled")
	}
	if interpreterCfg == nil {
		panic("interpreterCfg must not be nil")
	}
	if attributeStore == nil {
		panic("attributeStore must not be nil")
	}
	if workerPool == nil {
		panic("workerPool must not be nil")
	}
	if readyCheck == nil {
		readyCheck = func(context.Context) error { return nil }
	}

	handler := newHandler(apiCfg, blobStoreCfg, interpreterCfg, client, logger, store, attributeStore, workerPool)
	maxMsg := apiCfg.EffectiveGrpcMaxMessageBytes()
	healthSrv := health.NewServer()
	healthSrv.SetServingStatus("", healthpb.HealthCheckResponse_NOT_SERVING)
	healthSrv.SetServingStatus(dexpb.FlowService_ServiceDesc.ServiceName, healthpb.HealthCheckResponse_NOT_SERVING)
	healthSrv.SetServingStatus(dexpb.InternalService_ServiceDesc.ServiceName, healthpb.HealthCheckResponse_NOT_SERVING)

	unaryInterceptors := append(
		extraUnaryInterceptors,
		unaryRecover(logger),
		unaryLog(logger),
	)
	grpcServer := grpc.NewServer(
		grpc.MaxRecvMsgSize(maxMsg),
		grpc.MaxSendMsgSize(maxMsg),
		grpc.ChainUnaryInterceptor(unaryInterceptors...),
	)
	dexpb.RegisterFlowServiceServer(grpcServer, handler)
	dexpb.RegisterInternalServiceServer(grpcServer, handler)
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
	return s.Serve(lis)
}

// Serve hosts the gRPC services on listener.
func (s *Server) Serve(listener net.Listener) error {
	s.logger.Info("FlowService gRPC listening", tag.Address(listener.Addr().String()))

	if err := s.readyCheck(context.Background()); err != nil {
		s.logger.Error("initial readiness check failed", tag.Error(err))
	} else {
		s.setServing(true)
	}

	return s.grpcServer.Serve(listener)
}

// GracefulStop stops accepting requests before closing dependencies.
func (s *Server) GracefulStop(timeout time.Duration) {
	s.StopServing(timeout)
	s.Close()
}

func (s *Server) StopServing(timeout time.Duration) {
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
}

func (s *Server) Close() {
	s.handler.close()
}

func (s *Server) setServing(serving bool) {
	st := healthpb.HealthCheckResponse_NOT_SERVING
	if serving {
		st = healthpb.HealthCheckResponse_SERVING
	}
	s.healthSrv.SetServingStatus("", st)
	s.healthSrv.SetServingStatus(dexpb.FlowService_ServiceDesc.ServiceName, st)
	s.healthSrv.SetServingStatus(dexpb.InternalService_ServiceDesc.ServiceName, st)
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
					tag.Panic(fmt.Sprintf("%v", recovered)),
					tag.OperationName(info.FullMethod),
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
			tag.OperationName(info.FullMethod),
			tag.Elapsed(time.Since(start)),
			tag.Error(err),
		)
		return resp, err
	}
}
