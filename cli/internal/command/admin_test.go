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

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"
)

type stepDownTestService struct {
	dexpb.UnimplementedAdminServiceServer
	response *dexpb.StepDownActiveNodeResponse
	failure  error

	mu             sync.Mutex
	authorizations []string
}

func (s *stepDownTestService) StepDownActiveNode(
	ctx context.Context,
	_ *dexpb.StepDownActiveNodeRequest,
) (*dexpb.StepDownActiveNodeResponse, error) {
	requestMetadata, _ := metadata.FromIncomingContext(ctx)
	s.mu.Lock()
	s.authorizations = append(s.authorizations, requestMetadata.Get("authorization")...)
	s.mu.Unlock()
	if s.failure != nil {
		return nil, s.failure
	}
	return s.response, nil
}

func (s *stepDownTestService) recordedAuthorizations() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.authorizations...)
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

func startStepDownTestService(t *testing.T, service *stepDownTestService) string {
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

func TestAdminStepDownPrintsTheObservedTakeoverAndSendsTheCredential(t *testing.T) {
	service := &stepDownTestService{response: &dexpb.StepDownActiveNodeResponse{
		NodeId:            "node-a",
		ReleasedNodeEpoch: 7,
		ObservedTakeover: &dexpb.ActiveNodeTakeover{
			NodeEpoch:       8,
			HolderProcessId: "standby-process",
		},
		LeaseReacquisitionHoldOff: durationpb.New(5_500_000_000),
	}}
	address := startStepDownTestService(t, service)
	credentialFile := filepath.Join(t.TempDir(), "admin-token")
	require.NoError(t, os.WriteFile(credentialFile, []byte("admin-jwt\n"), 0o600))

	output, err := executeAdminCommand(
		"admin", "step-down", "--yes", "--server", address, "--credential-file", credentialFile,
	)

	require.NoError(t, err)
	require.JSONEq(t, `{
		"nodeId": "node-a",
		"releasedNodeEpoch": 7,
		"isTakeoverObserved": true,
		"observedTakeover": {"nodeEpoch": 8, "holderProcessId": "standby-process"},
		"leaseReacquisitionHoldOff": "5.5s"
	}`, output)
	require.Equal(t, []string{"Bearer admin-jwt"}, service.recordedAuthorizations())
}

func TestAdminStepDownWithoutTakeoverOmitsTheTakeover(t *testing.T) {
	service := &stepDownTestService{response: &dexpb.StepDownActiveNodeResponse{
		NodeId:                    "node-a",
		ReleasedNodeEpoch:         7,
		LeaseReacquisitionHoldOff: durationpb.New(5_000_000_000),
	}}
	address := startStepDownTestService(t, service)

	output, err := executeAdminCommand("admin", "step-down", "--yes", "--server", address)

	require.NoError(t, err)
	require.JSONEq(t, `{
		"nodeId": "node-a",
		"releasedNodeEpoch": 7,
		"isTakeoverObserved": false,
		"leaseReacquisitionHoldOff": "5s"
	}`, output)
	require.Empty(t, service.recordedAuthorizations())
}

func TestAdminStepDownRequiresConfirmationBeforeCalling(t *testing.T) {
	service := &stepDownTestService{response: &dexpb.StepDownActiveNodeResponse{}}
	address := startStepDownTestService(t, service)

	_, err := executeAdminCommand("admin", "step-down", "--server", address)

	require.Error(t, err)
	require.Equal(t, 2, ExitCode(err))
	require.Equal(t, "confirmation_required", writtenError(t, err)["kind"])
	require.Empty(t, service.recordedAuthorizations())
}

func TestAdminStepDownReportsRefusalsAsOperationErrors(t *testing.T) {
	address := startStepDownTestService(t, &stepDownTestService{
		failure: status.Error(codes.FailedPrecondition, "this server process is not the active process of its node ID"),
	})
	_, err := executeAdminCommand("admin", "step-down", "--yes", "--server", address)
	require.Error(t, err)
	require.Equal(t, 1, ExitCode(err))
	written := writtenError(t, err)
	require.Equal(t, "admin step-down", written["operation"])
	require.Equal(t, "FailedPrecondition", written["grpcCodeName"])

	flowServiceOnly := startGRPCServer(t, func(grpcServer *grpc.Server) {
		dexpb.RegisterFlowServiceServer(grpcServer, &dexpb.UnimplementedFlowServiceServer{})
	})
	_, err = executeAdminCommand("admin", "step-down", "--yes", "--server", flowServiceOnly)
	require.Error(t, err)
	require.Equal(t, "Unimplemented", writtenError(t, err)["grpcCodeName"])
}

func TestAdminCommandRejectsInvalidUsage(t *testing.T) {
	for _, arguments := range [][]string{
		{"admin"},
		{"admin", "promote"},
		{"admin", "step-down", "--yes", "unexpected"},
		{"admin", "step-down", "--unknown-flag"},
	} {
		_, err := executeAdminCommand(arguments...)
		require.Error(t, err, "%v", arguments)
		require.Equal(t, 2, ExitCode(err), "%v", arguments)
	}
	output, err := executeAdminCommand("admin", "step-down", "--help")
	require.NoError(t, err)
	require.Contains(t, output, "dexcli admin step-down --yes")
}
