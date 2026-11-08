// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	health "google.golang.org/grpc/health/grpc_health_v1"
)

const flowType = "RSSMeasurementFlow"
const stepType = "RSSMeasurementStep"

type rssMeasurement struct {
	flowClient   dexpb.FlowServiceClient
	workerClient dexpb.WorkerTaskServiceClient
	flowID       string
	stepCount    int
	valueBytes   int
	shouldReset  bool
}

func main() {
	address := flag.String("address", "127.0.0.1:17233", "local server gRPC address")
	scenario := flag.String("scenario", "small", "small: 20000 1-KiB Steps; large: 200 2-MiB rewrites and ResetFlow")
	flowID := flag.String("flow-id", "", "unique Flow ID; defaults to rss-<scenario>-<timestamp>")
	flag.Parse()
	if *scenario != "small" && *scenario != "large" {
		log.Fatal("scenario must be small or large")
	}
	if *flowID == "" {
		*flowID = fmt.Sprintf("rss-%s-%d", *scenario, time.Now().UnixNano())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Minute)
	defer cancel()
	connection, err := grpc.NewClient(*address,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(32<<20), grpc.MaxCallSendMsgSize(32<<20)),
	)
	if err != nil {
		log.Fatal(err)
	}
	defer closeConnection(connection)
	if err := waitForServer(ctx, connection); err != nil {
		log.Fatal(err)
	}
	workload := rssMeasurement{
		flowClient:   dexpb.NewFlowServiceClient(connection),
		workerClient: dexpb.NewWorkerTaskServiceClient(connection),
		flowID:       *flowID,
		stepCount:    20000,
		valueBytes:   1024,
		shouldReset:  *scenario == "large",
	}
	if workload.shouldReset {
		workload.stepCount, workload.valueBytes = 200, 2<<20
	}
	if err := workload.run(ctx); err != nil {
		log.Fatal(err)
	}
}

func (m *rssMeasurement) run(ctx context.Context) error {
	workerContext, cancelWorker := context.WithCancel(ctx)
	defer cancelWorker()
	stream, err := m.workerClient.PollWorkerTask(workerContext)
	if err != nil {
		return fmt.Errorf("open Worker stream: %w", err)
	}
	registration := &dexpb.PollWorkerTaskRegister{
		Queue: flowType, AppWorkerId: m.flowID, FlowTypes: []string{flowType},
		SdkName: "rss-measurement", SdkVersion: "0",
	}
	if err := stream.Send(&dexpb.PollWorkerTaskRequest{Frame: &dexpb.PollWorkerTaskRequest_Register{Register: registration}}); err != nil {
		return fmt.Errorf("register Worker: %w", err)
	}
	registered, err := stream.Recv()
	if err != nil {
		return fmt.Errorf("receive Worker registration: %w", err)
	}
	if registered.GetRegistered() == nil {
		return errors.New("Worker did not receive a registration acknowledgment")
	}
	started, err := m.flowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		FlowId: m.flowID, FlowType: flowType, StartStepType: stepType,
		StepInput: textValue("0"), StepOptions: &dexpb.StepOptions{SkipWaitFor: true},
		FlowStartOptions: &dexpb.FlowStartOptions{
			FlowConfigOverride: &dexpb.FlowConfig{WorkerTarget: &dexpb.WorkerTarget{IsPullMode: true}},
		},
		RequestId: m.flowID,
	})
	if err != nil {
		return fmt.Errorf("start Flow: %w", err)
	}
	log.Printf("started flow=%s run=%s steps=%d value_bytes=%d", m.flowID, started.RunId, m.stepCount, m.valueBytes)
	for completedSteps := 0; completedSteps < m.stepCount; {
		response, err := stream.Recv()
		if err != nil {
			return fmt.Errorf("receive Step %d: %w", completedSteps, err)
		}
		if response.GetPing() != nil {
			if err := stream.Send(&dexpb.PollWorkerTaskRequest{Frame: &dexpb.PollWorkerTaskRequest_Pong{Pong: &dexpb.PollWorkerTaskPong{}}}); err != nil {
				return fmt.Errorf("answer Worker ping: %w", err)
			}
			continue
		}
		task := response.GetTask()
		if task == nil {
			return fmt.Errorf("unexpected Worker frame: %T", response.Frame)
		}
		request := task.GetExecute()
		if request == nil {
			return errors.New("expected an Execute task")
		}
		stepIndex, err := strconv.Atoi(request.GetStepInput().GetStringValue())
		if err != nil || stepIndex != completedSteps {
			return fmt.Errorf("expected Step %d, received input %q", completedSteps, request.GetStepInput().GetStringValue())
		}
		if err := stream.Send(m.stepReply(task.TaskId, stepIndex)); err != nil {
			return fmt.Errorf("reply to Step %d: %w", stepIndex, err)
		}
		completedSteps++
		if completedSteps%1000 == 0 || m.shouldReset && completedSteps%20 == 0 {
			log.Printf("completed_steps=%d", completedSteps)
		}
	}
	result, err := m.flowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{FlowId: m.flowID, WaitTimeSeconds: 30})
	if err != nil {
		return fmt.Errorf("wait for Flow completion: %w", err)
	}
	if result.FlowStatus != dexpb.FlowStatus_FLOW_STATUS_COMPLETED {
		return fmt.Errorf("Flow ended with %s: %s", result.FlowStatus, result.ErrorMessage)
	}
	cancelWorker()
	log.Print("Flow completed")
	if m.shouldReset {
		return m.resetFlow(ctx)
	}
	return nil
}

func (m *rssMeasurement) stepReply(taskID string, stepIndex int) *dexpb.PollWorkerTaskRequest {
	decision := &dexpb.StepDecision{NextSteps: []*dexpb.StepMovement{{
		StepType: stepType, StepInput: textValue(strconv.Itoa(stepIndex + 1)),
		StepOptions: &dexpb.StepOptions{SkipWaitFor: true},
	}}}
	if stepIndex+1 == m.stepCount {
		decision = &dexpb.StepDecision{CloseDecision: &dexpb.CloseDecision{
			CloseDecisionType: dexpb.CloseDecisionType_CLOSE_DECISION_TYPE_FORCE_COMPLETE,
			CloseInput:        textValue("done"),
		}}
	}
	prefix := fmt.Sprintf("%08d:", stepIndex)
	output := &dexpb.InvokeExecuteMethodResponse{
		StepDecision: decision,
		UpsertAttributes: []*dexpb.AttributeWrite{{
			Key: "payload", Value: textValue(prefix + strings.Repeat("x", m.valueBytes-len(prefix))),
		}},
	}
	return &dexpb.PollWorkerTaskRequest{Frame: &dexpb.PollWorkerTaskRequest_TaskFrame{
		TaskFrame: &dexpb.PollWorkerTaskFrame{
			TaskId: taskID,
			Output: &dexpb.PollWorkerTaskFrame_Execute{Execute: &dexpb.InvokeExecuteMethodOutput{
				Output: &dexpb.InvokeExecuteMethodOutput_Result{Result: output},
			}},
		},
	}}
}

func (m *rssMeasurement) resetFlow(ctx context.Context) error {
	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
		return ctx.Err()
	}
	log.Print("ResetFlow starting")
	reset, err := m.flowClient.ResetFlow(ctx, &dexpb.ResetFlowRequest{FlowId: m.flowID, ResetType: dexpb.FlowResetType_FLOW_RESET_TYPE_BEGINNING, Reason: "RSS measurement"})
	if err != nil {
		return fmt.Errorf("reset Flow: %w", err)
	}
	if reset.RunAttempt != 2 {
		return fmt.Errorf("expected run attempt 2, received %d", reset.RunAttempt)
	}
	log.Print("ResetFlow completed with run attempt 2")
	return nil
}

func waitForServer(ctx context.Context, connection *grpc.ClientConn) error {
	readinessContext, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	client := health.NewHealthClient(connection)
	for {
		response, err := client.Check(readinessContext, &health.HealthCheckRequest{Service: "dex.FlowService"})
		if err == nil && response.Status == health.HealthCheckResponse_SERVING {
			return nil
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-timer.C:
		case <-readinessContext.Done():
			timer.Stop()
			return fmt.Errorf("wait for server readiness: %w", readinessContext.Err())
		}
	}
}

func closeConnection(connection *grpc.ClientConn) {
	if err := connection.Close(); err != nil {
		log.Printf("close gRPC connection: %v", err)
	}
}

func textValue(text string) *dexpb.Value {
	return &dexpb.Value{Kind: &dexpb.Value_StringValue{StringValue: text}}
}
