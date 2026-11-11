// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package integ

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/dex"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type flowDefinitionUploadFlow struct {
	emptyFlowSchema
	flowType string
}

func (flow flowDefinitionUploadFlow) GetFlowType() string { return flow.flowType }

func (flowDefinitionUploadFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(flowDefinitionUploadStep{})}
}

type flowDefinitionUploadStep struct {
	dex.StepDefaultsNoWaitFor[string]
}

func (flowDefinitionUploadStep) Execute(_ dex.Context, input string) (*dex.StepDecision, error) {
	return dex.GracefulComplete(input), nil
}

type warningRecordingLogger struct {
	mu       sync.Mutex
	warnings []string
}

func (*warningRecordingLogger) Debug(string, ...interface{}) {}
func (*warningRecordingLogger) Info(string, ...interface{})  {}
func (*warningRecordingLogger) Error(string, ...interface{}) {}

func (logger *warningRecordingLogger) Warn(msg string, keyvals ...interface{}) {
	logger.mu.Lock()
	defer logger.mu.Unlock()
	logger.warnings = append(logger.warnings, fmt.Sprint(append([]interface{}{msg}, keyvals...)...))
}

func (logger *warningRecordingLogger) recordedWarnings() []string {
	logger.mu.Lock()
	defer logger.mu.Unlock()
	return append([]string(nil), logger.warnings...)
}

func TestWorkerUploadsFlowDefinitionsFromPathsAndMemory(t *testing.T) {
	workingDirectory, err := os.Getwd()
	require.NoError(t, err)
	fileFlowType := "FileFlowDefinition-" + uuid.NewString()
	memoryFlowType := "MemoryFlowDefinition-" + uuid.NewString()
	unregisteredFlowType := "UnregisteredFlowDefinition-" + uuid.NewString()

	definitionDirectory := t.TempDir()
	nestedDirectory := filepath.Join(definitionDirectory, "nested")
	require.NoError(t, os.Mkdir(nestedDirectory, 0o755))
	underWorkingDirectory := filepath.Join(workingDirectory, "workflows", "order.go")
	fileContent := flowDefinitionJSON(fileFlowType, underWorkingDirectory)
	filePath := filepath.Join(nestedDirectory, "file-flow.json")
	require.NoError(t, os.WriteFile(filePath, []byte(fileContent), 0o600))
	fileModificationTime := time.Date(2026, 1, 2, 3, 4, 5, 123456000, time.UTC)
	require.NoError(t, os.Chtimes(filePath, fileModificationTime, fileModificationTime))
	require.NoError(t, os.WriteFile(
		filepath.Join(definitionDirectory, "unregistered.json"),
		[]byte(flowDefinitionJSON(unregisteredFlowType, "/elsewhere/unregistered.go")),
		0o600,
	))
	require.NoError(t, os.WriteFile(filepath.Join(definitionDirectory, "notes.txt"), []byte("not JSON"), 0o600))

	memoryContent := flowDefinitionJSON(memoryFlowType, "/outside/the/working/directory/memory.go")
	memoryGeneratedAt := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	worker := startFlowDefinitionWorker(t, []dex.Flow{
		flowDefinitionUploadFlow{flowType: fileFlowType},
		flowDefinitionUploadFlow{flowType: memoryFlowType},
	}, dex.WorkerOptions{
		FlowDefinitionPaths: []string{definitionDirectory, filePath},
		FlowDefinitions: []dex.FlowDefinitionDocument{{
			FlowType: memoryFlowType, Content: []byte(memoryContent), GeneratedAt: memoryGeneratedAt,
		}},
	}, nil)
	require.NoError(t, worker.WaitPullRegistered(integrationContext(t)))

	stored := getStoredFlowDefinitions(t, fileFlowType, memoryFlowType, unregisteredFlowType)
	require.Len(t, stored, 2)
	require.Contains(t, stored, fileFlowType)
	require.Contains(t, stored, memoryFlowType)
	require.NotContains(t, stored, unregisteredFlowType)

	expectedFileContent := strings.Replace(fileContent, underWorkingDirectory, "workflows/order.go", 1)
	require.Equal(t, expectedFileContent, string(stored[fileFlowType].GetContent()))
	require.True(t, fileModificationTime.Equal(stored[fileFlowType].GetInfo().GetGeneratedAt().AsTime()))
	require.True(t, strings.HasPrefix(stored[fileFlowType].GetInfo().GetUploader(), "sdk-go/"))

	expectedMemoryContent := strings.Replace(memoryContent, "/outside/the/working/directory/memory.go", "memory.go", 1)
	require.Equal(t, expectedMemoryContent, string(stored[memoryFlowType].GetContent()))
	require.True(t, memoryGeneratedAt.Equal(stored[memoryFlowType].GetInfo().GetGeneratedAt().AsTime()))
}

func TestWorkerFlowDefinitionTopLevelGeneratedAtWins(t *testing.T) {
	flowType := "GeneratedAtFlowDefinition-" + uuid.NewString()
	content := `{"schemaVersion":"2.0","generatedAt":"2026-05-06T07:08:09.25Z","flow":{"name":"` + flowType + `"}}`
	filePath := filepath.Join(t.TempDir(), "flow.json")
	require.NoError(t, os.WriteFile(filePath, []byte(content), 0o600))
	worker := startFlowDefinitionWorker(t, []dex.Flow{flowDefinitionUploadFlow{flowType: flowType}},
		dex.WorkerOptions{FlowDefinitionPaths: []string{filePath}}, nil)
	require.NoError(t, worker.WaitPullRegistered(integrationContext(t)))

	stored := getStoredFlowDefinitions(t, flowType)
	require.Equal(t, content, string(stored[flowType].GetContent()))
	expected := time.Date(2026, 5, 6, 7, 8, 9, 250000000, time.UTC)
	require.True(t, expected.Equal(stored[flowType].GetInfo().GetGeneratedAt().AsTime()))
}

func TestNewWorkerRejectsFlowTypeFromTwoFlowDefinitionSources(t *testing.T) {
	flowType := "DuplicateFlowDefinition-" + uuid.NewString()
	filePath := filepath.Join(t.TempDir(), "flow.json")
	require.NoError(t, os.WriteFile(filePath, []byte(flowDefinitionJSON(flowType, "flow.go")), 0o600))
	registry, err := dex.NewRegistry([]dex.Flow{flowDefinitionUploadFlow{flowType: flowType}})
	require.NoError(t, err)
	_, err = dex.NewWorker(registry, newPullSDKCache(t), dex.WorkerOptions{
		FlowServiceAddress:  flowServiceAddress(),
		FlowDefinitionPaths: []string{filePath},
		FlowDefinitions: []dex.FlowDefinitionDocument{{
			FlowType: flowType, Content: []byte(flowDefinitionJSON(flowType, "flow.go")), GeneratedAt: time.Now(),
		}},
	})
	require.ErrorContains(t, err, "appears in both WorkerOptions.FlowDefinitionPaths and WorkerOptions.FlowDefinitions")

	secondFilePath := filepath.Join(t.TempDir(), "copy.json")
	require.NoError(t, os.WriteFile(secondFilePath, []byte(flowDefinitionJSON(flowType, "flow.go")), 0o600))
	_, err = dex.NewWorker(registry, newPullSDKCache(t), dex.WorkerOptions{
		FlowServiceAddress:  flowServiceAddress(),
		FlowDefinitionPaths: []string{filePath, secondFilePath},
	})
	require.ErrorContains(t, err, "both describe Flow type")
}

func TestWorkerKeepsRunningWhenFlowDefinitionUploadFails(t *testing.T) {
	flowType := "RejectedFlowDefinition-" + uuid.NewString()
	flow := flowDefinitionUploadFlow{flowType: flowType}
	logger := &warningRecordingLogger{}
	worker := startFlowDefinitionWorker(t, []dex.Flow{flow}, dex.WorkerOptions{
		FlowDefinitions: []dex.FlowDefinitionDocument{{
			FlowType:    flowType,
			Content:     []byte(flowDefinitionJSON(flowType, "flow.go")),
			GeneratedAt: time.Now().Add(48 * time.Hour),
		}},
	}, logger)
	ctx := integrationContext(t)
	require.NoError(t, worker.WaitPullRegistered(ctx))
	require.Empty(t, getStoredFlowDefinitions(t, flowType))
	require.Equal(t, 1, countWarningsContaining(logger.recordedWarnings(), "upload Flow definitions failed"))

	registry, err := dex.NewRegistry([]dex.Flow{flow})
	require.NoError(t, err)
	client, err := dex.NewClient(registry, newPullSDKCache(t), dex.ClientOptions{FlowServiceAddress: flowServiceAddress()})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	flowID := newFlowID(t, "rejected-flow-definition")
	_, err = client.StartFlow(ctx, flow, flowID, "done", dex.StartFlowOptions{})
	require.NoError(t, err)
	result, err := client.WaitForFlow(ctx, flowID, dex.WaitForFlowOptions{})
	require.NoError(t, err)
	require.Equal(t, dex.FlowCompleted, result.Status)
}

func TestClientUploadFlowDefinitionsReportsOutcomes(t *testing.T) {
	flowType := "ClientFlowDefinition-" + uuid.NewString()
	registry, err := dex.NewRegistry(nil)
	require.NoError(t, err)
	client, err := dex.NewClient(registry, newPullSDKCache(t), dex.ClientOptions{FlowServiceAddress: flowServiceAddress()})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	ctx := integrationContext(t)
	olderTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	newerTime := olderTime.Add(time.Hour)
	upload := func(path string, generatedAt time.Time, isForced bool) dex.FlowDefinitionUploadResult {
		results, err := client.UploadFlowDefinitions(ctx, []dex.FlowDefinitionDocument{{
			FlowType: flowType, Content: []byte(flowDefinitionJSON(flowType, path)), GeneratedAt: generatedAt,
		}}, isForced)
		require.NoError(t, err)
		require.Len(t, results, 1)
		require.Equal(t, flowType, results[0].FlowType)
		return results[0]
	}
	require.Equal(t, dex.FlowDefinitionUploadCreated, upload("a.go", olderTime, false).Outcome)
	require.Equal(t, dex.FlowDefinitionUploadUnchanged, upload("a.go", olderTime, false).Outcome)
	require.Equal(t, dex.FlowDefinitionUploadReplaced, upload("b.go", newerTime, false).Outcome)
	kept := upload("c.go", olderTime, false)
	require.Equal(t, dex.FlowDefinitionUploadKeptExisting, kept.Outcome)
	require.True(t, newerTime.Equal(kept.StoredGeneratedAt))
	require.Equal(t, dex.FlowDefinitionUploadReplaced, upload("c.go", olderTime, true).Outcome)

	_, err = client.UploadFlowDefinitions(ctx, []dex.FlowDefinitionDocument{{
		FlowType: flowType, Content: []byte(flowDefinitionJSON("OtherFlow", "a.go")), GeneratedAt: olderTime,
	}}, false)
	require.ErrorContains(t, err, `has flow.name "OtherFlow"`)
}

func countWarningsContaining(warnings []string, fragment string) int {
	count := 0
	for _, warning := range warnings {
		if strings.Contains(warning, fragment) {
			count++
		}
	}
	return count
}

func startFlowDefinitionWorker(t *testing.T, flows []dex.Flow, options dex.WorkerOptions, logger dex.Logger) *dex.Worker {
	t.Helper()
	registry, err := dex.NewRegistry(flows)
	require.NoError(t, err)
	options.FlowServiceAddress = flowServiceAddress()
	options.Pull = &dex.PullWorkerOptions{AppWorkerID: "flow-definition-" + uuid.NewString()}
	options.Logger = logger
	worker, err := dex.NewWorker(registry, newPullSDKCache(t), options)
	require.NoError(t, err)
	result := make(chan error, 1)
	go func() { result <- worker.Start() }()
	t.Cleanup(func() {
		require.NoError(t, worker.Stop(context.Background()))
		require.NoError(t, <-result)
	})
	return worker
}

func getStoredFlowDefinitions(t *testing.T, flowTypes ...string) map[string]*dexpb.FlowDefinition {
	t.Helper()
	connection, err := grpc.NewClient(flowServiceAddress(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, connection.Close()) })
	response, err := dexpb.NewFlowServiceClient(connection).GetFlowDefinitions(
		integrationContext(t),
		&dexpb.GetFlowDefinitionsRequest{FlowTypes: flowTypes},
	)
	require.NoError(t, err)
	stored := make(map[string]*dexpb.FlowDefinition, len(response.GetDefinitions()))
	for _, definition := range response.GetDefinitions() {
		stored[definition.GetInfo().GetFlowType()] = definition
	}
	return stored
}

// flowDefinitionJSON keeps keys in a non-sorted order so the test detects re-encoding.
func flowDefinitionJSON(flowType string, sourcePath string) string {
	return fmt.Sprintf(
		"{\n  \"schemaVersion\": \"2.0\",\n  \"valid\": true,\n  \"source\": {\"language\": \"go\", \"path\": %q},\n"+
			"  \"flow\": {\"name\": %q, \"note\": \"<&>\"},\n  \"nodes\": []\n}\n",
		sourcePath, flowType,
	)
}
