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
	"database/sql"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/config"
	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/service"
	uclient "github.com/superdurable/dex/service/client"
	"github.com/superdurable/dex/service/common/ptr"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health/grpc_health_v1"
	"gopkg.in/yaml.v3"
)

type binaryDexProcess struct {
	command *exec.Cmd
	done    chan struct{}
	waitErr error
}

var binaryDexTerminationGracePeriod = 10 * time.Second

// serverZDEncodedValueOverheadBytes is the JSON obj_value framing that server-zd counts and the S3 threshold does not.
const serverZDEncodedValueOverheadBytes = 16

type temporalOnlyUnifiedClient struct {
	uclient.UnifiedClient
	test *testing.T
}

var errTemporalOnlyInBinaryMode = errors.New("Temporal-only operation in binary mode")

func startBinaryDexService(t *testing.T, testConfig DexServiceTestConfig) *integRuntime {
	t.Helper()
	requireTemporalOnlyScenario(t)
	require.Equal(t, "zd", *dexServerEngine, "dex-server-engine must be zd in binary mode")
	if testConfig.BackendType != service.BackendTypeTemporal {
		t.Skip("server-zd does not support Cadence")
	}
	require.Equal(t, "pull", *integWorkerMode, "binary mode requires pull Workers")
	configuration, unsupportedSetting := binaryDexConfig(testConfig)
	if unsupportedSetting != "" {
		t.Skipf("server-zd does not support %s", unsupportedSetting)
	}
	createAttributeSyncFencingTables(t, testConfig.AttributeStore.Stores)
	dataDirectory := t.TempDir()
	address, err := reserveBinaryDexAddress()
	require.NoError(t, err)
	configurationPath := filepath.Join(dataDirectory, "integ-config.yaml")
	configurationBytes, err := yaml.Marshal(configuration)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(configurationPath, configurationBytes, 0o600))
	arguments := []string{"dev", "--data-dir", dataDirectory, "--listen", address, "--config", configurationPath}
	outputPath := filepath.Join(dataDirectory, "server.log")
	outputFile, err := os.Create(outputPath)
	require.NoError(t, err)
	command := exec.Command(*dexServerBinary, arguments...)
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Stdout = outputFile
	command.Stderr = outputFile
	require.NoError(t, command.Start())
	process := &binaryDexProcess{command: command, done: make(chan struct{})}
	go func() {
		process.waitErr = command.Wait()
		close(process.done)
	}()
	t.Cleanup(func() {
		process.stop(t)
		require.NoError(t, outputFile.Close())
		if t.Failed() {
			output, readErr := os.ReadFile(outputPath)
			if readErr != nil {
				t.Logf("read Dex Server log: %v", readErr)
			} else {
				t.Logf("Dex Server log:\n%s", output)
			}
		}
	})
	require.NoError(t, waitForBinaryDex(address, process, 15*time.Second))
	var connection *grpc.ClientConn
	if testConfig.Credential != "" {
		connection = newDexClientConnectionWithCredential(t, address, testConfig.Credential, config.DefaultGrpcMaxMessageBytes)
	} else {
		connection, err = newDexClientConnection(address, config.DefaultGrpcMaxMessageBytes)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, connection.Close()) })
	}
	flowServiceClient := dexpb.NewFlowServiceClient(connection)
	// Each process starts empty; the Temporal dev server predefines these search attributes.
	registrationCtx, cancelRegistration := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelRegistration()
	_, err = flowServiceClient.SyncAttributeIndexes(
		registrationCtx,
		&dexpb.SyncAttributeIndexRequest{AttributeIndexes: localDexDevTestAttributeIndexes},
	)
	require.NoError(t, err)
	runtime := &integRuntime{
		PublicAddress: address,
		FlowClient: &externalDexFlowClient{
			FlowServiceClient: flowServiceClient,
			maxWaitSeconds:    int32(12),
		},
		defaultFlowConfig: &dexpb.FlowConfig{StepDurability: ptr.Any(dexpb.StepDurability_STEP_DURABILITY_SYNC)},
		UnifiedClient:     &temporalOnlyUnifiedClient{test: t},
	}
	if testConfig.MaxWaitSeconds > 0 {
		runtime.FlowClient.(*externalDexFlowClient).maxWaitSeconds = int32(testConfig.MaxWaitSeconds)
	}
	return configurePullWorkerClientWithCredential(t, runtime, testConfig.Credential)
}

func binaryDexConfig(testConfig DexServiceTestConfig) (map[string]any, string) {
	if testConfig.CadenceDomain != "" || testConfig.BackendType == service.BackendTypeCadence {
		return nil, "Cadence"
	}
	for _, store := range testConfig.AttributeStore.Stores {
		// C26: server-zd supports only PostgreSQL Attribute Stores.
		if store.Type != config.AttributeStoreTypePostgres {
			return nil, fmt.Sprintf("%s Attribute Store", store.Type)
		}
	}
	if testConfig.MemoEncryption {
		return nil, "memo encryption"
	}
	if len(testConfig.DefaultHeaders) > 0 {
		return nil, "default Worker headers"
	}
	if testConfig.S3KeyPrefix != "" {
		return nil, "S3 key prefix"
	}
	if testConfig.BlobCacheDirectory != "" {
		return nil, "Blob cache directory"
	}
	if testConfig.BlobStoreEnabled != nil && !*testConfig.BlobStoreEnabled {
		return nil, "disabled Blob Store"
	}
	if testConfig.AsyncStepInputSnapshotsEnabled != nil {
		return nil, "async Step input snapshots"
	}
	if testConfig.UseTemporalSynchronousUpdateForAllRPCs {
		return nil, "Temporal synchronous RPC updates"
	}
	if testConfig.TemporalMetricsHandler != nil {
		return nil, "Temporal metrics handler"
	}
	if streamStoreBackend := testConfig.StreamStore.EffectiveBackend(); streamStoreBackend != config.StreamStoreBackendMemory &&
		streamStoreBackend != config.StreamStoreBackendDisabled {
		return nil, "external Stream Store"
	}
	if testConfig.Internal != nil {
		return nil, "internal listener"
	}
	if testConfig.Wake != nil {
		return nil, "Worker wake hook"
	}
	if testConfig.UseBuiltInDefaultWorkflowConfig {
		return nil, "built-in default Flow configuration"
	}
	if *disableStickyCache {
		return nil, "disabled sticky cache"
	}
	configuration := make(map[string]any)
	apiConfig := make(map[string]any)
	if testConfig.MaxWaitSeconds > 0 {
		apiConfig["maxWaitSeconds"] = testConfig.MaxWaitSeconds
	}
	if testConfig.GrpcMaxMessageBytes > 0 {
		apiConfig["grpcMaxMessageBytes"] = testConfig.GrpcMaxMessageBytes
	}
	if len(apiConfig) > 0 {
		configuration["api"] = apiConfig
	}
	// server-zd keeps large values in its log; its wire threshold plays the Go Blob Store threshold's role (C5).
	if testConfig.LocalBlobThreshold > 0 {
		configuration["values"] = map[string]any{"wireInlineThresholdBytes": testConfig.LocalBlobThreshold}
	} else if testConfig.S3TestThreshold > 0 {
		configuration["values"] = map[string]any{
			"wireInlineThresholdBytes": testConfig.S3TestThreshold + serverZDEncodedValueOverheadBytes,
		}
	}
	if testConfig.LazyLoading != nil {
		valuesConfig, ok := configuration["values"].(map[string]any)
		if !ok {
			valuesConfig = make(map[string]any)
			configuration["values"] = valuesConfig
		}
		valuesConfig["hydrateBeforeDispatch"] = !*testConfig.LazyLoading
	}
	if testConfig.Matching != nil {
		matchingConfig := binaryMatchingConfig(testConfig.Matching)
		if len(matchingConfig) > 0 {
			configuration["matching"] = matchingConfig
		}
	}
	configuration["streamStore"] = binaryStreamStoreConfig(testConfig.StreamStore)
	if testConfig.Auth != nil {
		configuration["auth"] = binaryAuthConfig(testConfig.Auth)
	}
	if len(testConfig.AttributeStore.Stores) > 0 {
		configuration["attributeStore"] = binaryAttributeStoreConfig(testConfig.AttributeStore)
	}
	return configuration, ""
}

// binaryAttributeStoreConfig writes the Go Server's attributeStore keys, which server-zd reads unchanged.
func binaryAttributeStoreConfig(settings config.AttributeStoreConfig) map[string]any {
	stores := make(map[string]any, len(settings.Stores))
	for name, store := range settings.Stores {
		entry := map[string]any{"type": string(store.Type), "dsn": store.DSN, "tableName": store.TableName}
		for key, value := range map[string]string{
			"flowIdColumn": store.FlowIDColumn, "databaseName": store.DatabaseName,
			"collectionName": store.CollectionName, "rowKey": store.RowKey,
			"columnNaming": string(store.ColumnNaming),
		} {
			if value != "" {
				entry[key] = value
			}
		}
		if len(store.AttributeMaps) > 0 {
			attributeMaps := make(map[string]any, len(store.AttributeMaps))
			for mapName, mapTable := range store.AttributeMaps {
				attributeMaps[mapName] = map[string]any{
					"tableName": mapTable.TableName, "instanceKeyColumn": mapTable.InstanceKeyColumn,
				}
			}
			entry["attributeMaps"] = attributeMaps
		}
		stores[name] = entry
	}
	configuration := map[string]any{"stores": stores}
	for name, duration := range map[string]time.Duration{
		"schemaSyncInterval": settings.SchemaSyncInterval, "syncAttemptTimeout": settings.SyncAttemptTimeout,
	} {
		if duration != 0 {
			configuration[name] = duration.String()
		}
	}
	if settings.SyncBatchSize != 0 {
		configuration["syncBatchSize"] = settings.SyncBatchSize
	}
	if settings.SyncRetryPolicy != nil {
		configuration["syncRetryPolicy"] = binaryRetryPolicyConfig(settings.SyncRetryPolicy)
	}
	return configuration
}

func binaryRetryPolicyConfig(policy *config.RetryPolicy) map[string]any {
	configuration := make(map[string]any)
	for name, duration := range map[string]time.Duration{
		"initialInterval": policy.InitialInterval, "maximumInterval": policy.MaximumInterval,
		"totalDuration": policy.TotalDuration,
	} {
		if duration != 0 {
			configuration[name] = duration.String()
		}
	}
	if policy.BackoffCoefficient != 0 {
		configuration["backoffCoefficient"] = policy.BackoffCoefficient
	}
	if policy.MaximumAttempts != 0 {
		configuration["maximumAttempts"] = policy.MaximumAttempts
	}
	return configuration
}

// serverZDFencingTableDDL creates server-zd's default fencing table (C29); the Go Server needs none.
const serverZDFencingTableDDL = `CREATE TABLE IF NOT EXISTS dexzd_attribute_sync_fencing (
store_name text NOT NULL, cluster_id text NOT NULL, scope text NOT NULL, fencing_token bigint NOT NULL,
updated_at timestamptz NOT NULL, PRIMARY KEY (store_name, cluster_id, scope))`

// createAttributeSyncFencingTables needs no cleanup: each dev process is a new cluster, so rows never collide.
func createAttributeSyncFencingTables(t *testing.T, stores map[string]config.AttributeStoreConfigEntry) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	preparedDSNs := make(map[string]struct{}, len(stores))
	for _, store := range stores {
		if _, isPrepared := preparedDSNs[store.DSN]; isPrepared {
			continue
		}
		preparedDSNs[store.DSN] = struct{}{}
		database, err := sql.Open("pgx", store.DSN)
		require.NoError(t, err)
		_, err = database.ExecContext(ctx, serverZDFencingTableDDL)
		require.NoError(t, errors.Join(err, database.Close()))
	}
}

// binaryAuthConfig writes the Go Server's auth section with its own YAML keys, which server-zd reads unchanged.
func binaryAuthConfig(settings *config.AuthConfig) map[string]any {
	authenticators := make([]map[string]any, 0, len(settings.Authenticators))
	for _, authenticator := range settings.Authenticators {
		entry := map[string]any{"kind": string(authenticator.Kind)}
		for name, value := range map[string]string{
			"issuer": authenticator.Issuer, "jwksURL": authenticator.JWKSURL,
			"caFile": authenticator.CAFile, "file": authenticator.File,
		} {
			if value != "" {
				entry[name] = value
			}
		}
		if len(authenticator.Audiences) > 0 {
			entry["audiences"] = authenticator.Audiences
		}
		authenticators = append(authenticators, entry)
	}
	return map[string]any{"authenticators": authenticators}
}

// binaryMatchingConfig omits the local* settings: server-zd has no ASYNC local phase (C2).
func binaryMatchingConfig(settings *config.MatchingConfig) map[string]any {
	configuration := make(map[string]any)
	for name, duration := range map[string]time.Duration{
		"dispatchWait": settings.DispatchWait, "stickyWait": settings.StickyWait, "pingInterval": settings.PingInterval,
	} {
		if duration != 0 {
			configuration[name] = duration.String()
		}
	}
	for name, value := range map[string]int{
		"maxConcurrentTasksCap": settings.MaxConcurrentTasksCap, "maxStreams": settings.MaxStreams,
		"maxQueueLength": settings.MaxQueueLength, "maxFlowTypes": settings.MaxFlowTypes,
		"maxFlowTypeLength": settings.MaxFlowTypeLength,
	} {
		if value != 0 {
			configuration[name] = value
		}
	}
	return configuration
}

func binaryStreamStoreConfig(settings config.StreamStoreConfig) map[string]any {
	// server-zd dev enables memory Streams by default; the Go Server disables them.
	configuration := map[string]any{"backend": settings.EffectiveBackend()}
	for name, value := range map[string]int64{
		"maxMessageBytes":               settings.MaxMessageBytes,
		"estimatedMessageOverheadBytes": settings.EstimatedMessageOverheadBytes,
	} {
		if value != 0 {
			configuration[name] = value
		}
	}
	for name, value := range map[string]int32{
		"maxReadMessages":    settings.MaxReadMessages,
		"trimTriggerPercent": settings.TrimTriggerPercent,
		"trimTargetPercent":  settings.TrimTargetPercent,
	} {
		if value != 0 {
			configuration[name] = value
		}
	}
	if settings.BackgroundTrimBatchSize != 0 {
		configuration["backgroundTrimBatchSize"] = settings.BackgroundTrimBatchSize
	}
	if settings.TrimWorkers != 0 {
		configuration["trimWorkers"] = settings.TrimWorkers
	}
	for name, duration := range map[string]time.Duration{
		"trimLeaseTTL": settings.TrimLeaseTTL, "trimLeaseRetry": settings.TrimLeaseRetry,
		"trimBatchYieldTime": settings.TrimBatchYieldTime,
	} {
		if duration != 0 {
			configuration[name] = duration.String()
		}
	}
	return configuration
}

func reserveBinaryDexAddress() (string, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	address := listener.Addr().String()
	return address, listener.Close()
}

func waitForBinaryDex(address string, process *binaryDexProcess, timeout time.Duration) (waitErr error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	connection, err := newDexClientConnection(address, config.DefaultGrpcMaxMessageBytes)
	if err != nil {
		return err
	}
	defer func() { waitErr = errors.Join(waitErr, connection.Close()) }()
	client := grpc_health_v1.NewHealthClient(connection)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	var lastErr error
	for {
		checkCtx, cancelCheck := context.WithTimeout(ctx, time.Second)
		response, checkErr := client.Check(checkCtx, &grpc_health_v1.HealthCheckRequest{Service: ""})
		cancelCheck()
		if checkErr == nil && response.GetStatus() == grpc_health_v1.HealthCheckResponse_SERVING {
			return nil
		}
		if checkErr != nil {
			lastErr = checkErr
		} else {
			lastErr = fmt.Errorf("health status is %s", response.GetStatus())
		}
		select {
		case <-process.done:
			return fmt.Errorf("Dex Server exited before serving: %w", process.waitErr)
		case <-ctx.Done():
			return fmt.Errorf("Dex Server readiness timed out: %w", errors.Join(ctx.Err(), lastErr))
		case <-ticker.C:
		}
	}
}

func (process *binaryDexProcess) stop(t *testing.T) {
	t.Helper()
	select {
	case <-process.done:
		return
	default:
	}
	if err := syscall.Kill(-process.command.Process.Pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		t.Errorf("terminate Dex Server: %v", err)
	}
	select {
	case <-process.done:
	case <-time.After(binaryDexTerminationGracePeriod):
		if err := syscall.Kill(-process.command.Process.Pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
			t.Errorf("kill Dex Server: %v", err)
		}
		<-process.done
	}
}

func requireGoServerSetting(t *testing.T, setting string) {
	t.Helper()
	if *dexServerBinary != "" {
		t.Skipf("server-zd does not support %s", setting)
	}
}

func isServerZDBinaryMode() bool {
	return *dexServerBinary != ""
}

// describeFlowStatus reads the current run's status from Temporal, or through GetFlowSummary in binary mode.
func describeFlowStatus(t *testing.T, ctx context.Context, runtime *integRuntime, flowID string) dexpb.FlowStatus {
	t.Helper()
	if isServerZDBinaryMode() {
		summary, err := runtime.FlowClient.GetFlowSummary(ctx, &dexpb.GetFlowSummaryRequest{FlowId: flowID})
		require.NoError(t, err)
		return summary.GetFlowStatus()
	}
	description, err := runtime.UnifiedClient.DescribeWorkflowExecution(ctx, flowID, "", nil)
	require.NoError(t, err)
	return description.Status
}

// describeFlowRunID returns the current run ID from Temporal, or through GetFlowSummary in binary mode.
func describeFlowRunID(t *testing.T, ctx context.Context, runtime *integRuntime, flowID string) string {
	t.Helper()
	if isServerZDBinaryMode() {
		summary, err := runtime.FlowClient.GetFlowSummary(ctx, &dexpb.GetFlowSummaryRequest{FlowId: flowID})
		require.NoError(t, err)
		return summary.GetFlowExecutionId().GetRunId()
	}
	description, err := runtime.UnifiedClient.DescribeWorkflowExecution(ctx, flowID, "", nil)
	require.NoError(t, err)
	return description.RunId
}

// readFlowConfig returns the run's FlowConfig from the Temporal debug dump, or through GetFlowState in binary mode.
func readFlowConfig(ctx context.Context, runtime *integRuntime, flowID string) (*dexpb.FlowConfig, error) {
	if isServerZDBinaryMode() {
		state, err := runtime.FlowClient.GetFlowState(ctx, &dexpb.GetFlowStateRequest{FlowId: flowID})
		return state.GetFlowConfig(), err
	}
	var dump dexpb.DebugDumpResponse
	err := runtime.UnifiedClient.QueryWorkflow(ctx, &dump, flowID, "", service.DebugDumpQueryType)
	return dump.GetConfig(), err
}

func requireTemporalOnly(t *testing.T) {
	t.Helper()
	if *dexServerBinary != "" {
		t.Skip("Temporal-only assertion")
	}
}

func (client *temporalOnlyUnifiedClient) unsupported(method string) error {
	client.reportUnsupported(method)
	return fmt.Errorf("%w: %s", errTemporalOnlyInBinaryMode, method)
}

func (client *temporalOnlyUnifiedClient) reportUnsupported(method string) {
	client.test.Errorf("missing requireTemporalOnly: %s", method)
}

func (client *temporalOnlyUnifiedClient) QueryWorkflow(context.Context, interface{}, string, string, string, ...interface{}) error {
	return client.unsupported("QueryWorkflow")
}

func (client *temporalOnlyUnifiedClient) DescribeWorkflowExecution(context.Context, string, string, map[string]dexpb.IndexType) (*uclient.DescribeWorkflowExecutionResponse, error) {
	return nil, client.unsupported("DescribeWorkflowExecution")
}

func (client *temporalOnlyUnifiedClient) GetWorkflowHistory(context.Context, *uclient.GetWorkflowHistoryRequest) (*uclient.WorkflowHistory, error) {
	return nil, client.unsupported("GetWorkflowHistory")
}

func (client *temporalOnlyUnifiedClient) GetBackendType() service.BackendType {
	client.reportUnsupported("GetBackendType")
	return ""
}

func (client *temporalOnlyUnifiedClient) InterpreterWorkflowType() string {
	client.reportUnsupported("InterpreterWorkflowType")
	return ""
}

func (client *temporalOnlyUnifiedClient) GetApiService() interface{} {
	client.reportUnsupported("GetApiService")
	return nil
}

func (client *temporalOnlyUnifiedClient) StartBlobStoreCleanupWorkflow(context.Context, string, string, string, string) error {
	return client.unsupported("StartBlobStoreCleanupWorkflow")
}

func (client *temporalOnlyUnifiedClient) GetWorkflowResult(context.Context, interface{}, string, string) (dexpb.FlowStatus, error) {
	return dexpb.FlowStatus_FLOW_STATUS_UNSPECIFIED, client.unsupported("GetWorkflowResult")
}

func (client *temporalOnlyUnifiedClient) StartInterpreterWorkflow(context.Context, uclient.StartWorkflowOptions, ...interface{}) (string, error) {
	return "", client.unsupported("StartInterpreterWorkflow")
}

func (client *temporalOnlyUnifiedClient) SignalWorkflow(context.Context, string, string, string, interface{}) error {
	return client.unsupported("SignalWorkflow")
}

func (client *temporalOnlyUnifiedClient) TerminateWorkflow(context.Context, string, string, string) error {
	return client.unsupported("TerminateWorkflow")
}

func (client *temporalOnlyUnifiedClient) SynchronousUpdateWorkflow(context.Context, interface{}, string, string, string, string, interface{}) error {
	return client.unsupported("SynchronousUpdateWorkflow")
}

func (client *temporalOnlyUnifiedClient) ListAttributeIndexes(context.Context) (map[string]dexpb.IndexType, error) {
	return nil, client.unsupported("ListAttributeIndexes")
}

func (client *temporalOnlyUnifiedClient) NormalizeAttributeIndexType(dexpb.IndexType) dexpb.IndexType {
	client.reportUnsupported("NormalizeAttributeIndexType")
	return dexpb.IndexType_INDEX_TYPE_UNSPECIFIED
}
