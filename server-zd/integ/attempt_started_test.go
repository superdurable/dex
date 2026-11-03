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
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"github.com/superdurable/dex/server-zd/integ/internal/testconfig"
	"github.com/superdurable/dex/server-zd/integ/workflow/common"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"gopkg.in/yaml.v3"
)

const attemptStartedRecoveryStep = "RecoveryStep"

// scriptedPullWorker is one raw pull stream on the probe Flow type that a test drives frame by frame.
type scriptedPullWorker struct {
	stream     dexpb.WorkerTaskService_PollWorkerTaskClient
	cancel     context.CancelFunc
	registered *dexpb.PollWorkerTaskRegistered
	frames     chan *dexpb.PollWorkerTaskResponse
}

// restartableDexService is server-zd on a fixed address and data directory that a test can crash and start again.
type restartableDexService struct {
	address           string
	dataDirectory     string
	configurationPath string
	process           *binaryDexProcess
	FlowClient        dexpb.FlowServiceClient
}

// A Worker that dies after its mark committed is never replaced: the attempt fails and its recovery sees STARTED.
func TestAttemptStartedWorkerDeathRoutesToFailure(t *testing.T) {
	runtime := startPullDispatchService(t, serverZDTestConfig{
		Matching: &testconfig.MatchingConfig{DispatchWait: 5 * time.Second, StickyWait: 100 * time.Millisecond},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	first := openScriptedPullWorker(t, runtime.PublicAddress, "attempt-started-first-"+uuid.NewString())
	require.True(t, first.registered.GetSupportsAttemptStartedAck())
	flowID := "attempt-started-worker-death-" + uuid.NewString()
	require.NoError(t, startDispatchProbe(ctx, runtime.FlowClient, flowID, dispatchProbeStep,
		&dexpb.WorkerTarget{IsPullMode: true}, singleAttemptStepOptions()))
	task := first.nextTask(t)
	require.Equal(t, dispatchProbeStep, task.GetExecute().GetStepType())
	first.markAttemptStarted(t, task.GetTaskId())
	first.kill()

	second := openScriptedPullWorker(t, runtime.PublicAddress, "attempt-started-second-"+uuid.NewString())
	recoveryError := second.completeRecoveryStep(t)
	require.Equal(t, dexpb.StepAttemptProgress_STEP_ATTEMPT_PROGRESS_STARTED, recoveryError.GetAttemptProgress())
	requireFlowCompleted(t, ctx, runtime.FlowClient, flowID)
}

// A node restart while a marked attempt runs fails the attempt instead of dispatching it again.
func TestAttemptStartedNodeRestartRoutesToFailure(t *testing.T) {
	service := startRestartableDexService(t, serverZDTestConfig{
		Matching: &testconfig.MatchingConfig{DispatchWait: 5 * time.Second, StickyWait: 100 * time.Millisecond},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	first := openScriptedPullWorker(t, service.address, "attempt-started-restart-first-"+uuid.NewString())
	flowID := "attempt-started-restart-" + uuid.NewString()
	require.NoError(t, startDispatchProbe(ctx, service.FlowClient, flowID, dispatchProbeStep,
		&dexpb.WorkerTarget{IsPullMode: true}, singleAttemptStepOptions()))
	task := first.nextTask(t)
	first.markAttemptStarted(t, task.GetTaskId())

	service.crash(t)
	service.start(t)
	second := openScriptedPullWorker(t, service.address, "attempt-started-restart-second-"+uuid.NewString())
	recoveryError := second.completeRecoveryStep(t)
	require.Equal(t, "StartedAttemptInterrupted", recoveryError.GetErrorType())
	require.Equal(t, dexpb.StepAttemptProgress_STEP_ATTEMPT_PROGRESS_STARTED, recoveryError.GetAttemptProgress())
	requireFlowCompleted(t, ctx, service.FlowClient, flowID)
}

// A node restart while an unmarked attempt runs still dispatches it again at the same attempt.
func TestAttemptNotMarkedNodeRestartRedispatches(t *testing.T) {
	service := startRestartableDexService(t, serverZDTestConfig{
		Matching: &testconfig.MatchingConfig{DispatchWait: 5 * time.Second, StickyWait: 100 * time.Millisecond},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	first := openScriptedPullWorker(t, service.address, "attempt-unmarked-restart-first-"+uuid.NewString())
	flowID := "attempt-unmarked-restart-" + uuid.NewString()
	require.NoError(t, startDispatchProbe(ctx, service.FlowClient, flowID, dispatchProbeStep,
		&dexpb.WorkerTarget{IsPullMode: true}, singleAttemptStepOptions()))
	firstTask := first.nextTask(t)
	require.Equal(t, int32(1), firstTask.GetExecute().GetContext().GetAttempt())

	service.crash(t)
	service.start(t)
	second := openScriptedPullWorker(t, service.address, "attempt-unmarked-restart-second-"+uuid.NewString())
	task := second.nextTask(t)
	require.Equal(t, dispatchProbeStep, task.GetExecute().GetStepType())
	require.Equal(t, int32(1), task.GetExecute().GetContext().GetAttempt())
	second.completeExecute(t, task)
	requireFlowCompleted(t, ctx, service.FlowClient, flowID)
}

// An attempt that no Worker took within the dispatch wait reaches its recovery as NOT_ASSIGNED.
func TestAttemptNeverAssignedRecoveryProgress(t *testing.T) {
	runtime := startPullDispatchService(t, serverZDTestConfig{
		Matching: &testconfig.MatchingConfig{DispatchWait: 2 * time.Second, StickyWait: 100 * time.Millisecond},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	flowID := "attempt-never-assigned-" + uuid.NewString()
	require.NoError(t, startDispatchProbe(ctx, runtime.FlowClient, flowID, dispatchProbeStep,
		&dexpb.WorkerTarget{IsPullMode: true}, singleAttemptStepOptions()))
	waitForStepExecution(t, ctx, runtime.FlowClient, flowID, attemptStartedRecoveryStep)

	worker := openScriptedPullWorker(t, runtime.PublicAddress, "attempt-never-assigned-"+uuid.NewString())
	recoveryError := worker.completeRecoveryStep(t)
	require.Equal(t, dexpb.StepAttemptProgress_STEP_ATTEMPT_PROGRESS_NOT_ASSIGNED, recoveryError.GetAttemptProgress())
	require.Equal(t, "FLOW_ERROR_TYPE_WORKER_API_FAIL", recoveryError.GetErrorType())
	requireFlowCompleted(t, ctx, runtime.FlowClient, flowID)
}

// A Worker that dies after it received the attempt and before it marked it reaches recovery as ASSIGNED.
func TestAttemptAssignedNotStartedRecoveryProgress(t *testing.T) {
	runtime := startPullDispatchService(t, serverZDTestConfig{
		Matching: &testconfig.MatchingConfig{DispatchWait: 3 * time.Second, StickyWait: 100 * time.Millisecond},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	first := openScriptedPullWorker(t, runtime.PublicAddress, "attempt-assigned-first-"+uuid.NewString())
	flowID := "attempt-assigned-" + uuid.NewString()
	require.NoError(t, startDispatchProbe(ctx, runtime.FlowClient, flowID, dispatchProbeStep,
		&dexpb.WorkerTarget{IsPullMode: true}, singleAttemptStepOptions()))
	task := first.nextTask(t)
	require.Equal(t, dispatchProbeStep, task.GetExecute().GetStepType())
	first.kill()
	waitForStepExecution(t, ctx, runtime.FlowClient, flowID, attemptStartedRecoveryStep)

	second := openScriptedPullWorker(t, runtime.PublicAddress, "attempt-assigned-second-"+uuid.NewString())
	recoveryError := second.completeRecoveryStep(t)
	require.Equal(t, dexpb.StepAttemptProgress_STEP_ATTEMPT_PROGRESS_ASSIGNED, recoveryError.GetAttemptProgress())
	requireFlowCompleted(t, ctx, runtime.FlowClient, flowID)
}

// An attempt redelivered to a second Worker before it was marked runs there, at the same attempt.
func TestAttemptAssignedNotStartedRedeliversWithinDispatchWait(t *testing.T) {
	runtime := startPullDispatchService(t, serverZDTestConfig{
		Matching: &testconfig.MatchingConfig{DispatchWait: 10 * time.Second, StickyWait: 100 * time.Millisecond},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	first := openScriptedPullWorker(t, runtime.PublicAddress, "attempt-redeliver-first-"+uuid.NewString())
	flowID := "attempt-redeliver-" + uuid.NewString()
	require.NoError(t, startDispatchProbe(ctx, runtime.FlowClient, flowID, dispatchProbeStep,
		&dexpb.WorkerTarget{IsPullMode: true}, singleAttemptStepOptions()))
	require.Equal(t, dispatchProbeStep, first.nextTask(t).GetExecute().GetStepType())
	second := openScriptedPullWorker(t, runtime.PublicAddress, "attempt-redeliver-second-"+uuid.NewString())
	first.kill()

	task := second.nextTask(t)
	require.Equal(t, dispatchProbeStep, task.GetExecute().GetStepType())
	require.Equal(t, int32(1), task.GetExecute().GetContext().GetAttempt())
	second.markAttemptStarted(t, task.GetTaskId())
	second.completeExecute(t, task)
	requireFlowCompleted(t, ctx, runtime.FlowClient, flowID)
}

func singleAttemptStepOptions() *dexpb.StepOptions {
	return &dexpb.StepOptions{
		SkipWaitFor:                      true,
		ExecuteRetryPolicy:               &dexpb.RetryPolicy{MaximumAttempts: 1},
		ExecuteFailurePolicy:             dexpb.ExecuteMethodFailurePolicy_EXECUTE_METHOD_FAILURE_POLICY_PROCEED_TO_CONFIGURED_STEP,
		ExecuteFailureProceedStepType:    attemptStartedRecoveryStep,
		ExecuteFailureProceedStepOptions: &dexpb.StepOptions{SkipWaitFor: true},
	}
}

func waitForStepExecution(t *testing.T, ctx context.Context, client dexpb.FlowServiceClient, flowID string, stepType string) {
	t.Helper()
	require.Eventually(t, func() bool {
		state, err := client.GetFlowState(ctx, &dexpb.GetFlowStateRequest{FlowId: flowID})
		if err != nil {
			return false
		}
		for _, step := range userActiveStepExecutions(state) {
			if step.GetStepType() == stepType {
				return true
			}
		}
		return false
	}, 30*time.Second, 100*time.Millisecond)
}

func requireFlowCompleted(t *testing.T, ctx context.Context, client dexpb.FlowServiceClient, flowID string) {
	t.Helper()
	result, err := client.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{FlowId: flowID, WaitTimeSeconds: 20})
	require.NoError(t, err)
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_COMPLETED, result.GetFlowStatus(), result.GetErrorMessage())
}

func openScriptedPullWorker(t *testing.T, address string, workerID string) *scriptedPullWorker {
	t.Helper()
	connection, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, connection.Close()) })
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	stream, err := dexpb.NewWorkerTaskServiceClient(connection).PollWorkerTask(ctx)
	require.NoError(t, err)
	require.NoError(t, stream.Send(&dexpb.PollWorkerTaskRequest{Frame: &dexpb.PollWorkerTaskRequest_Register{
		Register: &dexpb.PollWorkerTaskRegister{Queue: dispatchProbeFlowType, AppWorkerId: workerID, FlowTypes: []string{dispatchProbeFlowType}},
	}}))
	registered, err := stream.Recv()
	require.NoError(t, err)
	require.NotNil(t, registered.GetRegistered())
	worker := &scriptedPullWorker{
		stream: stream, cancel: cancel, registered: registered.GetRegistered(),
		frames: make(chan *dexpb.PollWorkerTaskResponse, 16),
	}
	go worker.readFrames()
	return worker
}

func (worker *scriptedPullWorker) readFrames() {
	defer close(worker.frames)
	for {
		frame, err := worker.stream.Recv()
		if err != nil {
			return
		}
		if frame.GetPing() != nil {
			if err := worker.stream.Send(&dexpb.PollWorkerTaskRequest{Frame: &dexpb.PollWorkerTaskRequest_Pong{Pong: &dexpb.PollWorkerTaskPong{}}}); err != nil {
				return
			}
			continue
		}
		worker.frames <- frame
	}
}

func (worker *scriptedPullWorker) nextTask(t *testing.T) *dexpb.PollWorkerTask {
	t.Helper()
	for {
		frame := worker.nextFrame(t)
		if task := frame.GetTask(); task != nil {
			return task
		}
	}
}

func (worker *scriptedPullWorker) nextFrame(t *testing.T) *dexpb.PollWorkerTaskResponse {
	t.Helper()
	select {
	case frame, isOpen := <-worker.frames:
		require.True(t, isOpen, "the Worker stream closed")
		return frame
	case <-time.After(30 * time.Second):
		t.Fatal("timed out waiting for a server frame")
		return nil
	}
}

// markAttemptStarted sends the mark and returns once Dex acknowledged that it committed.
func (worker *scriptedPullWorker) markAttemptStarted(t *testing.T, taskID string) {
	t.Helper()
	require.NoError(t, worker.stream.Send(&dexpb.PollWorkerTaskRequest{Frame: &dexpb.PollWorkerTaskRequest_TaskFrame{
		TaskFrame: &dexpb.PollWorkerTaskFrame{TaskId: taskID, Output: &dexpb.PollWorkerTaskFrame_Execute{
			Execute: &dexpb.InvokeExecuteMethodOutput{Output: &dexpb.InvokeExecuteMethodOutput_AttemptStarted{
				AttemptStarted: &dexpb.StepMethodAttemptStarted{},
			}},
		}},
	}}))
	for {
		if ack := worker.nextFrame(t).GetAttemptStartedAck(); ack != nil {
			require.Equal(t, taskID, ack.GetTaskId())
			return
		}
	}
}

func (worker *scriptedPullWorker) completeExecute(t *testing.T, task *dexpb.PollWorkerTask) {
	t.Helper()
	flowID := task.GetExecute().GetContext().GetFlowId()
	require.NoError(t, worker.stream.Send(&dexpb.PollWorkerTaskRequest{Frame: &dexpb.PollWorkerTaskRequest_TaskFrame{
		TaskFrame: &dexpb.PollWorkerTaskFrame{TaskId: task.GetTaskId(), Output: &dexpb.PollWorkerTaskFrame_Execute{
			Execute: &dexpb.InvokeExecuteMethodOutput{Output: &dexpb.InvokeExecuteMethodOutput_Result{
				Result: &dexpb.InvokeExecuteMethodResponse{StepDecision: &dexpb.StepDecision{
					CloseDecision: common.GracefulCompleteDecision(stringValue(flowID)),
				}},
			}},
		}},
	}}))
}

// completeRecoveryStep requires the next task to be the recovery Step, completes the Flow, and returns its recovery error.
func (worker *scriptedPullWorker) completeRecoveryStep(t *testing.T) *dexpb.RecoveryErrorInfo {
	t.Helper()
	task := worker.nextTask(t)
	require.Equal(t, attemptStartedRecoveryStep, task.GetExecute().GetStepType(), "the failed attempt was dispatched again")
	recoveryError := task.GetExecute().GetContext().GetRecoveryError()
	require.NotNil(t, recoveryError)
	worker.completeExecute(t, task)
	return recoveryError
}

func (worker *scriptedPullWorker) kill() {
	worker.cancel()
}

func startRestartableDexService(t *testing.T, testConfig serverZDTestConfig) *restartableDexService {
	t.Helper()
	require.NotEmpty(t, *dexServerBinary, "set -dex-server-binary or DEX_SERVER_BINARY")
	configuration, unsupportedSetting := binaryDexConfig(testConfig)
	require.Empty(t, unsupportedSetting)
	dataDirectory := t.TempDir()
	address, err := reserveBinaryDexAddress()
	require.NoError(t, err)
	configurationBytes, err := yaml.Marshal(configuration)
	require.NoError(t, err)
	configurationPath := filepath.Join(dataDirectory, "integ-testconfig.yaml")
	require.NoError(t, os.WriteFile(configurationPath, configurationBytes, 0o600))
	service := &restartableDexService{address: address, dataDirectory: dataDirectory, configurationPath: configurationPath}
	t.Cleanup(func() {
		if service.process != nil {
			service.process.stop(t)
		}
	})
	service.start(t)
	connection, err := newDexClientConnection(address, testconfig.DefaultGrpcMaxMessageBytes)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, connection.Close()) })
	service.FlowClient = dexpb.NewFlowServiceClient(connection)
	return service
}

// start runs server-zd on the service's address and data directory, appending to one log file.
func (service *restartableDexService) start(t *testing.T) {
	t.Helper()
	outputPath := filepath.Join(service.dataDirectory, "server.log")
	outputFile, err := os.OpenFile(outputPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	require.NoError(t, err)
	command := exec.Command(*dexServerBinary, "dev", "--data-dir", service.dataDirectory,
		"--listen", service.address, "--config", service.configurationPath)
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Stdout = outputFile
	command.Stderr = outputFile
	require.NoError(t, command.Start())
	process := &binaryDexProcess{command: command, done: make(chan struct{})}
	go func() {
		process.waitErr = command.Wait()
		close(process.done)
	}()
	service.process = process
	t.Cleanup(func() {
		require.NoError(t, outputFile.Close())
		if t.Failed() {
			output, readErr := os.ReadFile(outputPath)
			if readErr == nil {
				t.Logf("Dex Server log:\n%s", output)
			}
		}
	})
	require.NoError(t, waitForBinaryDex(service.address, process, 15*time.Second))
}

// crash kills the server process group without a graceful shutdown, as a node failure would.
func (service *restartableDexService) crash(t *testing.T) {
	t.Helper()
	require.NoError(t, syscall.Kill(-service.process.command.Process.Pid, syscall.SIGKILL))
	<-service.process.done
	service.process = nil
}
