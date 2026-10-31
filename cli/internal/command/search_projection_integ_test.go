// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//go:build integration

package command

import (
	"context"
	"net"
	"strings"
	"testing"

	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

type testSearchProjectionAdmin struct {
	dexpb.UnimplementedAdminServiceServer
	requests chan *dexpb.RebuildSearchProjectionRequest
}

func (s *testSearchProjectionAdmin) RebuildSearchProjection(_ context.Context, request *dexpb.RebuildSearchProjectionRequest) (*dexpb.RebuildSearchProjectionResponse, error) {
	s.requests <- request
	return &dexpb.RebuildSearchProjectionResponse{ScheduledPartitionIds: []uint32{1, 2}}, nil
}

func (s *testSearchProjectionAdmin) GetSearchProjectionStatus(context.Context, *emptypb.Empty) (*dexpb.GetSearchProjectionStatusResponse, error) {
	return nil, status.Error(codes.PermissionDenied, "configure auth.adminAudiences")
}

func TestSearchProjectionCommandsUseAdminRPCsAndValidateSelectors(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	service := &testSearchProjectionAdmin{requests: make(chan *dexpb.RebuildSearchProjectionRequest, 3)}
	dexpb.RegisterAdminServiceServer(server, service)
	finished := make(chan error, 1)
	go func() { finished <- server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		if err := <-finished; err != nil {
			t.Error(err)
		}
	})
	address := listener.Addr().String()
	response := executeTestCommand(t, nil, "search-projection", "rebuild", "--partitions", "1,2", "--server", address)
	if len(response["scheduled_partition_ids"].([]any)) != 2 {
		t.Fatal(response)
	}
	request := <-service.requests
	if len(request.PartitionIds) != 2 || request.PartitionIds[0] != 1 || request.PartitionIds[1] != 2 {
		t.Fatal(request)
	}
	for _, selector := range []string{"--all", "--marked"} {
		executeTestCommand(t, nil, "search-projection", "rebuild", selector, "--server", address)
		request := <-service.requests
		if (selector == "--all") != request.AllPartitions || (selector == "--marked") != request.AllMarkedPartitions {
			t.Fatal(request)
		}
	}
	app := NewApp(strings.NewReader(""), &strings.Builder{}, &strings.Builder{})
	err = app.Execute(context.Background(), []string{"search-projection", "status", "--server", address})
	if err == nil || !strings.Contains(err.Error(), "auth.adminAudiences") {
		t.Fatalf("admin denial: %v", err)
	}
	for _, args := range [][]string{
		{"rebuild"}, {"rebuild", "--all", "--marked"}, {"rebuild", "--partitions", "-1"},
		{"rebuild", "--partitions", "1,,2"}, {"rebuild", "--all", "--partitions", "1"},
	} {
		if err := app.Execute(context.Background(), append([]string{"search-projection"}, args...)); err == nil {
			t.Fatalf("invalid selectors accepted: %v", args)
		}
	}
}
