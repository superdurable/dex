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
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/superdurable/dex/server/common/log"
	"github.com/superdurable/dex/server/common/log/tag"
	"github.com/superdurable/dex/server/config"
	"github.com/superdurable/dex/server/internal/persistence"
	"github.com/xcherryio/apis/goapi/xcapi"
)

type ginHandler struct {
	config config.Config
	logger log.Logger
	svc    Service
}

func newGinHandler(
	cfg config.Config,
	processStore persistence.ProcessStore,
	visibilityStore persistence.VisibilityStore,
	logger log.Logger,
) *ginHandler {
	svc := NewServiceImpl(cfg, processStore, visibilityStore, logger)
	return &ginHandler{
		config: cfg,
		logger: logger,
		svc:    svc,
	}
}

func (h *ginHandler) StartProcess(c *gin.Context) {
	var req xcapi.ProcessExecutionStartRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		invalidRequestSchema(c)
		return
	}
	var errResp *ErrorWithStatus
	var resp *xcapi.ProcessExecutionStartResponse
	h.logger.Debug("received StartProcess API request", tag.Value(h.toJson(req)))
	defer func() {
		h.logger.Debug("responded StartProcess API request", tag.Value(h.toJson(resp)), tag.Value(h.toJson(errResp)))
	}()

	resp, errResp = h.svc.StartProcess(c.Request.Context(), req)

	if errResp != nil {
		c.JSON(errResp.StatusCode, errResp.Error)
		return
	}
	c.JSON(http.StatusOK, resp)
}

func (h *ginHandler) StopProcess(c *gin.Context) {
	var req xcapi.ProcessExecutionStopRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		invalidRequestSchema(c)
		return
	}
	var err *ErrorWithStatus
	h.logger.Debug("received StopProcess API request", tag.Value(h.toJson(req)))
	defer func() {
		h.logger.Debug("responded StopProcess API request", tag.Value(h.toJson(err)))
	}()

	err = h.svc.StopProcess(c.Request.Context(), req)

	if err != nil {
		c.JSON(err.StatusCode, err.Error)
		return
	}

	c.JSON(http.StatusOK, struct{}{})
}

func (h *ginHandler) DescribeProcess(c *gin.Context) {
	var req xcapi.ProcessExecutionDescribeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		invalidRequestSchema(c)
		return
	}
	var resp *xcapi.ProcessExecutionDescribeResponse
	var errResp *ErrorWithStatus

	h.logger.Debug("received DescribeProcess API request", tag.Value(h.toJson(req)))
	defer func() {
		h.logger.Debug("responded DescribeProcess API request", tag.Value(h.toJson(resp)), tag.Value(h.toJson(errResp)))
	}()

	resp, errResp = h.svc.DescribeLatestProcess(c.Request.Context(), req)

	if errResp != nil {
		c.JSON(errResp.StatusCode, errResp.Error)
		return
	}

	c.JSON(http.StatusOK, resp)
	return
}

func (h *ginHandler) PublishToLocalQueue(c *gin.Context) {
	var req xcapi.PublishToLocalQueueRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		invalidRequestSchema(c)
		return
	}
	if len(req.GetMessages()) == 0 {
		invalidRequestSchema(c)
		return
	}

	var err *ErrorWithStatus
	h.logger.Debug("received PublishToLocalQueue API request", tag.Value(h.toJson(req)))
	defer func() {
		h.logger.Debug("responded PublishToLocalQueue API request", tag.Value(h.toJson(err)))
	}()

	err = h.svc.PublishToLocalQueue(c.Request.Context(), req)

	if err != nil {
		c.JSON(err.StatusCode, err.Error)
		return
	}

	c.JSON(http.StatusOK, struct{}{})
}

func (h *ginHandler) Rpc(c *gin.Context) {
	var req xcapi.ProcessExecutionRpcRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		invalidRequestSchema(c)
		return
	}

	if req.GetRpcName() == "" {
		invalidRequestSchema(c)
		return
	}

	var err *ErrorWithStatus
	h.logger.Debug("received Rpc API request", tag.Value(h.toJson(req)))
	defer func() {
		h.logger.Debug("responded Rpc API request", tag.Value(h.toJson(err)))
	}()

	resp, err := h.svc.Rpc(c.Request.Context(), req)

	if err != nil {
		c.JSON(err.StatusCode, err.Error)
		return
	}

	c.JSON(http.StatusOK, resp)
}

func (h *ginHandler) ListProcessExecutions(c *gin.Context) {
	var req xcapi.ListProcessExecutionsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		invalidRequestSchema(c)
		return
	}

	var resp *xcapi.ListProcessExecutionsResponse
	var errResp *ErrorWithStatus
	h.logger.Debug("received ListProcessExecutions API request", tag.Value(h.toJson(req)))
	defer func() {
		h.logger.Debug("responded ListProcessExecutions API request", tag.Value(h.toJson(resp)), tag.Value(h.toJson(errResp)))
	}()

	resp, errResp = h.svc.ListProcessExecutions(c.Request.Context(), req)

	if errResp != nil {
		c.JSON(errResp.StatusCode, errResp.Error)
		return
	}

	c.JSON(http.StatusOK, resp)
}

func (h *ginHandler) WaitForProcessCompletion(c *gin.Context) {
	var req xcapi.ProcessExecutionWaitForCompletionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		invalidRequestSchema(c)
		return
	}

	var errResp *ErrorWithStatus
	h.logger.Debug("received WaitForProcessCompletion API request", tag.Value(h.toJson(req)))
	defer func() {
		h.logger.Debug("responded WaitForProcessCompletion API request", tag.Value(h.toJson(errResp)))
	}()

	resp, errResp := h.svc.WaitForProcessCompletion(c.Request.Context(), req)

	if errResp != nil {
		c.JSON(errResp.StatusCode, errResp.Error)
		return
	}

	c.JSON(http.StatusOK, resp)
}

func (h *ginHandler) toJson(req any) string {
	str, err := json.Marshal(req)
	if err != nil {
		h.logger.Error("error when serializing request", tag.Error(err), tag.DefaultValue(req))
		return ""
	}
	return string(str)
}

func invalidRequestSchema(c *gin.Context) {
	c.JSON(http.StatusBadRequest, xcapi.ApiErrorResponse{
		Details: xcapi.PtrString("invalid request schema"),
	})
}
