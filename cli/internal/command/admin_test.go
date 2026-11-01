// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package command

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"
)

// adminTestService answers AdminService calls and records what each step down received.
type adminTestService struct {
	dexpb.UnimplementedAdminServiceServer
	description *dexpb.DescribeActiveNodeResponse
	response    *dexpb.StepDownActiveNodeResponse
	failure     error
	// isBlocking makes each step down wait until its call ends, so the client times out.
	isBlocking bool

	mu                 sync.Mutex
	callCount          int
	authorizations     []string
	expectedNodeEpochs []*uint64
	remainingDeadlines []*time.Duration
}

func (s *adminTestService) DescribeActiveNode(
	ctx context.Context,
	_ *dexpb.DescribeActiveNodeRequest,
) (*dexpb.DescribeActiveNodeResponse, error) {
	requestMetadata, _ := metadata.FromIncomingContext(ctx)
	s.mu.Lock()
	s.authorizations = append(s.authorizations, requestMetadata.Get("authorization")...)
	s.mu.Unlock()
	return s.description, nil
}

func (s *adminTestService) StepDownActiveNode(
	ctx context.Context,
	request *dexpb.StepDownActiveNodeRequest,
) (*dexpb.StepDownActiveNodeResponse, error) {
	requestMetadata, _ := metadata.FromIncomingContext(ctx)
	var remainingDeadline *time.Duration
	if deadline, hasDeadline := ctx.Deadline(); hasDeadline {
		remaining := time.Until(deadline)
		remainingDeadline = &remaining
	}
	s.mu.Lock()
	s.callCount++
	s.authorizations = append(s.authorizations, requestMetadata.Get("authorization")...)
	s.expectedNodeEpochs = append(s.expectedNodeEpochs, request.ExpectedNodeEpoch)
	s.remainingDeadlines = append(s.remainingDeadlines, remainingDeadline)
	s.mu.Unlock()
	if s.isBlocking {
		<-ctx.Done()
		return nil, status.FromContextError(ctx.Err()).Err()
	}
	if s.failure != nil {
		return nil, s.failure
	}
	return s.response, nil
}

func (s *adminTestService) recordedCallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.callCount
}

func (s *adminTestService) recordedAuthorizations() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.authorizations...)
}

func (s *adminTestService) recordedExpectedNodeEpochs() []*uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*uint64(nil), s.expectedNodeEpochs...)
}

func (s *adminTestService) recordedRemainingDeadlines() []*time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*time.Duration(nil), s.remainingDeadlines...)
}

// startGRPCServer serves the registered services on a loopback port and returns its address.
func startGRPCServer(t *testing.T, register func(*grpc.Server)) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	grpcServer := grpc.NewServer()
	register(grpcServer)
	serveFinished := make(chan error, 1)
	go func() { serveFinished <- grpcServer.Serve(listener) }()
	t.Cleanup(func() {
		grpcServer.GracefulStop()
		serveErr := <-serveFinished
		if serveErr != nil {
			require.ErrorIs(t, serveErr, grpc.ErrServerStopped)
		}
	})
	return listener.Addr().String()
}

func startAdminTestService(t *testing.T, service *adminTestService) string {
	t.Helper()
	return startGRPCServer(t, func(grpcServer *grpc.Server) {
		dexpb.RegisterAdminServiceServer(grpcServer, service)
	})
}

func executeAdminCommand(arguments ...string) (string, error) {
	stdout := &bytes.Buffer{}
	app := NewApp(bytes.NewReader(nil), stdout, &bytes.Buffer{})
	app.lookupEnv = func(string) (string, bool) { return "", false }
	err := app.Execute(context.Background(), arguments)
	return stdout.String(), err
}

func writtenError(t *testing.T, err error) map[string]any {
	t.Helper()
	output := &bytes.Buffer{}
	WriteError(output, err)
	var payload map[string]map[string]any
	require.NoError(t, json.Unmarshal(output.Bytes(), &payload))
	return payload["error"]
}

func takeoverResponse() *dexpb.StepDownActiveNodeResponse {
	return &dexpb.StepDownActiveNodeResponse{
		NodeId:                    "node-a",
		ReleasedNodeEpoch:         7,
		LeaseReacquisitionHoldOff: durationpb.New(5_500 * time.Millisecond),
		LeaseHolderAfterStepDown: &dexpb.StepDownActiveNodeResponse_TakenOverByAnotherProcess{
			TakenOverByAnotherProcess: &dexpb.NodeLeaseHolder{
				NodeEpoch:       8,
				HolderProcessId: "standby-process",
			},
		},
	}
}

func TestAdminDescribePrintsTheActiveNodeAndSendsTheCredential(t *testing.T) {
	service := &adminTestService{description: &dexpb.DescribeActiveNodeResponse{
		NodeId:          "node-a",
		NodeEpoch:       18_446_744_073_709_551_615,
		HolderProcessId: "active-process",
	}}
	address := startAdminTestService(t, service)
	credentialFile := filepath.Join(t.TempDir(), "admin-token")
	require.NoError(t, os.WriteFile(credentialFile, []byte("admin-jwt\n"), 0o600))

	output, err := executeAdminCommand(
		"admin", "describe", "--server", address, "--credential-file", credentialFile,
	)

	require.NoError(t, err)
	require.JSONEq(t, `{
		"nodeId": "node-a",
		"nodeEpoch": "18446744073709551615",
		"holderProcessId": "active-process"
	}`, output)
	require.Equal(t, []string{"Bearer admin-jwt"}, service.recordedAuthorizations())
	require.Zero(t, service.recordedCallCount())
}

func TestAdminStepDownPrintsTheTakeoverAndSendsTheCredential(t *testing.T) {
	service := &adminTestService{response: takeoverResponse()}
	address := startAdminTestService(t, service)
	credentialFile := filepath.Join(t.TempDir(), "admin-token")
	require.NoError(t, os.WriteFile(credentialFile, []byte("admin-jwt\n"), 0o600))

	output, err := executeAdminCommand(
		"admin", "step-down", "--yes", "--server", address, "--credential-file", credentialFile,
	)

	require.NoError(t, err)
	require.JSONEq(t, `{
		"nodeId": "node-a",
		"releasedNodeEpoch": "7",
		"leaseReacquisitionHoldOff": "5.5s",
		"leaseHolderAfterStepDown": "takenOverByAnotherProcess",
		"activeNodeEpoch": "8",
		"activeHolderProcessId": "standby-process"
	}`, output)
	require.Equal(t, 1, service.recordedCallCount())
	require.Equal(t, []string{"Bearer admin-jwt"}, service.recordedAuthorizations())
}

func TestAdminStepDownReportsATakeBackAndAnUnknownLeaseHolder(t *testing.T) {
	takeBack := takeoverResponse()
	takeBack.LeaseHolderAfterStepDown = &dexpb.StepDownActiveNodeResponse_TakenBackByThisProcess{
		TakenBackByThisProcess: &dexpb.NodeLeaseHolder{NodeEpoch: 8, HolderProcessId: "active-process"},
	}
	output, err := executeAdminCommand(
		"admin", "step-down", "--yes", "--server", startAdminTestService(t, &adminTestService{response: takeBack}),
	)
	require.NoError(t, err)
	require.JSONEq(t, `{
		"nodeId": "node-a",
		"releasedNodeEpoch": "7",
		"leaseReacquisitionHoldOff": "5.5s",
		"leaseHolderAfterStepDown": "takenBackByThisProcess",
		"activeNodeEpoch": "8",
		"activeHolderProcessId": "active-process"
	}`, output)

	unknown := takeoverResponse()
	unknown.LeaseHolderAfterStepDown = &dexpb.StepDownActiveNodeResponse_LeaseHolderUnknown{
		LeaseHolderUnknown: &dexpb.NodeLeaseHolderUnknown{Detail: "reading the node lease failed"},
	}
	output, err = executeAdminCommand(
		"admin", "step-down", "--yes", "--server", startAdminTestService(t, &adminTestService{response: unknown}),
	)
	require.NoError(t, err)
	require.JSONEq(t, `{
		"nodeId": "node-a",
		"releasedNodeEpoch": "7",
		"leaseReacquisitionHoldOff": "5.5s",
		"leaseHolderAfterStepDown": "unknown",
		"leaseHolderUnknownDetail": "reading the node lease failed"
	}`, output)
}

func TestAdminStepDownSendsTheExpectedNodeEpochOnlyWhenSet(t *testing.T) {
	service := &adminTestService{response: takeoverResponse()}
	address := startAdminTestService(t, service)

	for _, arguments := range [][]string{
		{"admin", "step-down", "--yes", "--server", address},
		{"admin", "step-down", "--yes", "--expected-node-epoch", "7", "--server", address},
		{"admin", "step-down", "--yes", "--expected-node-epoch", "0", "--server", address},
	} {
		_, err := executeAdminCommand(arguments...)
		require.NoError(t, err, "%v", arguments)
	}

	expectedNodeEpochs := service.recordedExpectedNodeEpochs()
	require.Len(t, expectedNodeEpochs, 3)
	require.Nil(t, expectedNodeEpochs[0])
	require.NotNil(t, expectedNodeEpochs[1])
	require.Equal(t, uint64(7), *expectedNodeEpochs[1])
	require.NotNil(t, expectedNodeEpochs[2])
	require.Equal(t, uint64(0), *expectedNodeEpochs[2])
}

func TestAdminStepDownWaitsLongerThanTheHoldOffUnlessTimeoutIsSet(t *testing.T) {
	service := &adminTestService{response: takeoverResponse()}
	address := startAdminTestService(t, service)

	for _, arguments := range [][]string{
		{"admin", "step-down", "--yes", "--server", address},
		{"--timeout", "10s", "admin", "step-down", "--yes", "--server", address},
		{"admin", "step-down", "--yes", "--timeout", "10s", "--server", address},
		{"--timeout", "0", "admin", "step-down", "--yes", "--server", address},
	} {
		_, err := executeAdminCommand(arguments...)
		require.NoError(t, err, "%v", arguments)
	}

	deadlines := service.recordedRemainingDeadlines()
	require.Len(t, deadlines, 4)
	for index, maximum := range []time.Duration{adminStepDownDefaultTimeout, 10 * time.Second, 10 * time.Second} {
		require.NotNil(t, deadlines[index], "call %d", index)
		require.LessOrEqual(t, *deadlines[index], maximum, "call %d", index)
		require.Greater(t, *deadlines[index], maximum-5*time.Second, "call %d", index)
	}
	require.Nil(t, deadlines[3], "--timeout 0 disables the deadline")
}

func TestAdminStepDownRequiresConfirmationBeforeCalling(t *testing.T) {
	service := &adminTestService{response: takeoverResponse()}
	address := startAdminTestService(t, service)

	_, err := executeAdminCommand("admin", "step-down", "--server", address)

	require.Error(t, err)
	require.Equal(t, 2, ExitCode(err))
	require.Equal(t, "confirmation_required", writtenError(t, err)["kind"])
	require.Zero(t, service.recordedCallCount())

	_, err = executeAdminCommand("admin", "step-down", "--yes", "--server", address)
	require.NoError(t, err)
	require.Equal(t, 1, service.recordedCallCount())
}

func TestAdminStepDownReportsRefusalsAsOperationErrors(t *testing.T) {
	address := startAdminTestService(t, &adminTestService{
		failure: status.Error(codes.FailedPrecondition, "the active server process holds node epoch 8, not the expected node epoch 7"),
	})
	_, err := executeAdminCommand("admin", "step-down", "--yes", "--expected-node-epoch", "7", "--server", address)
	require.Error(t, err)
	require.Equal(t, 1, ExitCode(err))
	written := writtenError(t, err)
	require.Equal(t, "admin step-down", written["operation"])
	require.Equal(t, "FailedPrecondition", written["grpcCodeName"])
	require.NotContains(t, written, "stepDownOutcome")

	flowServiceOnly := startGRPCServer(t, func(grpcServer *grpc.Server) {
		dexpb.RegisterFlowServiceServer(grpcServer, &dexpb.UnimplementedFlowServiceServer{})
	})
	_, err = executeAdminCommand("admin", "step-down", "--yes", "--server", flowServiceOnly)
	require.Error(t, err)
	require.Equal(t, "Unimplemented", writtenError(t, err)["grpcCodeName"])
	_, err = executeAdminCommand("admin", "describe", "--server", flowServiceOnly)
	require.Error(t, err)
	written = writtenError(t, err)
	require.Equal(t, "admin describe", written["operation"])
	require.Equal(t, "Unimplemented", written["grpcCodeName"])
}

func TestAdminStepDownReportsAnUnknownOutcomeWhenTheCallTimesOut(t *testing.T) {
	service := &adminTestService{isBlocking: true}
	address := startAdminTestService(t, service)

	_, err := executeAdminCommand("admin", "step-down", "--yes", "--timeout", "100ms", "--server", address)

	require.Error(t, err)
	require.Equal(t, 1, ExitCode(err))
	written := writtenError(t, err)
	require.Equal(t, "DeadlineExceeded", written["grpcCodeName"])
	require.Equal(t, "unknown", written["stepDownOutcome"])
	require.Contains(t, written["hint"], "dexcli admin describe")
	require.Contains(t, written["hint"], "--expected-node-epoch")
	require.Equal(t, 1, service.recordedCallCount())
}

func TestAdminCommandRejectsInvalidUsage(t *testing.T) {
	for _, arguments := range [][]string{
		{"admin"},
		{"admin", "promote"},
		{"admin", "step-down", "--yes", "unexpected"},
		{"admin", "step-down", "--unknown-flag"},
		{"admin", "step-down", "--yes", "--expected-node-epoch", "-1"},
		{"admin", "step-down", "--yes", "--expected-node-epoch", "latest"},
		{"admin", "describe", "unexpected"},
	} {
		_, err := executeAdminCommand(arguments...)
		require.Error(t, err, "%v", arguments)
		require.Equal(t, 2, ExitCode(err), "%v", arguments)
	}
}

func TestAdminHelpListsSubcommandsAndFlags(t *testing.T) {
	output, err := executeAdminCommand("admin", "--help")
	require.NoError(t, err)
	require.Contains(t, output, "describe")
	require.Contains(t, output, "step-down")

	output, err = executeAdminCommand("admin", "step-down", "--help")
	require.NoError(t, err)
	require.Contains(t, output, "dexcli admin step-down --yes")
	for _, flagName := range []string{"-yes", "-expected-node-epoch epoch", "-timeout", "-credential-file", "-server"} {
		require.Contains(t, output, flagName)
	}
	require.Contains(t, output, "(default 2m0s)")

	output, err = executeAdminCommand("admin", "describe", "-h")
	require.NoError(t, err)
	require.Contains(t, output, "dexcli admin describe")
	require.Contains(t, output, "-timeout")
	require.NotContains(t, output, "-expected-node-epoch")
}
