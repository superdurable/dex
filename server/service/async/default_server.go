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

package async

import (
	"context"
	"net"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/superdurable/dex/server/common/log"
	"github.com/superdurable/dex/server/common/log/tag"
	"github.com/superdurable/dex/server/config"
	"github.com/superdurable/dex/server/persistence"
	"go.uber.org/multierr"
)

const PathNotifyImmediateTasks = "/internal/api/v1/dex/notify-immediate-tasks"
const PathNotifyTimerTasks = "/internal/api/v1/dex/notify-timer-tasks"
const PathWaitForProcessCompletion = "/internal/api/v1/dex/wait-for-process-completion"

type defaultSever struct {
	rootCtx context.Context
	cfg     config.Config
	logger  log.Logger

	engine     *gin.Engine
	httpServer *http.Server
	svc        Service
}

func NewDefaultAsyncServerWithGin(
	rootCtx context.Context,
	cfg config.Config,
	processStore persistence.ProcessStore,
	visibilityStore persistence.VisibilityStore,
	logger log.Logger,
) Server {
	engine := gin.Default()

	svc := NewAsyncServiceImpl(rootCtx, processStore, visibilityStore, cfg, logger)

	membershipImpl := NewMembershipImpl(cfg, logger, &svc, ServerTypeAsync)

	handler := newGinHandler(cfg, svc, membershipImpl, logger)

	engine.POST(PathNotifyImmediateTasks, handler.NotifyImmediateTasks)
	engine.POST(PathNotifyTimerTasks, handler.NotifyTimerTasks)
	engine.POST(PathWaitForProcessCompletion, handler.WaitForProcessCompletion)

	svrCfg := cfg.AsyncService.InternalHttpServer
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
		svc:        svc,
	}
}

func (s defaultSever) Start() error {

	go func() {
		err := s.httpServer.ListenAndServe()
		s.logger.Info("Internal Http Server for Async service is closed", tag.Error(err))
	}()

	return s.svc.Start()
}

func (s defaultSever) Stop(ctx context.Context) error {
	err1 := s.httpServer.Shutdown(ctx)
	err2 := s.svc.Stop(ctx)
	return multierr.Combine(err1, err2)
}
