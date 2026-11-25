// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package integ

import (
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/dex"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/emptypb"
)

const nonRetryableDetail = "go card rejected"

var (
	// nonRetryableAttempts counts failing handler calls per Flow ID.
	nonRetryableAttempts sync.Map
	// nonRetryableRecoveries stores the RecoveryErrorInfo each Flow's recovery handler saw.
	nonRetryableRecoveries sync.Map
)

type nonRetryableCardRejectedError struct{}

func (nonRetryableCardRejectedError) Error() string {
	return nonRetryableDetail
}

// hourlyRetry keeps a real business retry policy: unlimited attempts an hour apart.
func hourlyRetry() *dex.RetryPolicy {
	return &dex.RetryPolicy{InitialInterval: time.Hour, MaximumInterval: time.Hour}
}

// nonRetryableServerZDRelease is the first server-zd release that honors WorkerErrorResponse.non_retryable.
var nonRetryableServerZDRelease = [3]int{0, 1, 38}

// skipUnlessServerHonorsNonRetryable skips on a released server-zd older than
// nonRetryableServerZDRelease, as the release compatibility workflow can select. A source
// build reports the unstamped workspace version 0.1.0 and always runs the test.
func skipUnlessServerHonorsNonRetryable(t *testing.T) {
	t.Helper()
	connection, err := grpc.NewClient(flowServiceAddress(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, connection.Close()) })
	serverInfo, err := dexpb.NewFlowServiceClient(connection).GetServerInfo(integrationContext(t), &emptypb.Empty{})
	require.NoError(t, err)
	var release [3]int
	if _, err := fmt.Sscanf(serverInfo.GetServerVersion(), "%d.%d.%d", &release[0], &release[1], &release[2]); err != nil {
		return
	}
	isUnstampedSourceBuild := release == [3]int{0, 1, 0}
	if !isUnstampedSourceBuild && slices.Compare(release[:], nonRetryableServerZDRelease[:]) < 0 {
		t.Skipf("server-zd %s predates non-retryable Worker failures", serverInfo.GetServerVersion())
	}
}

func TestNonRetryableExecuteFailureSkipsRetriesAndProceeds(t *testing.T) {
	skipUnlessServerHonorsNonRetryable(t)
	flowID := newFlowID(t, "non-retryable-execute")
	_, err := integClient.StartFlow(integrationContext(t), nonRetryableExecuteFlow{}, flowID, "order", dex.StartFlowOptions{})
	require.NoError(t, err)

	result := waitForFlow(t, flowID, false)
	require.Equal(t, dex.FlowCompleted, result.Status)
	assertNonRetryableOutcome(t, flowID)
}

func TestNonRetryableWaitForFailureSkipsRetriesAndProceeds(t *testing.T) {
	skipUnlessServerHonorsNonRetryable(t)
	flowID := newFlowID(t, "non-retryable-wait-for")
	_, err := integClient.StartFlow(integrationContext(t), nonRetryableWaitForFlow{}, flowID, struct{}{}, dex.StartFlowOptions{})
	require.NoError(t, err)

	result := waitForFlow(t, flowID, false)
	require.Equal(t, dex.FlowCompleted, result.Status)
	assertNonRetryableOutcome(t, flowID)
}

// assertNonRetryableOutcome checks one handler call and a recovery that saw the wrapped cause.
func assertNonRetryableOutcome(t *testing.T, flowID string) {
	t.Helper()
	attempts, ok := nonRetryableAttempts.Load(flowID)
	require.True(t, ok)
	require.Equal(t, int32(1), attempts.(*atomic.Int32).Load())
	recovery, ok := nonRetryableRecoveries.Load(flowID)
	require.True(t, ok)
	require.Equal(t, nonRetryableDetail, recovery.(dex.RecoveryErrorInfo).Detail)
	require.Contains(t, recovery.(dex.RecoveryErrorInfo).ErrorType, "nonRetryableCardRejectedError")
}

func recordNonRetryableAttempt(ctx dex.Context) error {
	calls, _ := nonRetryableAttempts.LoadOrStore(ctx.FlowID(), &atomic.Int32{})
	calls.(*atomic.Int32).Add(1)
	return dex.NonRetryable(dex.ErrorWithStack(nonRetryableCardRejectedError{}))
}

func recordNonRetryableRecovery(ctx dex.Context) (*dex.StepDecision, error) {
	recovery := ctx.RecoveryError()
	if recovery == nil {
		return nil, dex.NonRetryable(dex.ErrorWithStack(errMissingRecoveryError))
	}
	nonRetryableRecoveries.Store(ctx.FlowID(), *recovery)
	return dex.GracefulComplete(recovery.Detail), nil
}

var errMissingRecoveryError = nonRetryableMissingRecoveryError{}

type nonRetryableMissingRecoveryError struct{}

func (nonRetryableMissingRecoveryError) Error() string {
	return "recovery error is missing"
}

type nonRetryableExecuteFlow struct {
	emptyFlowSchema
}

func (nonRetryableExecuteFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(nonRetryableChargeStep{}),
		dex.DefineStep(nonRetryableRecoveryStep{}),
	}
}

type nonRetryableChargeStep struct {
	dex.StepDefaultsNoWaitFor[string]
}

func (nonRetryableChargeStep) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{
		ExecuteRetry:   hourlyRetry(),
		ExecuteFailure: dex.ProceedToOnExecuteFailure(nonRetryableRecoveryStep{}, nil),
	}
}

func (nonRetryableChargeStep) Execute(ctx dex.Context, _ string) (*dex.StepDecision, error) {
	return nil, recordNonRetryableAttempt(ctx)
}

type nonRetryableRecoveryStep struct {
	dex.StepDefaultsNoWaitFor[string]
}

func (nonRetryableRecoveryStep) Execute(ctx dex.Context, _ string) (*dex.StepDecision, error) {
	return recordNonRetryableRecovery(ctx)
}

type nonRetryableWaitForFlow struct {
	emptyFlowSchema
}

func (nonRetryableWaitForFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(nonRetryableWaitForStep{})}
}

type nonRetryableWaitForStep struct {
	dex.DefaultStepType
}

func (nonRetryableWaitForStep) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{
		WaitForRetry:   hourlyRetry(),
		WaitForFailure: dex.ProceedOnWaitForFailure,
	}
}

func (nonRetryableWaitForStep) WaitFor(ctx dex.Context, _ struct{}) (*dex.Wait, error) {
	return nil, recordNonRetryableAttempt(ctx)
}

func (nonRetryableWaitForStep) Execute(ctx dex.Context, _ struct{}) (*dex.StepDecision, error) {
	return recordNonRetryableRecovery(ctx)
}
