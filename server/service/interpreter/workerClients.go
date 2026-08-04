// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

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
