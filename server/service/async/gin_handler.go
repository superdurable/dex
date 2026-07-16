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
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/xcherryio/apis/goapi/xcapi"
	"github.com/superdurable/dex/server/common/log"
	"github.com/superdurable/dex/server/config"
)

type ginHandler struct {
	config     config.Config
	logger     log.Logger
	svc        Service
	membership Membership
}

func newGinHandler(cfg config.Config, svc Service, membership Membership, logger log.Logger) *ginHandler {
	return &ginHandler{
		config:     cfg,
		logger:     logger,
		svc:        svc,
		membership: membership,
	}
}

func (h *ginHandler) NotifyImmediateTasks(c *gin.Context) {
	var req xcapi.NotifyImmediateTasksRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		invalidRequestSchema(c)
		return
	}

	if h.config.AsyncService.Mode == config.AsyncServiceModeCluster {
		targetServerAddress := h.membership.GetAsyncServerAddressForShard(req.ShardId)
		if targetServerAddress != h.membership.GetServerAddress() {
			h.logger.Info(fmt.Sprintf("NotifyRemoteImmediateTaskAsyncInCluster: %s -> %s", h.membership.GetServerAddress(), targetServerAddress))

			h.svc.NotifyRemoteImmediateTaskAsyncInCluster(req, targetServerAddress)
			successRespond(c)
			return
		}
	}

	err := h.svc.NotifyPollingImmediateTask(req)
	if err != nil {
		invalidRequestForError(c, err)
		return
	}

	successRespond(c)
}

func (h *ginHandler) NotifyTimerTasks(c *gin.Context) {
	var req xcapi.NotifyTimerTasksRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		invalidRequestSchema(c)
		return
	}

	if h.config.AsyncService.Mode == config.AsyncServiceModeCluster {
		targetServerAddress := h.membership.GetAsyncServerAddressForShard(req.ShardId)
		if targetServerAddress != h.membership.GetServerAddress() {
			h.logger.Info(fmt.Sprintf("NotifyRemoteImmediateTaskAsyncInCluster: %s -> %s", h.membership.GetServerAddress(), targetServerAddress))

			h.svc.NotifyRemoteTimerTaskAsyncInCluster(req, targetServerAddress)
			successRespond(c)
			return
		}
	}

	err := h.svc.NotifyPollingTimerTask(req)
	if err != nil {
		invalidRequestForError(c, err)
		return
	}

	successRespond(c)
}

func (h *ginHandler) WaitForProcessCompletion(c *gin.Context) {
	var req xcapi.WaitForProcessCompletionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		invalidRequestSchema(c)
		return
	}

	if h.config.AsyncService.Mode == config.AsyncServiceModeCluster {
		targetServerAddress := h.membership.GetAsyncServerAddressForShard(req.ShardId)
		if targetServerAddress != h.membership.GetServerAddress() {
			h.logger.Info(fmt.Sprintf("AskRemoteToWaitForProcessCompletionInCluster: %s -> %s",
				h.membership.GetServerAddress(), targetServerAddress))

			resp, err := h.svc.AskRemoteToWaitForProcessCompletionInCluster(c.Request.Context(), req, targetServerAddress)
			if err != nil {
				c.JSON(http.StatusInternalServerError, err.Error())
				return
			}

			c.JSON(http.StatusOK, resp)
			return
		}
	}

	resp, err := h.svc.WaitForProcessCompletion(c.Request.Context(), req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, err.Error())
		return
	}

	c.JSON(http.StatusOK, resp)
}

func successRespond(c *gin.Context) {
	c.JSON(http.StatusOK, map[string]string{
		"message": "success",
	})
}
func invalidRequestSchema(c *gin.Context) {
	c.JSON(http.StatusBadRequest, xcapi.ApiErrorResponse{
		Details: xcapi.PtrString("invalid request schema"),
	})
}

func invalidRequestForError(c *gin.Context, err error) {
	c.JSON(http.StatusBadRequest, xcapi.ApiErrorResponse{
		Details: xcapi.PtrString(err.Error()),
	})
}
