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
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
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

func (logger *flowDefinitionWarningLogger) countWarningsContaining(fragment string) int {
	logger.mu.Lock()
	defer logger.mu.Unlock()
	count := 0
	for _, warning := range logger.warnings {
		if strings.Contains(warning, fragment) {
			count++
		}
	}
	return count
}

// slowUploadFlowService delays each UploadFlowDefinitions call until uploadDelay passes or the call is canceled.
type slowUploadFlowService struct {
	*workerSyncFlowService
	uploadDelay   time.Duration
	uploadCalls   atomic.Int32
	uploadStarted chan struct{}
}

func (service *slowUploadFlowService) UploadFlowDefinitions(
	ctx context.Context,
	request *dexpb.UploadFlowDefinitionsRequest,
) (*dexpb.UploadFlowDefinitionsResponse, error) {
	if service.uploadCalls.Add(1) == 1 && service.uploadStarted != nil {
		close(service.uploadStarted)
	}
	select {
	case <-time.After(service.uploadDelay):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	results := make([]*dexpb.FlowDefinitionUploadResult, 0, len(request.GetDefinitions()))
	for _, definition := range request.GetDefinitions() {
		results = append(results, &dexpb.FlowDefinitionUploadResult{
			FlowType: definition.GetFlowType(),
			Outcome:  dexpb.FlowDefinitionUploadOutcome_FLOW_DEFINITION_UPLOAD_OUTCOME_CREATED,
		})
	}
	return &dexpb.UploadFlowDefinitionsResponse{Results: results}, nil
}

func registeredFlowDefinitionDocument() FlowDefinitionDocument {
	flowType := GetFinalFlowType(workerFlow)
	return FlowDefinitionDocument{
		FlowType:    flowType,
		Content:     []byte(`{"schemaVersion":"2.0","flow":{"name":"` + flowType + `"}}`),
		GeneratedAt: time.Now(),
	}
}

// An older server answers UploadFlowDefinitions with UNIMPLEMENTED; Start must still open task streams.
func TestWorkerStartsWhenServerLacksFlowDefinitionUpload(t *testing.T) {
	flowService := &workerSyncFlowService{}
	flowServiceAddress := startWorkerFlowService(t, flowService, &workerStartupTaskService{flowService: flowService})
	logger := &flowDefinitionWarningLogger{}
	worker, err := newWorkerForTest(t, []Flow{workerFlow}, WorkerOptions{
		FlowServiceAddress: flowServiceAddress,
		Logger:             logger,
		FlowDefinitions:    []FlowDefinitionDocument{registeredFlowDefinitionDocument()},
	})
	require.NoError(t, err)
	startResult := make(chan error, 1)
	go func() { startResult <- worker.Start() }()
	require.Eventually(t, func() bool {
		return slices.Contains(flowService.recordedCalls(), "PollWorkerTask")
	}, 5*time.Second, time.Millisecond)
	require.Equal(t, 1, logger.countWarningsContaining("does not support Flow definition upload"))
	require.NoError(t, worker.Stop(context.Background()))
	require.NoError(t, <-startResult)
}

func TestWorkerStopCancelsInFlightFlowDefinitionUpload(t *testing.T) {
	syncService := &workerSyncFlowService{}
	flowService := &slowUploadFlowService{
		workerSyncFlowService: syncService, uploadDelay: time.Hour, uploadStarted: make(chan struct{}),
	}
	flowServiceAddress := startWorkerFlowService(t, flowService, &workerStartupTaskService{flowService: syncService})
	worker, err := newWorkerForTest(t, []Flow{workerFlow}, WorkerOptions{
		FlowServiceAddress: flowServiceAddress,
		Logger:             &flowDefinitionWarningLogger{},
		FlowDefinitions:    []FlowDefinitionDocument{registeredFlowDefinitionDocument()},
	})
	require.NoError(t, err)
	startResult := make(chan error, 1)
	go func() { startResult <- worker.Start() }()
	select {
	case <-flowService.uploadStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("Worker did not start uploading Flow definitions")
	}
	stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stoppedAt := time.Now()
	require.NoError(t, worker.Stop(stopCtx))
	require.Less(t, time.Since(stoppedAt), 5*time.Second)
	require.NoError(t, <-startResult)
}

func TestWorkerFlowDefinitionUploadUsesOneDeadlineForAllBatches(t *testing.T) {
	syncService := &workerSyncFlowService{}
	flowService := &slowUploadFlowService{workerSyncFlowService: syncService, uploadDelay: 400 * time.Millisecond}
	flowServiceAddress := startWorkerFlowService(t, flowService, &workerStartupTaskService{flowService: syncService})
	flows := make([]Flow, 0, 3*maxFlowDefinitionUploadBatchItems)
	documents := make([]FlowDefinitionDocument, 0, cap(flows))
	for index := range cap(flows) {
		flowType := fmt.Sprintf("BatchFlow%04d", index)
		flows = append(flows, &batchFlowDefinitionFlow{flowType: flowType})
		documents = append(documents, FlowDefinitionDocument{
			FlowType:    flowType,
			Content:     []byte(`{"schemaVersion":"2.0","flow":{"name":"` + flowType + `"}}`),
			GeneratedAt: time.Now(),
		})
	}
	logger := &flowDefinitionWarningLogger{}
	worker, err := newWorkerForTest(t, flows, WorkerOptions{
		FlowServiceAddress:        flowServiceAddress,
		Logger:                    logger,
		AttributeIndexSyncTimeout: time.Second,
		FlowDefinitions:           documents,
	})
	require.NoError(t, err)
	startResult := make(chan error, 1)
	go func() { startResult <- worker.Start() }()
	require.Eventually(t, func() bool {
		return slices.Contains(syncService.recordedCalls(), "PollWorkerTask")
	}, 5*time.Second, time.Millisecond)
	require.Equal(t, int32(3), flowService.uploadCalls.Load())
	require.Equal(t, 1, logger.countWarningsContaining("upload Flow definitions failed"))
	require.NoError(t, worker.Stop(context.Background()))
	require.NoError(t, <-startResult)
}

type batchFlowDefinitionFlow struct {
	FlowDefaults
	flowType string
}

func (flow *batchFlowDefinitionFlow) GetFlowType() string { return flow.flowType }

func (*batchFlowDefinitionFlow) GetPersistenceSchema() PersistenceSchema { return PersistenceSchema{} }

func (*batchFlowDefinitionFlow) GetSteps() []StepDef {
	return []StepDef{DefineStartStep(batchFlowDefinitionStep{})}
}

type batchFlowDefinitionStep struct {
	StepDefaultsNoWaitFor[string]
}

func (batchFlowDefinitionStep) Execute(_ Context, input string) (*StepDecision, error) {
	return GracefulComplete(input), nil
}

type flowDefinitionConformanceCases struct {
	SourcePathRewrite []struct {
		Name       string  `json:"name"`
		Cwd        *string `json:"cwd"`
		SourcePath string  `json:"sourcePath"`
		Expected   *string `json:"expected"`
	} `json:"sourcePathRewrite"`
	JSONStringEncoding []struct {
		Value   string `json:"value"`
		Encoded string `json:"encoded"`
	} `json:"jsonStringEncoding"`
	GeneratedAt []struct {
		Value      string `json:"value"`
		UnixMillis int64  `json:"unixMillis"`
		IsError    bool   `json:"error"`
	} `json:"generatedAt"`
	DocumentValidity []struct {
		Name             string `json:"name"`
		Document         string `json:"document"`
		IsValid          bool   `json:"valid"`
		IsGeneratedAtErr bool   `json:"generatedAtError"`
	} `json:"documentValidity"`
	DocumentRewrite []struct {
		Name     string  `json:"name"`
		Cwd      string  `json:"cwd"`
		Document string  `json:"document"`
		Expected *string `json:"expected"`
	} `json:"documentRewrite"`
}

// The conformance cases are pure functions shared by every SDK, so they are asserted directly.
func TestFlowDefinitionUploadConformance(t *testing.T) {
	fixture, err := os.ReadFile(filepath.Join("..", "..", "protos", "conformance", "flow_definition_upload.json"))
	require.NoError(t, err)
	var cases flowDefinitionConformanceCases
	require.NoError(t, json.Unmarshal(fixture, &cases))
	require.NotEmpty(t, cases.SourcePathRewrite)
	for _, testCase := range cases.SourcePathRewrite {
		workingDirectory := ""
		if testCase.Cwd != nil {
			workingDirectory = *testCase.Cwd
		}
		content := `{"schemaVersion":"2.0","source": {"path" : ` + string(encodeMinimalJSONString(testCase.SourcePath)) + `},"flow":{"name":"<A&B>"}}`
		rewritten := string(rewriteFlowDefinitionSourcePath([]byte(content), workingDirectory))
		if testCase.Expected == nil {
			require.Equal(t, content, rewritten, testCase.Name)
			continue
		}
		expected := `{"schemaVersion":"2.0","source": {"path" : ` + string(encodeMinimalJSONString(*testCase.Expected)) + `},"flow":{"name":"<A&B>"}}`
		require.Equal(t, expected, rewritten, testCase.Name)
	}
	require.NotEmpty(t, cases.JSONStringEncoding)
	for _, testCase := range cases.JSONStringEncoding {
		require.Equal(t, testCase.Encoded, string(encodeMinimalJSONString(testCase.Value)))
	}
	require.NotEmpty(t, cases.DocumentValidity)
	for _, testCase := range cases.DocumentValidity {
		_, err := parseFlowDefinitionHeader([]byte(testCase.Document))
		if testCase.IsValid {
			require.NoError(t, err, testCase.Name)
			continue
		}
		require.Error(t, err, testCase.Name)
		var notFlowDefinition *notFlowDefinitionError
		// A generatedAt error is a configuration error even in a directory, so it is not a skip.
		require.Equal(t, !testCase.IsGeneratedAtErr, errors.As(err, &notFlowDefinition), testCase.Name)
	}
	require.NotEmpty(t, cases.DocumentRewrite)
	for _, testCase := range cases.DocumentRewrite {
		rewritten := string(rewriteFlowDefinitionSourcePath([]byte(testCase.Document), testCase.Cwd))
		if testCase.Expected == nil {
			require.Equal(t, testCase.Document, rewritten, testCase.Name)
			continue
		}
		require.Equal(t, *testCase.Expected, rewritten, testCase.Name)
	}
	require.NotEmpty(t, cases.GeneratedAt)
	for _, testCase := range cases.GeneratedAt {
		content := `{"schemaVersion":"2.0","generatedAt":` + string(encodeMinimalJSONString(testCase.Value)) + `,"flow":{"name":"A"}}`
		filePath := filepath.Join(t.TempDir(), "flow.json")
		require.NoError(t, os.WriteFile(filePath, []byte(content), 0o600))
		documents, err := ReadFlowDefinitionFiles([]string{filePath}, &flowDefinitionWarningLogger{})
		if testCase.IsError {
			require.Error(t, err, testCase.Value)
			continue
		}
		require.NoError(t, err, testCase.Value)
		require.Equal(t, testCase.UnixMillis, documents[0].GeneratedAt.UnixMilli(), testCase.Value)
		require.Zero(t, documents[0].GeneratedAt.Nanosecond()%int(time.Millisecond), testCase.Value)
	}
}
