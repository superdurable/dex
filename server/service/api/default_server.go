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

package api

import (
	"context"
	"net"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/superdurable/dex/server/common/log"
	"github.com/superdurable/dex/server/common/log/tag"
	"github.com/superdurable/dex/server/config"
	"github.com/superdurable/dex/server/persistence"
)

const PathStartProcessExecution = "/api/v1/dex/service/process-execution/start"
const PathDescribeProcessExecution = "/api/v1/dex/service/process-execution/describe"
const PathStopProcessExecution = "/api/v1/dex/service/process-execution/stop"
const PathPublishToLocalQueue = "/api/v1/dex/service/process-execution/publish-to-local-queue"
const PathProcessExecutionRpc = "/api/v1/dex/service/process-execution/rpc"
const PathListProcessExecutions = "/api/v1/dex/service/process-execution/list"
const PathWaitForProcessCompletion = "/api/v1/dex/service/process-execution/wait-for-process-completion"

type defaultSever struct {
	rootCtx context.Context
	cfg     config.Config
	logger  log.Logger

	engine     *gin.Engine
	httpServer *http.Server
}

func NewDefaultAPIServerWithGin(
	rootCtx context.Context,
	cfg config.Config,
	processStore persistence.ProcessStore,
	visibilityStore persistence.VisibilityStore,
	logger log.Logger,
) Server {
	engine := gin.Default()

	handler := newGinHandler(cfg, processStore, visibilityStore, logger)

	engine.GET("/", func(c *gin.Context) {
		c.String(http.StatusOK, "Hello from Dex server!")
	})
	engine.POST(PathStartProcessExecution, handler.StartProcess)
	engine.POST(PathDescribeProcessExecution, handler.DescribeProcess)
	engine.POST(PathStopProcessExecution, handler.StopProcess)
	engine.POST(PathPublishToLocalQueue, handler.PublishToLocalQueue)
	engine.POST(PathProcessExecutionRpc, handler.Rpc)
	engine.POST(PathListProcessExecutions, handler.ListProcessExecutions)
	engine.POST(PathWaitForProcessCompletion, handler.WaitForProcessCompletion)

	svrCfg := cfg.ApiService.HttpServer
	httpServer := &http.Server{
		Addr:              svrCfg.Address,
		ReadTimeout:       svrCfg.ReadTimeout,
		WriteTimeout:      svrCfg.WriteTimeout,
		ReadHeaderTimeout: svrCfg.ReadHeaderTimeout,
		IdleTimeout:       svrCfg.IdleTimeout,
		MaxHeaderBytes:    svrCfg.MaxHeaderBytes,
		TLSConfig:         svrCfg.TLSConfig,
		Handler:           engine,
		BaseContext: func(listener net.Listener) context.Context {
			// for graceful shutdown
			return rootCtx
		},
	}

	return &defaultSever{
		rootCtx:    rootCtx,
		cfg:        cfg,
		logger:     logger,
		engine:     engine,
		httpServer: httpServer,
	}
}

func (s defaultSever) Start() error {
	go func() {
		err := s.httpServer.ListenAndServe()
		s.logger.Info("Http Server for API service is closed", tag.Error(err))
	}()

	return nil
}

func (s defaultSever) Stop(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}
