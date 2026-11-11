// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package dex

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type flowDefinitionWarningLogger struct {
	mu       sync.Mutex
	warnings []string
}

func (*flowDefinitionWarningLogger) Debug(string, ...interface{}) {}
func (*flowDefinitionWarningLogger) Info(string, ...interface{})  {}
func (*flowDefinitionWarningLogger) Error(string, ...interface{}) {}

func (logger *flowDefinitionWarningLogger) Warn(msg string, keyvals ...interface{}) {
	logger.mu.Lock()
	defer logger.mu.Unlock()
	logger.warnings = append(logger.warnings, fmt.Sprint(append([]interface{}{msg}, keyvals...)...))
}

func (logger *flowDefinitionWarningLogger) recordedWarnings() []string {
	logger.mu.Lock()
	defer logger.mu.Unlock()
	return append([]string(nil), logger.warnings...)
}

// An older server answers UploadFlowDefinitions with UNIMPLEMENTED; Start must still open task streams.
func TestWorkerStartsWhenServerLacksFlowDefinitionUpload(t *testing.T) {
	flowService := &workerSyncFlowService{}
	flowServiceAddress := startWorkerFlowService(t, flowService, &workerStartupTaskService{flowService: flowService})
	flowType := GetFinalFlowType(workerFlow)
	logger := &flowDefinitionWarningLogger{}
	worker, err := newWorkerForTest(t, []Flow{workerFlow}, WorkerOptions{
		FlowServiceAddress: flowServiceAddress,
		Logger:             logger,
		FlowDefinitions: []FlowDefinitionDocument{{
			FlowType:    flowType,
			Content:     []byte(`{"schemaVersion":"2.0","flow":{"name":"` + flowType + `"}}`),
			GeneratedAt: time.Now(),
		}},
	})
	require.NoError(t, err)
	startResult := make(chan error, 1)
	go func() { startResult <- worker.Start() }()
	require.Eventually(t, func() bool {
		return slices.Contains(flowService.recordedCalls(), "PollWorkerTask")
	}, 5*time.Second, time.Millisecond)
	warnings := logger.recordedWarnings()
	require.Len(t, warnings, 1)
	require.Contains(t, warnings[0], "does not support Flow definition upload")
	require.NoError(t, worker.Stop(context.Background()))
	require.NoError(t, <-startResult)
}

func TestRewriteFlowDefinitionSourcePathKeepsOtherBytes(t *testing.T) {
	content := []byte("{\"source\" : { \"language\":\"go\" ,\n \"path\" :\t\"/work/app/flows/a.go\" }, \"flow\":{\"name\":\"<A>\"}}")
	rewritten := rewriteFlowDefinitionSourcePath(content, "/work/app")
	require.Equal(t, "{\"source\" : { \"language\":\"go\" ,\n \"path\" :\t\"flows/a.go\" }, \"flow\":{\"name\":\"<A>\"}}", string(rewritten))
	require.Equal(t, `{"source":{"path":"a.go"}}`, string(rewriteFlowDefinitionSourcePath([]byte(`{"source":{"path":"/other/a.go"}}`), "/work/app")))
	relative := []byte(`{"source":{"path":"flows/a.go"}}`)
	require.Equal(t, relative, rewriteFlowDefinitionSourcePath(relative, "/work/app"))
	noSource := []byte(`{"flow":{"name":"A"},"nodes":[{"source":{"path":"/x"}}]}`)
	require.Equal(t, noSource, rewriteFlowDefinitionSourcePath(noSource, "/work/app"))
}
