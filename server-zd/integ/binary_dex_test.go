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
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/dex/ptr"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"github.com/superdurable/dex/server-zd/integ/internal/testconfig"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

func TestBinaryDexLauncher(t *testing.T) {
	executable, err := os.Executable()
	require.NoError(t, err)
	previousBinary := *dexServerBinary
	*dexServerBinary = executable
	t.Cleanup(func() {
		*dexServerBinary = previousBinary
	})
	t.Setenv("DEX_INTEG_FAKE_BINARY", "1")
	receiptPath := t.TempDir() + "/arguments"
	t.Setenv("DEX_INTEG_FAKE_RECEIPT", receiptPath)
	runtime := startBinaryDexService(t, serverZDTestConfig{
		MaxWaitSeconds: 7, GrpcMaxMessageBytes: 8192,
	})
	require.NotEmpty(t, runtime.PublicAddress)
	receiptBytes, err := os.ReadFile(receiptPath)
	require.NoError(t, err)
	arguments := strings.Split(string(receiptBytes), "\n")
	require.Equal(t, "dev", arguments[0])
	require.Contains(t, arguments, "--data-dir")
	require.Contains(t, arguments, "--listen")
	configIndex := -1
	for index, argument := range arguments {
		if argument == "--config" {
			configIndex = index
		}
	}
	require.Greater(t, configIndex, 0)
	configurationBytes, err := os.ReadFile(arguments[configIndex+1])
	require.NoError(t, err)
	require.Contains(t, string(configurationBytes), "maxWaitSeconds: 7")
	require.Contains(t, string(configurationBytes), "grpcMaxMessageBytes: 8192")
}

func TestBinaryDexConfigMapping(t *testing.T) {
	configuration, unsupportedSetting := binaryDexConfig(serverZDTestConfig{

		ObjectPayloadThresholdBytes: 256,
		LazyLoading:                 ptr.Any(false),
		Matching:                    &testconfig.MatchingConfig{DispatchWait: 2 * time.Second, MaxStreams: 16},
		StreamStore:                 testconfig.StreamStoreConfig{Backend: testconfig.StreamStoreBackendMemory, MaxReadMessages: 25},

		ServerZDMaxLongPollWaiters: 1,
	})
	require.Empty(t, unsupportedSetting)
	require.Equal(t, map[string]any{"maxLongPollWaitersPerNode": 1, "maxLongPollWaitersPerFlow": 1}, configuration["limits"])
	require.Equal(t, map[string]any{"wireInlineThresholdBytes": 268, "hydrateBeforeDispatch": true}, configuration["values"])
	require.Equal(t, map[string]any{"dispatchWait": "2s", "maxStreams": 16}, configuration["matching"])
	require.Equal(t, map[string]any{"backend": testconfig.StreamStoreBackendMemory, "maxReadMessages": int32(25)}, configuration["streamStore"])
}

func TestBinaryDexConfigAttributeStoreMapping(t *testing.T) {
	configuration, unsupportedSetting := binaryDexConfig(serverZDTestConfig{

		AttributeStore: testconfig.AttributeStoreConfig{
			Stores: map[string]testconfig.AttributeStoreConfigEntry{
				"groups": {
					Type: testconfig.AttributeStoreTypePostgres, DSN: "postgres://dex@localhost/dex", TableName: "public.groups",
					RowKey: "group-{group_id}", ColumnNaming: testconfig.AttributeStoreColumnNamingSnakeCase,
					AttributeMaps: map[string]testconfig.AttributeStoreMapTableConfig{
						"Members": {TableName: "public.members", InstanceKeyColumn: "account_id"},
					},
				},
			},
			SyncBatchSize:      2,
			SyncAttemptTimeout: time.Second,
			SyncRetryPolicy: &testconfig.RetryPolicy{
				InitialInterval: 100 * time.Millisecond, BackoffCoefficient: 1.5, TotalDuration: 168 * time.Hour,
			},
		},
	})
	require.Empty(t, unsupportedSetting)
	require.Equal(t, map[string]any{
		"stores": map[string]any{
			"groups": map[string]any{
				"type": "postgres", "dsn": "postgres://dex@localhost/dex", "tableName": "public.groups",
				"rowKey": "group-{group_id}", "columnNaming": "snake_case",
				"attributeMaps": map[string]any{
					"Members": map[string]any{"tableName": "public.members", "instanceKeyColumn": "account_id"},
				},
			},
		},
		"syncBatchSize":      2,
		"syncAttemptTimeout": "1s",
		"syncRetryPolicy": map[string]any{
			"initialInterval": "100ms", "backoffCoefficient": 1.5, "totalDuration": "168h0m0s",
		},
	}, configuration["attributeStore"])

	_, unsupportedSetting = binaryDexConfig(serverZDTestConfig{

		AttributeStore: testconfig.AttributeStoreConfig{
			Stores: map[string]testconfig.AttributeStoreConfigEntry{"documents": {Type: testconfig.AttributeStoreTypeMongoDB}},
		},
	})
	require.Equal(t, "mongodb Attribute Store", unsupportedSetting)
}

func TestBinaryDexLauncherEarlyExit(t *testing.T) {
	address, process := startFakeBinaryDexProcess(t, "exit")
	err := waitForBinaryDex(address, process, time.Second)
	require.ErrorContains(t, err, "exited before serving")
}

func TestBinaryDexLauncherReadinessTimeout(t *testing.T) {
	address, process := startFakeBinaryDexProcess(t, "not-serving")
	err := waitForBinaryDex(address, process, 3*time.Second)
	require.ErrorContains(t, err, "readiness timed out")
}

func TestBinaryDexLauncherKillsUnresponsiveProcess(t *testing.T) {
	address, process := startFakeBinaryDexProcess(t, "ignore-sigterm")
	previousGracePeriod := binaryDexTerminationGracePeriod
	binaryDexTerminationGracePeriod = 50 * time.Millisecond
	t.Cleanup(func() { binaryDexTerminationGracePeriod = previousGracePeriod })
	require.NoError(t, waitForBinaryDex(address, process, 5*time.Second))
	process.stop(t)
	select {
	case <-process.done:
	case <-time.After(time.Second):
		t.Fatal("fake Dex Server was not reaped")
	}
	require.Error(t, process.waitErr)
}

func startFakeBinaryDexProcess(t *testing.T, mode string) (string, *binaryDexProcess) {
	t.Helper()
	executable, err := os.Executable()
	require.NoError(t, err)
	address, err := reserveBinaryDexAddress()
	require.NoError(t, err)
	command := exec.Command(executable, "dev", "--data-dir", t.TempDir(), "--listen", address)
	command.Env = append(os.Environ(), "DEX_INTEG_FAKE_BINARY=1", "DEX_INTEG_FAKE_MODE="+mode,
		"DEX_INTEG_FAKE_RECEIPT="+t.TempDir()+"/arguments")
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	require.NoError(t, command.Start())
	process := &binaryDexProcess{command: command, done: make(chan struct{})}
	go func() {
		process.waitErr = command.Wait()
		close(process.done)
	}()
	t.Cleanup(func() { process.stop(t) })
	return address, process
}

func TestBinaryDexLauncherDisablesStreamsByDefault(t *testing.T) {
	executable, err := os.Executable()
	require.NoError(t, err)
	previousBinary := *dexServerBinary
	*dexServerBinary = executable
	t.Cleanup(func() {
		*dexServerBinary = previousBinary
	})
	t.Setenv("DEX_INTEG_FAKE_BINARY", "1")
	receiptPath := t.TempDir() + "/arguments"
	t.Setenv("DEX_INTEG_FAKE_RECEIPT", receiptPath)
	startBinaryDexService(t, serverZDTestConfig{})
	receiptBytes, err := os.ReadFile(receiptPath)
	require.NoError(t, err)
	arguments := strings.Split(string(receiptBytes), "\n")
	require.Equal(t, "--config", arguments[len(arguments)-2])
	configurationBytes, err := os.ReadFile(arguments[len(arguments)-1])
	require.NoError(t, err)
	require.Equal(t, "streamStore:\n    backend: disabled\n", string(configurationBytes))
}

func runFakeBinaryDex() int {
	arguments := os.Args[1:]
	if len(arguments) < 5 || arguments[0] != "dev" {
		return 2
	}
	address := ""
	for index := 0; index+1 < len(arguments); index++ {
		if arguments[index] == "--listen" {
			address = arguments[index+1]
		}
	}
	if address == "" {
		return 2
	}
	if err := os.WriteFile(os.Getenv("DEX_INTEG_FAKE_RECEIPT"), []byte(strings.Join(arguments, "\n")), 0o600); err != nil {
		return 3
	}
	if os.Getenv("DEX_INTEG_FAKE_MODE") == "exit" {
		return 6
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return 4
	}
	server := grpc.NewServer()
	healthServer := health.NewServer()
	healthStatus := healthpb.HealthCheckResponse_SERVING
	if os.Getenv("DEX_INTEG_FAKE_MODE") == "not-serving" {
		healthStatus = healthpb.HealthCheckResponse_NOT_SERVING
	}
	healthServer.SetServingStatus("", healthStatus)
	healthpb.RegisterHealthServer(server, healthServer)
	dexpb.RegisterFlowServiceServer(server, &fakeBinaryFlowService{})
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM)
	go func() {
		<-signals
		if os.Getenv("DEX_INTEG_FAKE_MODE") != "ignore-sigterm" {
			server.GracefulStop()
		}
	}()
	if err := server.Serve(listener); err != nil {
		return 5
	}
	return 0
}

type fakeBinaryFlowService struct {
	dexpb.UnimplementedFlowServiceServer
}

func (*fakeBinaryFlowService) SyncAttributeIndexes(
	context.Context,
	*dexpb.SyncAttributeIndexRequest,
) (*dexpb.SyncAttributeIndexResponse, error) {
	return &dexpb.SyncAttributeIndexResponse{}, nil
}
