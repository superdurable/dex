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

package interpreter

import (
	"fmt"

	"github.com/superdurable/dex/config"
	"github.com/superdurable/dex/service/common/workerclient"
)

// NewInternalServiceClient builds the continue-as-new dump client.
func NewInternalServiceClient(cfg *config.Config) *workerclient.InternalServiceClient {
	if cfg == nil {
		panic("NewInternalServiceClient requires non-nil config sections")
	}
	internalService, err := workerclient.NewInternalServiceClient(
		internalServiceTarget(&cfg.Api, &cfg.Interpreter.InterpreterActivityConfig),
		cfg,
	)
	if err != nil {
		panic(fmt.Sprintf("create internal service client: %v", err))
	}
	return internalService
}

func internalServiceTarget(
	apiCfg *config.ApiConfig,
	activityCfg *config.InterpreterActivityConfig,
) string {
	if activityCfg.InternalServiceTarget != "" {
		return activityCfg.InternalServiceTarget
	}
	port := apiCfg.Port
	if port == 0 {
		port = config.DefaultApiPort
	}
	return fmt.Sprintf("localhost:%d", port)
}
