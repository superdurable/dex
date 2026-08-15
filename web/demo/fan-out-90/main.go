// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

package main

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/service/common/ptr"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

const (
	flowType      = "web-step1-fanout-90"
	startStepType = "Step1"
	fanOutCount   = 90
)

type worker struct {
	dexpb.UnimplementedWorkerServiceServer
}

func main() {
	if err := run(); err != nil {
		panic(err)
	}
}

func run() (returnErr error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	workerServer := grpc.NewServer()
	dexpb.RegisterWorkerServiceServer(workerServer, &worker{})
	serverErrors := make(chan error, 1)
	go func() {
		serverErrors <- workerServer.Serve(listener)
	}()
	defer func() {
		workerServer.GracefulStop()
		if serveErr := <-serverErrors; serveErr != nil && returnErr == nil {
			returnErr = serveErr
		}
	}()

	flowServiceAddress := environmentOrDefault("DEX_FLOW_SERVICE_ADDRESS", "127.0.0.1:8801")
	connection, err := grpc.NewClient(
		flowServiceAddress,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := connection.Close(); closeErr != nil && returnErr == nil {
			returnErr = closeErr
		}
	}()

	requestContext, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	flowClient := dexpb.NewFlowServiceClient(connection)
	flowID := fmt.Sprintf("web-step1-fanout-90-%d", time.Now().Unix())
	started, err := flowClient.StartFlow(requestContext, &dexpb.StartFlowRequest{
		RequestId:          uuid.NewString(),
		FlowId:             flowID,
		FlowType:           flowType,
		FlowTimeoutSeconds: 180,
		StartStepType:      startStepType,
		StepOptions:        executeOnlyOptions(),
		FlowStartOptions: &dexpb.FlowStartOptions{
			FlowConfigOverride: &dexpb.FlowConfig{
				ContinueAsNewThreshold: ptr.Any(int32(100)),
				StepDurability: ptr.Any(
					dexpb.StepDurability_STEP_DURABILITY_SYNC,
				),
				WorkerTarget: &dexpb.WorkerTarget{Address: listener.Addr().String()},
			},
		},
	})
	if err != nil {
		return err
	}
	result, err := flowClient.WaitForFlow(requestContext, &dexpb.WaitForFlowRequest{
		FlowId:          flowID,
		RunId:           started.GetRunId(),
		WaitTimeSeconds: 150,
	})
	if err != nil {
		return err
	}
	webAddress := strings.TrimRight(environmentOrDefault("DEX_WEB_URL", "http://127.0.0.1:8802"), "/")
	_, err = fmt.Printf(
		"flow_id=%s\nrun_id=%s\nstatus=%s\nurl=%s/flows/%s/%s\n",
		flowID,
		started.GetRunId(),
		result.GetFlowStatus(),
		webAddress,
		url.PathEscape(flowID),
		url.PathEscape(started.GetRunId()),
	)
	return err
}

func (w *worker) InvokeExecuteMethod(
	_ context.Context,
	request *dexpb.InvokeExecuteMethodRequest,
) (*dexpb.InvokeExecuteMethodResponse, error) {
	if request.GetFlowType() != flowType {
		return nil, status.Error(codes.InvalidArgument, "unexpected flow type")
	}
	if request.GetStepType() == startStepType {
		movements := make([]*dexpb.StepMovement, 0, fanOutCount)
		for index := 1; index <= fanOutCount; index++ {
			movements = append(movements, &dexpb.StepMovement{
				StepType:    fmt.Sprintf("FanOut-%03d", index),
				StepOptions: executeOnlyOptions(),
			})
		}
		return &dexpb.InvokeExecuteMethodResponse{
			StepDecision: &dexpb.StepDecision{NextSteps: movements},
		}, nil
	}
	if !strings.HasPrefix(request.GetStepType(), "FanOut-") {
		return nil, status.Error(codes.InvalidArgument, "unexpected step type")
	}
	return &dexpb.InvokeExecuteMethodResponse{
		StepDecision: &dexpb.StepDecision{
			CloseDecision: &dexpb.CloseDecision{
				CloseDecisionType: dexpb.CloseDecisionType_CLOSE_DECISION_TYPE_GRACEFUL_COMPLETE,
			},
		},
	}, nil
}

func executeOnlyOptions() *dexpb.StepOptions {
	return &dexpb.StepOptions{SkipWaitFor: true}
}

func environmentOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
