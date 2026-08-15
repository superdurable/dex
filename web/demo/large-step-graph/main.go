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
	"strconv"
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
	flowType         = "web-large-step-graph-90"
	parallelBranches = 6
	branchDepth      = 5
	serialBefore     = 30
	serialAfter      = 30
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

	requestContext, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	flowClient := dexpb.NewFlowServiceClient(connection)
	flowID := fmt.Sprintf("web-large-step-graph-90-%d", time.Now().Unix())
	start, err := flowClient.StartFlow(
		requestContext,
		&dexpb.StartFlowRequest{
			RequestId:          uuid.NewString(),
			FlowId:             flowID,
			FlowType:           flowType,
			FlowTimeoutSeconds: 150,
			StartStepType:      serialBeforeStep(1),
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
		},
	)
	if err != nil {
		return err
	}
	result, err := flowClient.WaitForFlow(
		requestContext,
		&dexpb.WaitForFlowRequest{
			FlowId:          flowID,
			RunId:           start.GetRunId(),
			WaitTimeSeconds: 120,
		},
	)
	if err != nil {
		return err
	}
	webAddress := strings.TrimRight(environmentOrDefault("DEX_WEB_URL", "http://127.0.0.1:8802"), "/")
	_, err = fmt.Printf(
		"flow_id=%s\nrun_id=%s\nstatus=%s\nurl=%s/flows/%s/%s\n",
		flowID,
		start.GetRunId(),
		result.GetFlowStatus(),
		webAddress,
		url.PathEscape(flowID),
		url.PathEscape(start.GetRunId()),
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
	stepType := request.GetStepType()
	switch {
	case strings.HasPrefix(stepType, "SerialBefore-"):
		index, parseErr := stepIndex(stepType)
		if parseErr != nil {
			return nil, status.Error(codes.InvalidArgument, "invalid serial-before step")
		}
		if index < serialBefore {
			return nextSteps(serialBeforeStep(index + 1)), nil
		}
		branches := make([]string, 0, parallelBranches)
		for branch := 1; branch <= parallelBranches; branch++ {
			branches = append(branches, branchStep(branch, 1))
		}
		return nextSteps(branches...), nil
	case strings.HasPrefix(stepType, "Branch-"):
		branch, depth, parseErr := branchIndexes(stepType)
		if parseErr != nil {
			return nil, status.Error(codes.InvalidArgument, "invalid branch step")
		}
		if depth < branchDepth {
			return nextSteps(branchStep(branch, depth+1)), nil
		}
		if branch == 1 {
			return nextSteps(serialAfterStep(1)), nil
		}
		return closeGracefully(), nil
	case strings.HasPrefix(stepType, "SerialAfter-"):
		index, parseErr := stepIndex(stepType)
		if parseErr != nil {
			return nil, status.Error(codes.InvalidArgument, "invalid serial-after step")
		}
		if index < serialAfter {
			return nextSteps(serialAfterStep(index + 1)), nil
		}
		return closeGracefully(), nil
	default:
		return nil, status.Errorf(codes.InvalidArgument, "unexpected step type %q", stepType)
	}
}

func nextSteps(stepTypes ...string) *dexpb.InvokeExecuteMethodResponse {
	movements := make([]*dexpb.StepMovement, 0, len(stepTypes))
	for _, stepType := range stepTypes {
		movements = append(movements, &dexpb.StepMovement{
			StepType:    stepType,
			StepOptions: executeOnlyOptions(),
		})
	}
	return &dexpb.InvokeExecuteMethodResponse{
		StepDecision: &dexpb.StepDecision{NextSteps: movements},
	}
}

func executeOnlyOptions() *dexpb.StepOptions {
	return &dexpb.StepOptions{SkipWaitFor: true}
}

func closeGracefully() *dexpb.InvokeExecuteMethodResponse {
	return &dexpb.InvokeExecuteMethodResponse{
		StepDecision: &dexpb.StepDecision{
			CloseDecision: &dexpb.CloseDecision{
				CloseDecisionType: dexpb.CloseDecisionType_CLOSE_DECISION_TYPE_GRACEFUL_COMPLETE,
			},
		},
	}
}

func serialBeforeStep(index int) string {
	return fmt.Sprintf("SerialBefore-%03d", index)
}

func serialAfterStep(index int) string {
	return fmt.Sprintf("SerialAfter-%03d", index)
}

func branchStep(branch, depth int) string {
	return fmt.Sprintf("Branch-%d-%02d", branch, depth)
}

func stepIndex(stepType string) (int, error) {
	parts := strings.Split(stepType, "-")
	return strconv.Atoi(parts[len(parts)-1])
}

func branchIndexes(stepType string) (int, int, error) {
	parts := strings.Split(stepType, "-")
	if len(parts) != 3 {
		return 0, 0, fmt.Errorf("invalid branch step %q", stepType)
	}
	branch, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, err
	}
	depth, err := strconv.Atoi(parts[2])
	return branch, depth, err
}

func environmentOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
