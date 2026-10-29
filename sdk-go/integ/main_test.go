// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Sustainable Use License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package integ

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/blob-cache-go/blobcache"
	"github.com/superdurable/dex/sdk-go/dex"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"github.com/superdurable/dex/sdk-go/integ/webv2approval"
	"github.com/superdurable/dex/sdk-go/integ/webv2reply"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/emptypb"
)

var integClient *dex.Client

var integrationServerEngine = sync.OnceValues(fetchIntegrationServerEngine)

type emptyFlowSchema struct {
	dex.FlowDefaults
}

func (emptyFlowSchema) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{}
}

type integrationEnvironment struct {
	cache        *blobcache.Cache
	cacheDir     string
	client       *dex.Client
	worker       *dex.Worker
	workerResult chan error
}

func newIntegrationEnvironment() (*integrationEnvironment, error) {
	registry, err := dex.NewRegistry(integrationFlows())
	if err != nil {
		return nil, err
	}
	cacheDir, err := os.MkdirTemp("", "dex-go-sdk-integ-")
	if err != nil {
		return nil, err
	}
	cache, err := blobcache.New(&blobcache.Config{
		Dir:      cacheDir,
		MaxBytes: 64 << 20,
	})
	if err != nil {
		return nil, errors.Join(err, os.RemoveAll(cacheDir))
	}
	workerOptions := dex.WorkerOptions{FlowServiceAddress: flowServiceAddress()}
	worker, err := dex.NewWorker(registry, cache, workerOptions)
	if err != nil {
		return nil, errors.Join(err, cache.Close(), os.RemoveAll(cacheDir))
	}
	workerResult := make(chan error, 1)
	go func() {
		workerResult <- worker.Start()
	}()
	client, err := dex.NewClient(registry, cache, dex.ClientOptions{
		FlowServiceAddress: flowServiceAddress(),
	})
	if err != nil {
		stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return nil, errors.Join(err, worker.Stop(stopCtx), cache.Close())
	}
	environment := &integrationEnvironment{
		cache:        cache,
		cacheDir:     cacheDir,
		client:       client,
		worker:       worker,
		workerResult: workerResult,
	}
	if err := environment.waitUntilReady(); err != nil {
		return nil, errors.Join(err, environment.Close())
	}
	return environment, nil
}

func (environment *integrationEnvironment) waitUntilReady() error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	return environment.worker.WaitPullRegistered(ctx)
}

func (environment *integrationEnvironment) Close() error {
	clientErr := environment.client.Close()
	stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stopErr := environment.worker.Stop(stopCtx)
	workerErr := <-environment.workerResult
	cacheErr := environment.cache.Close()
	removeErr := os.RemoveAll(environment.cacheDir)
	return errors.Join(clientErr, stopErr, workerErr, cacheErr, removeErr)
}

func TestMain(tests *testing.M) {
	environment, err := newIntegrationEnvironment()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	integClient = environment.client
	exitCode := tests.Run()
	if err := environment.Close(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		exitCode = 1
	}
	os.Exit(exitCode)
}

func integrationFlows() []dex.Flow {
	return []dex.Flow{
		actionPermissionFlow{},
		actionScalarFlow{},
		actionStepFlow{},
		actionSubFlowParent{},
		actionSubFlowChild{},
		abnormalExitFlow{},
		basicFlow{},
		multiOutputFlow{},
		proceedOnWaitForFailureFlow{},
		executeRecoveryFlow{},
		interStepFlow{},
		noStartStepFlow{},
		noStepFlow{},
		persistenceFlow{},
		hardDeleteAttributeFlow{},
		getAttributesFlow{},
		stepErrorRetryFlow{},
		rpcFlow{},
		selectiveRPCFlow{},
		channelFlow{},
		streamTestFlow{},
		heartbeatRecoveryFlow{},
		asyncHeartbeatFlow{},
		executeOnlyFlow{},
		executeOnlyValueFlow{},
		forceFailFlow{},
		waitForFailureFlow{},
		waitForMethodTimeoutFlow{},
		timeoutHandlerFlow{},
		timerFlow{},
		subFlowParentFlow{},
		subFlowAllParentFlow{},
		subFlowAnyParentFlow{},
		subFlowAttachParentFlow{},
		subFlowAlwaysRestartParentFlow{},
		subFlowAbnormalParentFlow{},
		subFlowContinueAsNewParentFlow{},
		subFlowTimerFlow{},
		subFlowFailingFlow{},
		subFlowImmediateFlow{},
		stepCancellationFlow{},
		workerRetryAfterWaitForFlow{},
		workerRetryAfterExecuteFlow{},
		workerOriginStackWaitForFlow{},
		messageSizeFlow{},
		webDebugChannelFlow{},
		&webv2approval.ApprovalFlow{},
		&webv2reply.ReplyFlow{},
	}
}

func integrationContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	t.Cleanup(cancel)
	return ctx
}

func fetchIntegrationServerEngine() (dexpb.ServerEngine, error) {
	connection, err := grpc.NewClient(flowServiceAddress(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return dexpb.ServerEngine_SERVER_ENGINE_UNSPECIFIED, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	serverInfo, requestErr := dexpb.NewFlowServiceClient(connection).GetServerInfo(ctx, &emptypb.Empty{})
	if err := errors.Join(requestErr, connection.Close()); err != nil {
		return dexpb.ServerEngine_SERVER_ENGINE_UNSPECIFIED, err
	}
	return serverInfo.GetServerEngine(), nil
}

func newFlowID(t *testing.T, prefix string) string {
	t.Helper()
	return prefix + "-" + uuid.NewString()
}

func waitForFlow(t *testing.T, flowID string, needsResults bool) dex.FlowResult {
	t.Helper()
	result, err := integClient.WaitForFlow(
		integrationContext(t),
		flowID,
		dex.WaitForFlowOptions{NeedsResults: needsResults},
	)
	require.NoError(t, err)
	return result
}

func waitForUncompletedFlow(
	t *testing.T,
	flowID string,
	needsResults bool,
) dex.FlowResult {
	t.Helper()
	result, err := integClient.WaitForFlow(
		integrationContext(t),
		flowID,
		dex.WaitForFlowOptions{NeedsResults: needsResults},
	)
	require.NoError(t, err)
	require.True(t, result.IsTerminal())
	require.NotEqual(t, dex.FlowCompleted, result.Status)
	return result
}

func flowServiceAddress() string {
	if address := os.Getenv("DEX_FLOW_SERVICE_ADDRESS"); address != "" {
		return address
	}
	return "127.0.0.1:8801"
}
