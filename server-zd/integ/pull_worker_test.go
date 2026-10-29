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
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"github.com/superdurable/dex/server-zd/integ/internal/testconfig"
	"github.com/superdurable/dex/server-zd/integ/workflow/common"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

type pullRoutingStateProbe struct {
	test     *testing.T
	ctx      context.Context
	client   dexpb.FlowServiceClient
	flowID   string
	workerID string
	source   dexpb.PullRoutingSource
}

type pullTaskObservation struct {
	queue   string
	method  string
	flowID  string
	attempt int32
}

type pullExecuteBehavior int

const (
	pullExecuteDefault pullExecuteBehavior = iota
	pullExecuteSilentFirst
	pullExecuteCheckpointFirst
	pullExecutePeriodicHeartbeat
)

type rawPullWorker struct {
	shouldStartSecondStep   bool
	events                  chan pullTaskObservation
	started                 chan pullTaskObservation
	canceled                chan string
	errors                  chan error
	cancellations           []context.CancelFunc
	queueCancels            map[string]context.CancelFunc
	rpcError                *dexpb.WorkerErrorResponse
	rpcAttributeKey         string
	rpcAsyncStep            bool
	disconnectOnWaitFor     bool
	spawnSubFlow            bool
	hasInvalidSubFlowTarget bool
	spawnParallel           bool
	blockParallel           bool
	sendProgress            bool
	holdWaitFor             bool
	releaseParallel         chan struct{}
	rejectNextStickyTask    chan struct{}
	rejectedStickyTasks     chan pullTaskObservation
	executeBehavior         pullExecuteBehavior
	restoredHeartbeats      chan string
	recoveryErrors          chan *dexpb.RecoveryErrorInfo
	unknownStepType         string
	unknownExecuteStepType  string
	unknownRPCName          string
	unknownRejections       chan pullTaskObservation
}

type rawPullWorkerConfig struct {
	shouldStartSecondStep      bool
	shouldCaptureRecoveryError bool
	unknownStepType            string
	unknownExecuteStepType     string
	unknownRPCName             string
	rpcAttributeKey            string
	rpcAsyncStep               bool
}

func startUnknownRejectingPullWorker(t *testing.T, address string, flowType string, workerID string, configuration rawPullWorkerConfig) *rawPullWorker {
	return newRawPullWorkerWithExecuteBehavior(t, address, flowType, workerID, nil, false, false, false, false, false, false, false, pullExecuteDefault, configuration)
}

func startStateWritingPullWorker(t *testing.T, address string, flowType string, workerID string, attributeKey string) *rawPullWorker {
	return newRawPullWorkerWithExecuteBehavior(t, address, flowType, workerID, nil, false, false, false, false, false, false, false, pullExecuteDefault, rawPullWorkerConfig{rpcAttributeKey: attributeKey})
}

func startHeartbeatPullWorker(t *testing.T, address string, flowType string, behavior pullExecuteBehavior) *rawPullWorker {
	return newRawPullWorkerWithExecuteBehavior(t, address, flowType, "heartbeat-worker-"+uuid.NewString(), nil, false, false, false, false, false, false, false, behavior)
}

func startRawPullWorker(t *testing.T, address string, flowType string, workerID string, rpcError *dexpb.WorkerErrorResponse) *rawPullWorker {
	return newRawPullWorker(t, address, flowType, workerID, rpcError, false, false, false, false, false, false, false)
}

func startRecoveryPullWorker(t *testing.T, address string, flowType string, workerID string) *rawPullWorker {
	return newRawPullWorkerWithExecuteBehavior(t, address, flowType, workerID, nil, false, false, false, false, false, false, false, pullExecuteDefault,
		rawPullWorkerConfig{shouldCaptureRecoveryError: true})
}

func startProgressPullWorker(t *testing.T, address string, flowType string, workerID string) *rawPullWorker {
	return newRawPullWorker(t, address, flowType, workerID, nil, false, false, false, false, true, false, false)
}

func startDisconnectingPullWorker(t *testing.T, address string, flowType string, workerID string) *rawPullWorker {
	return newRawPullWorker(t, address, flowType, workerID, nil, true, false, false, false, false, false, false)
}

func startSubFlowPullWorker(t *testing.T, address string, flowType string, workerID string) *rawPullWorker {
	return newRawPullWorker(t, address, flowType, workerID, nil, false, true, false, false, false, false, false)
}

func startInvalidSubFlowPullWorker(t *testing.T, address string, flowType string, workerID string) *rawPullWorker {
	return newRawPullWorker(t, address, flowType, workerID, nil, false, true, false, false, false, false, true)
}

func startParallelPullWorker(t *testing.T, address string, flowType string, workerID string, blockParallel bool) *rawPullWorker {
	return newRawPullWorker(t, address, flowType, workerID, nil, false, false, true, blockParallel, false, false, false)
}

func startCancelPullWorker(t *testing.T, address string, flowType string, workerID string) *rawPullWorker {
	return newRawPullWorker(t, address, flowType, workerID, nil, false, false, false, false, false, true, false)
}

func newRawPullWorker(t *testing.T, address string, flowType string, workerID string, rpcError *dexpb.WorkerErrorResponse, disconnectOnWaitFor bool, spawnSubFlow bool, spawnParallel bool, blockParallel bool, sendProgress bool, holdWaitFor bool, hasInvalidSubFlowTarget bool) *rawPullWorker {
	return newRawPullWorkerWithExecuteBehavior(t, address, flowType, workerID, rpcError, disconnectOnWaitFor, spawnSubFlow, spawnParallel, blockParallel, sendProgress, holdWaitFor, hasInvalidSubFlowTarget, pullExecuteDefault)
}

func newRawPullWorkerWithExecuteBehavior(t *testing.T, address string, flowType string, workerID string, rpcError *dexpb.WorkerErrorResponse, disconnectOnWaitFor bool, spawnSubFlow bool, spawnParallel bool, blockParallel bool, sendProgress bool, holdWaitFor bool, hasInvalidSubFlowTarget bool, executeBehavior pullExecuteBehavior, configurations ...rawPullWorkerConfig) *rawPullWorker {
	t.Helper()
	connection, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, connection.Close()) })
	configuration := rawPullWorkerConfig{}
	if len(configurations) > 0 {
		configuration = configurations[0]
	}
	worker := &rawPullWorker{
		shouldStartSecondStep: configuration.shouldStartSecondStep,
		events:                make(chan pullTaskObservation, 16), started: make(chan pullTaskObservation, 16), canceled: make(chan string, 4),
		errors: make(chan error, 4), queueCancels: map[string]context.CancelFunc{}, releaseParallel: make(chan struct{}),
		rejectNextStickyTask: make(chan struct{}, 1), rejectedStickyTasks: make(chan pullTaskObservation, 1),
		restoredHeartbeats: make(chan string, 2), executeBehavior: executeBehavior,
		rpcError: rpcError, disconnectOnWaitFor: disconnectOnWaitFor, spawnSubFlow: spawnSubFlow,
		rpcAttributeKey: configuration.rpcAttributeKey,
		rpcAsyncStep:    configuration.rpcAsyncStep,
		spawnParallel:   spawnParallel, blockParallel: blockParallel, sendProgress: sendProgress, holdWaitFor: holdWaitFor,
		hasInvalidSubFlowTarget: hasInvalidSubFlowTarget,
		unknownStepType:         configuration.unknownStepType, unknownExecuteStepType: configuration.unknownExecuteStepType,
		unknownRPCName:    configuration.unknownRPCName,
		unknownRejections: make(chan pullTaskObservation, 16),
	}
	if configuration.shouldCaptureRecoveryError {
		worker.recoveryErrors = make(chan *dexpb.RecoveryErrorInfo, 1)
	}
	for _, queue := range []string{flowType, workerID} {
		ctx, cancel := context.WithCancel(context.Background())
		worker.cancellations = append(worker.cancellations, cancel)
		worker.queueCancels[queue] = cancel
		t.Cleanup(cancel)
		stream, err := dexpb.NewWorkerTaskServiceClient(connection).PollWorkerTask(ctx)
		require.NoError(t, err)
		require.NoError(t, stream.Send(&dexpb.PollWorkerTaskRequest{Frame: &dexpb.PollWorkerTaskRequest_Register{
			Register: &dexpb.PollWorkerTaskRegister{Queue: queue, AppWorkerId: workerID, FlowTypes: []string{flowType}},
		}}))
		registered, err := stream.Recv()
		require.NoError(t, err)
		require.NotNil(t, registered.GetRegistered())
		go worker.serve(queue, stream)
	}
	return worker
}

func (w *rawPullWorker) serve(queue string, stream dexpb.WorkerTaskService_PollWorkerTaskClient) {
	heldTaskCancels := make(map[string]context.CancelFunc)
	defer func() {
		for _, cancel := range heldTaskCancels {
			cancel()
		}
	}()
	for {
		frame, err := stream.Recv()
		if err != nil {
			return
		}
		if frame.GetPing() != nil {
			if err := stream.Send(&dexpb.PollWorkerTaskRequest{Frame: &dexpb.PollWorkerTaskRequest_Pong{Pong: &dexpb.PollWorkerTaskPong{}}}); err != nil {
				w.errors <- err
				return
			}
			continue
		}
		if cancellation := frame.GetCancelTask(); cancellation != nil {
			if cancel, exists := heldTaskCancels[cancellation.GetTaskId()]; exists {
				cancel()
				delete(heldTaskCancels, cancellation.GetTaskId())
			}
			w.canceled <- cancellation.GetTaskId()
			continue
		}
		task := frame.GetTask()
		if task == nil {
			continue
		}
		if reason := w.unknownTaskReason(task); reason != dexpb.PollWorkerTaskRejectReason_POLL_WORKER_TASK_REJECT_REASON_UNSPECIFIED {
			if err := stream.Send(&dexpb.PollWorkerTaskRequest{Frame: &dexpb.PollWorkerTaskRequest_Reject{Reject: &dexpb.PollWorkerTaskReject{TaskId: task.GetTaskId(), Reason: reason}}}); err != nil {
				w.errors <- err
				return
			}
			w.unknownRejections <- pullTaskObservation{queue: queue, flowID: taskFlowID(task), attempt: taskAttempt(task)}
			continue
		}
		if queue != taskFlowType(task) {
			select {
			case <-w.rejectNextStickyTask:
				if err := stream.Send(&dexpb.PollWorkerTaskRequest{Frame: &dexpb.PollWorkerTaskRequest_Reject{Reject: &dexpb.PollWorkerTaskReject{TaskId: task.GetTaskId(), Reason: dexpb.PollWorkerTaskRejectReason_POLL_WORKER_TASK_REJECT_REASON_NO_CAPACITY}}}); err != nil {
					w.errors <- err
					return
				}
				w.rejectedStickyTasks <- pullTaskObservation{queue: queue}
				continue
			default:
			}
		}
		method := ""
		observation := pullTaskObservation{queue: queue}
		var output *dexpb.PollWorkerTaskFrame
		switch request := task.GetRequest().(type) {
		case *dexpb.PollWorkerTask_WaitFor:
			method = "wait_for"
			if w.rpcError != nil {
				output = &dexpb.PollWorkerTaskFrame{TaskId: task.GetTaskId(), Output: &dexpb.PollWorkerTaskFrame_WorkerError{WorkerError: w.rpcError}}
				observation = pullTaskObservation{queue: queue, method: method, flowID: request.WaitFor.GetContext().GetFlowId(), attempt: request.WaitFor.GetContext().GetAttempt()}
				break
			}
			if w.holdWaitFor {
				heldContext, cancel := context.WithCancel(context.Background())
				heldTaskCancels[task.GetTaskId()] = cancel
				go w.sendHeldWaitForHeartbeats(heldContext, stream, task.GetTaskId())
				w.events <- pullTaskObservation{queue: queue, method: method, flowID: request.WaitFor.GetContext().GetFlowId(), attempt: request.WaitFor.GetContext().GetAttempt()}
				continue
			}
			if w.disconnectOnWaitFor {
				w.events <- pullTaskObservation{queue: queue, method: method, flowID: request.WaitFor.GetContext().GetFlowId(), attempt: request.WaitFor.GetContext().GetAttempt()}
				w.stop()
				return
			}
			waitingCondition := &dexpb.WaitingCondition{
				WaitingConditionType: dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ALL_COMPLETED,
				ChannelConditions:    []*dexpb.ChannelCondition{{ChannelName: dispatchProbeChannel}},
			}
			if w.spawnSubFlow {
				waitingCondition.ChannelConditions = nil
				waitingCondition.SubFlowConditions = []*dexpb.SubFlowCondition{{
					SubFlowType: request.WaitFor.GetFlowType(), StartStepType: "ChildStep",
					StepOptions: &dexpb.StepOptions{SkipWaitFor: true},
				}}
				if w.hasInvalidSubFlowTarget {
					waitingCondition.SubFlowConditions[0].Options = &dexpb.SubFlowOptions{FlowConfigOverride: &dexpb.FlowConfig{
						WorkerTarget: &dexpb.WorkerTarget{},
					}}
				}
			}
			output = &dexpb.PollWorkerTaskFrame{TaskId: task.GetTaskId(), Output: &dexpb.PollWorkerTaskFrame_WaitFor{
				WaitFor: &dexpb.InvokeWaitForMethodOutput{Output: &dexpb.InvokeWaitForMethodOutput_Result{
					Result: &dexpb.InvokeWaitForMethodResponse{WaitingCondition: waitingCondition},
				}},
			}}
			observation = pullTaskObservation{queue: queue, method: method, flowID: request.WaitFor.GetContext().GetFlowId(), attempt: request.WaitFor.GetContext().GetAttempt()}
		case *dexpb.PollWorkerTask_Execute:
			method = "execute"
			if recoveryError := request.Execute.GetContext().GetRecoveryError(); recoveryError != nil && w.recoveryErrors != nil {
				w.recoveryErrors <- recoveryError
			}
			decision := &dexpb.StepDecision{
				CloseDecision: common.GracefulCompleteDecision(stringValue(request.Execute.GetContext().GetFlowId())),
			}
			if w.shouldStartSecondStep && request.Execute.GetStepType() == dispatchProbeStep {
				decision = &dexpb.StepDecision{NextSteps: []*dexpb.StepMovement{{StepType: "SecondStep"}}}
			}
			if w.spawnParallel {
				decision.CloseDecision = &dexpb.CloseDecision{
					CloseDecisionType: dexpb.CloseDecisionType_CLOSE_DECISION_TYPE_DEAD_END,
				}
				if request.Execute.GetStepType() == dispatchProbeStep {
					decision.CloseDecision = nil
					decision.NextSteps = []*dexpb.StepMovement{
						{StepType: "ParallelFirst", StepOptions: &dexpb.StepOptions{SkipWaitFor: true}},
						{StepType: "ParallelSecond", StepOptions: &dexpb.StepOptions{SkipWaitFor: true}},
					}
				}
			}
			output = &dexpb.PollWorkerTaskFrame{TaskId: task.GetTaskId(), Output: &dexpb.PollWorkerTaskFrame_Execute{
				Execute: &dexpb.InvokeExecuteMethodOutput{Output: &dexpb.InvokeExecuteMethodOutput_Result{
					Result: &dexpb.InvokeExecuteMethodResponse{StepDecision: decision},
				}},
			}}
			observation = pullTaskObservation{queue: queue, method: method, flowID: request.Execute.GetContext().GetFlowId(), attempt: request.Execute.GetContext().GetAttempt()}
			if observation.attempt == 1 && w.executeBehavior == pullExecuteSilentFirst {
				w.events <- observation
				continue
			}
			if observation.attempt == 1 && w.executeBehavior == pullExecuteCheckpointFirst {
				heartbeat := &dexpb.PollWorkerTaskFrame{TaskId: task.GetTaskId(), Output: &dexpb.PollWorkerTaskFrame_Execute{
					Execute: &dexpb.InvokeExecuteMethodOutput{Output: &dexpb.InvokeExecuteMethodOutput_Heartbeat{
						Heartbeat: &dexpb.StepMethodHeartbeat{Value: stringValue("pull-checkpoint")},
					}},
				}}
				if err := stream.Send(&dexpb.PollWorkerTaskRequest{Frame: &dexpb.PollWorkerTaskRequest_TaskFrame{TaskFrame: heartbeat}}); err != nil {
					w.errors <- err
					return
				}
				w.events <- observation
				continue
			}
			if observation.attempt > 1 && w.executeBehavior == pullExecuteCheckpointFirst {
				w.restoredHeartbeats <- request.Execute.GetContext().GetLastHeartbeatValue().GetStringValue()
			}
			if w.executeBehavior == pullExecutePeriodicHeartbeat {
				if err := w.sendPeriodicExecuteHeartbeats(stream, task.GetTaskId()); err != nil {
					w.errors <- err
					return
				}
			}
			if w.blockParallel && request.Execute.GetStepType() != dispatchProbeStep {
				w.started <- observation
				<-w.releaseParallel
			}
		case *dexpb.PollWorkerTask_Rpc:
			method = "rpc"
			if w.rpcError != nil {
				output = &dexpb.PollWorkerTaskFrame{TaskId: task.GetTaskId(), Output: &dexpb.PollWorkerTaskFrame_WorkerError{WorkerError: w.rpcError}}
			} else {
				rpcResponse := &dexpb.InvokeWorkerRPCResponse{Output: stringValue("pull-rpc-ok")}
				if w.rpcAttributeKey != "" {
					rpcResponse.UpsertAttributes = []*dexpb.AttributeWrite{{Key: w.rpcAttributeKey, Value: stringValue("written")}}
					stepOptions := &dexpb.StepOptions{SkipWaitFor: true}
					if w.rpcAsyncStep {
						stepOptions.ExecuteDurabilityOverride = dexpb.StepDurability_STEP_DURABILITY_ASYNC
					}
					rpcResponse.StepDecision = &dexpb.StepDecision{NextSteps: []*dexpb.StepMovement{{StepType: dispatchProbeStep, StepOptions: stepOptions}}}
				}
				output = &dexpb.PollWorkerTaskFrame{TaskId: task.GetTaskId(), Output: &dexpb.PollWorkerTaskFrame_Rpc{
					Rpc: rpcResponse,
				}}
			}
			observation = pullTaskObservation{queue: queue, method: method, flowID: request.Rpc.GetContext().GetFlowId(), attempt: request.Rpc.GetContext().GetAttempt()}
		default:
			w.errors <- fmt.Errorf("unexpected pull task request %T", task.GetRequest())
			return
		}
		if w.sendProgress && method != "rpc" {
			if err := w.sendProgressFrames(stream, task.GetTaskId(), method); err != nil {
				w.errors <- err
				return
			}
		}
		if err := stream.Send(&dexpb.PollWorkerTaskRequest{Frame: &dexpb.PollWorkerTaskRequest_TaskFrame{TaskFrame: output}}); err != nil {
			w.errors <- err
			return
		}
		w.events <- observation
	}
}

func (w *rawPullWorker) unknownTaskReason(task *dexpb.PollWorkerTask) dexpb.PollWorkerTaskRejectReason {
	if request := task.GetExecute(); request != nil && request.GetStepType() == w.unknownExecuteStepType && w.unknownExecuteStepType != "" {
		return dexpb.PollWorkerTaskRejectReason_POLL_WORKER_TASK_REJECT_REASON_UNKNOWN_STEP
	}
	if w.unknownStepType != "" {
		if request := task.GetWaitFor(); request != nil && request.GetStepType() == w.unknownStepType {
			return dexpb.PollWorkerTaskRejectReason_POLL_WORKER_TASK_REJECT_REASON_UNKNOWN_STEP
		}
		if request := task.GetExecute(); request != nil && request.GetStepType() == w.unknownStepType {
			return dexpb.PollWorkerTaskRejectReason_POLL_WORKER_TASK_REJECT_REASON_UNKNOWN_STEP
		}
	}
	if request := task.GetRpc(); request != nil && request.GetRpcName() == w.unknownRPCName && w.unknownRPCName != "" {
		return dexpb.PollWorkerTaskRejectReason_POLL_WORKER_TASK_REJECT_REASON_UNKNOWN_RPC
	}
	return dexpb.PollWorkerTaskRejectReason_POLL_WORKER_TASK_REJECT_REASON_UNSPECIFIED
}

func (w *rawPullWorker) sendHeldWaitForHeartbeats(ctx context.Context, stream dexpb.WorkerTaskService_PollWorkerTaskClient, taskID string) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			frame := &dexpb.PollWorkerTaskFrame{TaskId: taskID, Output: &dexpb.PollWorkerTaskFrame_WaitFor{
				WaitFor: &dexpb.InvokeWaitForMethodOutput{Output: &dexpb.InvokeWaitForMethodOutput_Heartbeat{Heartbeat: &dexpb.StepMethodHeartbeat{}}},
			}}
			if err := stream.Send(&dexpb.PollWorkerTaskRequest{Frame: &dexpb.PollWorkerTaskRequest_TaskFrame{TaskFrame: frame}}); err != nil {
				w.errors <- err
				return
			}
		}
	}
}

func (w *rawPullWorker) sendPeriodicExecuteHeartbeats(stream dexpb.WorkerTaskService_PollWorkerTaskClient, taskID string) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	// Heartbeat past the heartbeat timeout so only the heartbeats keep the attempt alive.
	for heartbeatCount := 0; heartbeatCount < int(pullStepHeartbeatTimeout()/time.Second)+2; heartbeatCount++ {
		<-ticker.C
		frame := &dexpb.PollWorkerTaskFrame{TaskId: taskID, Output: &dexpb.PollWorkerTaskFrame_Execute{
			Execute: &dexpb.InvokeExecuteMethodOutput{Output: &dexpb.InvokeExecuteMethodOutput_Heartbeat{Heartbeat: &dexpb.StepMethodHeartbeat{}}},
		}}
		if err := stream.Send(&dexpb.PollWorkerTaskRequest{Frame: &dexpb.PollWorkerTaskRequest_TaskFrame{TaskFrame: frame}}); err != nil {
			return err
		}
	}
	return nil
}

func taskFlowType(task *dexpb.PollWorkerTask) string {
	switch request := task.GetRequest().(type) {
	case *dexpb.PollWorkerTask_WaitFor:
		return request.WaitFor.GetFlowType()
	case *dexpb.PollWorkerTask_Execute:
		return request.Execute.GetFlowType()
	case *dexpb.PollWorkerTask_Rpc:
		return request.Rpc.GetFlowType()
	default:
		return ""
	}
}

func taskFlowID(task *dexpb.PollWorkerTask) string {
	switch request := task.GetRequest().(type) {
	case *dexpb.PollWorkerTask_WaitFor:
		return request.WaitFor.GetContext().GetFlowId()
	case *dexpb.PollWorkerTask_Execute:
		return request.Execute.GetContext().GetFlowId()
	case *dexpb.PollWorkerTask_Rpc:
		return request.Rpc.GetContext().GetFlowId()
	default:
		return ""
	}
}

func taskAttempt(task *dexpb.PollWorkerTask) int32 {
	switch request := task.GetRequest().(type) {
	case *dexpb.PollWorkerTask_WaitFor:
		return request.WaitFor.GetContext().GetAttempt()
	case *dexpb.PollWorkerTask_Execute:
		return request.Execute.GetContext().GetAttempt()
	case *dexpb.PollWorkerTask_Rpc:
		return request.Rpc.GetContext().GetAttempt()
	default:
		return 0
	}
}

func (w *rawPullWorker) sendProgressFrames(
	stream dexpb.WorkerTaskService_PollWorkerTaskClient, taskID string, method string,
) error {
	var heartbeat *dexpb.PollWorkerTaskFrame
	var streamWrite *dexpb.PollWorkerTaskFrame
	progress := &dexpb.StepStreamWrite{
		StreamName: method + "-progress", StreamCapacityBytes: 1 << 20,
		Value: stringValue(method + "-progress"),
	}
	if method == "wait_for" {
		heartbeat = &dexpb.PollWorkerTaskFrame{TaskId: taskID, Output: &dexpb.PollWorkerTaskFrame_WaitFor{
			WaitFor: &dexpb.InvokeWaitForMethodOutput{Output: &dexpb.InvokeWaitForMethodOutput_Heartbeat{
				Heartbeat: &dexpb.StepMethodHeartbeat{Value: stringValue("wait-checkpoint")},
			}},
		}}
		streamWrite = &dexpb.PollWorkerTaskFrame{TaskId: taskID, Output: &dexpb.PollWorkerTaskFrame_WaitFor{
			WaitFor: &dexpb.InvokeWaitForMethodOutput{Output: &dexpb.InvokeWaitForMethodOutput_StreamWrite{StreamWrite: progress}},
		}}
	} else {
		heartbeat = &dexpb.PollWorkerTaskFrame{TaskId: taskID, Output: &dexpb.PollWorkerTaskFrame_Execute{
			Execute: &dexpb.InvokeExecuteMethodOutput{Output: &dexpb.InvokeExecuteMethodOutput_Heartbeat{
				Heartbeat: &dexpb.StepMethodHeartbeat{Value: stringValue("execute-checkpoint")},
			}},
		}}
		streamWrite = &dexpb.PollWorkerTaskFrame{TaskId: taskID, Output: &dexpb.PollWorkerTaskFrame_Execute{
			Execute: &dexpb.InvokeExecuteMethodOutput{Output: &dexpb.InvokeExecuteMethodOutput_StreamWrite{StreamWrite: progress}},
		}}
	}
	for _, frame := range []*dexpb.PollWorkerTaskFrame{heartbeat, streamWrite} {
		if err := stream.Send(&dexpb.PollWorkerTaskRequest{Frame: &dexpb.PollWorkerTaskRequest_TaskFrame{TaskFrame: frame}}); err != nil {
			return err
		}
	}
	return nil
}

func (w *rawPullWorker) stop() {
	for _, cancel := range w.cancellations {
		cancel()
	}
}

func (w *rawPullWorker) stopQueue(queue string) {
	w.queueCancels[queue]()
}

func TestPullWorkerDispatchTemporal(t *testing.T) {
	testPullWorkerDispatch(t)
}

func TestPullWorkerCancellationTemporal(t *testing.T) {
	testPullWorkerCancellation(t)
}

func TestPullWorkerHeartbeatTimeoutTemporal(t *testing.T) {
	testPullWorkerHeartbeatTimeout(t)
}

func testPullWorkerHeartbeatTimeout(t *testing.T) {
	t.Helper()
	heartbeatTimeout := pullStepHeartbeatTimeout()
	for _, testCase := range []struct {
		name     string
		behavior pullExecuteBehavior
	}{
		{name: "silent_assigned_worker", behavior: pullExecuteSilentFirst},
		{name: "checkpoint_restored_on_retry", behavior: pullExecuteCheckpointFirst},
		{name: "periodic_worker_heartbeat", behavior: pullExecutePeriodicHeartbeat},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			runtime := startPullDispatchService(t, serverZDTestConfig{

				Matching:       &testconfig.MatchingConfig{DispatchWait: heartbeatTimeout + 4*time.Second, StickyWait: 100 * time.Millisecond},
				MaxWaitSeconds: 30,
			})
			worker := startHeartbeatPullWorker(t, runtime.PublicAddress, dispatchProbeFlowType, testCase.behavior)
			ctx, cancel := context.WithTimeout(context.Background(), 23*time.Second+heartbeatTimeout)
			defer cancel()
			flowID := "pull-heartbeat-" + uuid.NewString()
			stepOptions := &dexpb.StepOptions{
				SkipWaitFor: true, HeartbeatTimeoutSeconds: int32(heartbeatTimeout / time.Second),
				ExecuteRetryPolicy: &dexpb.RetryPolicy{InitialIntervalSeconds: 1, MaximumIntervalSeconds: 1, MaximumAttempts: 2},
			}
			require.NoError(t, startDispatchProbe(ctx, runtime.FlowClient, flowID, dispatchProbeStep,
				&dexpb.WorkerTarget{IsPullMode: true}, stepOptions))
			if testCase.behavior != pullExecutePeriodicHeartbeat {
				first := waitForPullTask(t, worker, flowID)
				require.Equal(t, "execute", first.method)
				require.Equal(t, int32(1), first.attempt)
				replacement := startHeartbeatPullWorker(t, runtime.PublicAddress, dispatchProbeFlowType, testCase.behavior)
				startedAt := time.Now()
				second := waitForPullTask(t, replacement, flowID)
				require.Equal(t, "execute", second.method)
				require.Equal(t, int32(2), second.attempt)
				require.GreaterOrEqual(t, time.Since(startedAt), heartbeatTimeout-500*time.Millisecond)
				require.Less(t, time.Since(startedAt), heartbeatTimeout+6*time.Second)
				if testCase.behavior == pullExecuteCheckpointFirst {
					require.Equal(t, "pull-checkpoint", <-replacement.restoredHeartbeats)
				}
			}
			result, err := runtime.FlowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{FlowId: flowID, WaitTimeSeconds: 20})
			require.NoError(t, err)
			require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_COMPLETED, result.GetFlowStatus(), result.GetErrorMessage())
			if testCase.behavior == pullExecutePeriodicHeartbeat {
				observed := waitForPullTask(t, worker, flowID)
				require.Equal(t, int32(1), observed.attempt)
			}
		})
	}
	t.Run("unassigned_wait", func(t *testing.T) {
		runtime := startPullDispatchService(t, serverZDTestConfig{

			Matching:       &testconfig.MatchingConfig{DispatchWait: heartbeatTimeout + 4*time.Second, StickyWait: 100 * time.Millisecond},
			MaxWaitSeconds: 30,
		})
		ctx, cancel := context.WithTimeout(context.Background(), 18*time.Second+heartbeatTimeout)
		defer cancel()
		flowID := "pull-unassigned-" + uuid.NewString()
		require.NoError(t, startDispatchProbe(ctx, runtime.FlowClient, flowID, dispatchProbeStep,
			&dexpb.WorkerTarget{IsPullMode: true}, &dexpb.StepOptions{
				SkipWaitFor: true, HeartbeatTimeoutSeconds: int32(heartbeatTimeout / time.Second),
				ExecuteRetryPolicy: &dexpb.RetryPolicy{MaximumAttempts: 1},
			}))
		select {
		case <-time.After(heartbeatTimeout + 1500*time.Millisecond):
		case <-ctx.Done():
			t.Fatal("timed out while waiting to register Worker")
		}
		worker := startHeartbeatPullWorker(t, runtime.PublicAddress, dispatchProbeFlowType, pullExecuteDefault)
		observed := waitForPullTask(t, worker, flowID)
		require.Equal(t, int32(1), observed.attempt)
		result, err := runtime.FlowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{FlowId: flowID, WaitTimeSeconds: 15})
		require.NoError(t, err)
		require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_COMPLETED, result.GetFlowStatus(), result.GetErrorMessage())
	})
}

func testPullWorkerCancellation(t *testing.T) {
	t.Helper()
	runtime := startPullDispatchService(t, serverZDTestConfig{Matching: &testconfig.MatchingConfig{}})
	worker := startCancelPullWorker(t, runtime.PublicAddress, dispatchProbeFlowType, "cancel-worker-"+uuid.NewString())
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	flowID := "pull-cancel-" + uuid.NewString()
	require.NoError(t, startDispatchProbe(ctx, runtime.FlowClient, flowID, dispatchProbeStep, &dexpb.WorkerTarget{IsPullMode: true}, nil))
	require.Equal(t, "wait_for", waitForPullTask(t, worker, flowID).method)
	_, err := runtime.FlowClient.StopFlow(ctx, &dexpb.StopFlowRequest{FlowId: flowID})
	require.NoError(t, err)
	select {
	case taskID := <-worker.canceled:
		require.NotEmpty(t, taskID)
	case <-ctx.Done():
		t.Fatal("pull Worker did not receive CancelTask")
	}
}

func TestPullWorkerUnavailableTemporal(t *testing.T) {
	testPullWorkerUnavailable(t)
}

func TestPullDispatchFailureTextTemporal(t *testing.T) {
	testPullDispatchFailureText(t)
}

func testPullDispatchFailureText(t *testing.T) {
	const expectedDetail = "no Worker available for queue " + dispatchProbeFlowType + ": no Worker accepted the task"

	t.Run("recovery Step", func(t *testing.T) {
		runtime := startPullDispatchService(t, serverZDTestConfig{

			Matching: &testconfig.MatchingConfig{DispatchWait: 5 * time.Second},
		})
		ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
		defer cancel()
		flowID := "pull-recovery-failure-" + uuid.NewString()
		stepOptions := &dexpb.StepOptions{
			WaitForRetryPolicy:   &dexpb.RetryPolicy{MaximumAttempts: 1},
			WaitForFailurePolicy: dexpb.WaitForMethodFailurePolicy_WAIT_FOR_METHOD_FAILURE_POLICY_PROCEED_ON_FAILURE,
		}
		require.NoError(t, startDispatchProbe(ctx, runtime.FlowClient, flowID, dispatchProbeStep,
			&dexpb.WorkerTarget{IsPullMode: true}, stepOptions))
		summary, err := runtime.FlowClient.GetFlowSummary(ctx, &dexpb.GetFlowSummaryRequest{FlowId: flowID})
		require.NoError(t, err)
		require.Eventually(t, func() bool {
			history, historyErr := runtime.FlowClient.GetHistoryEvents(ctx, &dexpb.GetHistoryEventsRequest{
				FlowId: flowID, RunId: summary.GetFlowExecutionId().GetRunId(), EstimatePageSize: 100,
			})
			if historyErr != nil {
				return false
			}
			for _, event := range history.GetEvents() {
				if event.GetStepWaitForFailed() != nil {
					return true
				}
			}
			return false
		}, 20*time.Second, 100*time.Millisecond)
		worker := startRecoveryPullWorker(t, runtime.PublicAddress, dispatchProbeFlowType, "pull-recovery-"+uuid.NewString())
		result, err := runtime.FlowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{FlowId: flowID, WaitTimeSeconds: 25})
		require.NoError(t, err)
		require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_COMPLETED, result.GetFlowStatus(), result.GetErrorMessage())
		select {
		case recoveryError := <-worker.recoveryErrors:
			require.Equal(t, expectedDetail, recoveryError.GetDetail())
		case <-ctx.Done():
			t.Fatal("recovery Step did not receive a recovery error")
		}
	})
}

func TestPullWorkerStickyFallbackTemporal(t *testing.T) {
	testPullWorkerStickyFallback(t)
}

func TestPullWorkerAppearsDuringRetriesTemporal(t *testing.T) {
	testPullWorkerAppearsDuringRetries(t)
}

func TestPullWorkerDisconnectTemporal(t *testing.T) {
	testPullWorkerDisconnect(t)
}

func TestPullWorkerStickyCapacityRecoveryTemporal(t *testing.T) {
	testPullWorkerStickyCapacityRecovery(t)
}

func TestPullSubFlowStartsWithoutStickyQueueTemporal(t *testing.T) {
	testPullSubFlowStartsWithoutStickyQueue(t)
}

func TestPullSubFlowRejectsInvalidTargetTemporal(t *testing.T) {
	testPullSubFlowRejectsInvalidTarget(t)
}

func testPullSubFlowRejectsInvalidTarget(t *testing.T) {
	t.Helper()
	runtime := startPullDispatchService(t, serverZDTestConfig{Matching: &testconfig.MatchingConfig{}})
	flowClient := runtime.FlowClient
	startInvalidSubFlowPullWorker(t, runtime.PublicAddress, dispatchProbeFlowType, "invalid-subflow-"+uuid.NewString())
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	flowID := "invalid-subflow-" + uuid.NewString()
	require.NoError(t, startDispatchProbe(ctx, flowClient, flowID, dispatchProbeStep, &dexpb.WorkerTarget{IsPullMode: true},
		&dexpb.StepOptions{WaitForRetryPolicy: &dexpb.RetryPolicy{MaximumAttempts: 1}}))
	startedAt := time.Now()
	result, err := flowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{FlowId: flowID, WaitTimeSeconds: 25})
	require.NoError(t, err)
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_FAILED, result.GetFlowStatus())

	// zd-bug #1009: server-zd fails the Flow as an internal error.
	require.Equal(t, dexpb.FlowErrorType_FLOW_ERROR_TYPE_INTERNAL, result.GetErrorType(), result.GetErrorMessage())
	require.Equal(t, "worker_target is empty", result.GetErrorMessage())

	require.Less(t, time.Since(startedAt), 10*time.Second)
}

func TestPullParallelLastWriteTemporal(t *testing.T) {
	testPullParallelLastWrite(t)
}

func testPullParallelLastWrite(t *testing.T) {
	t.Helper()
	runtime := startPullDispatchService(t, serverZDTestConfig{
		Matching: &testconfig.MatchingConfig{
			DispatchWait: 3 * time.Second, StickyWait: 100 * time.Millisecond,
		},
	})
	firstWorkerID := "pull-parallel-first-" + uuid.NewString()
	firstWorker := startParallelPullWorker(t, runtime.PublicAddress, dispatchProbeFlowType, firstWorkerID, true)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	flowID := "pull-parallel-" + uuid.NewString()
	require.NoError(t, startDispatchProbe(ctx, runtime.FlowClient, flowID, dispatchProbeStep,
		&dexpb.WorkerTarget{IsPullMode: true}, nil))
	require.Equal(t, dispatchProbeFlowType, waitForPullTask(t, firstWorker, flowID).queue)
	waitForFlowStickyWorker(t, ctx, runtime, flowID, firstWorkerID)
	firstWorker.stopQueue(dispatchProbeFlowType)
	secondWorkerID := "pull-parallel-second-" + uuid.NewString()
	secondWorker := startParallelPullWorker(t, runtime.PublicAddress, dispatchProbeFlowType, secondWorkerID, false)
	_, err := runtime.FlowClient.PublishToChannel(ctx, &dexpb.PublishToChannelRequest{
		FlowId: flowID, Messages: []*dexpb.ChannelMessage{{ChannelName: dispatchProbeChannel}},
	})
	require.NoError(t, err)
	require.Equal(t, firstWorkerID, waitForPullTask(t, firstWorker, flowID).queue)
	select {
	case started := <-firstWorker.started:
		require.Equal(t, flowID, started.flowID)
	case <-ctx.Done():
		t.Fatal("first parallel Step was not dispatched")
	}
	secondTask := waitForPullTask(t, secondWorker, flowID)
	require.Equal(t, "execute", secondTask.method)
	require.Equal(t, dispatchProbeFlowType, secondTask.queue)
	waitForFlowStickyWorker(t, ctx, runtime, flowID, secondWorkerID)
	close(firstWorker.releaseParallel)
	require.Equal(t, firstWorkerID, waitForPullTask(t, firstWorker, flowID).queue)
	waitForFlowStickyWorker(t, ctx, runtime, flowID, firstWorkerID)
	_, err = runtime.FlowClient.StopFlow(ctx, &dexpb.StopFlowRequest{FlowId: flowID})
	require.NoError(t, err)
}

func testPullSubFlowStartsWithoutStickyQueue(t *testing.T) {
	t.Helper()
	runtime := startPullDispatchService(t, serverZDTestConfig{Matching: &testconfig.MatchingConfig{}})
	workerID := "pull-subflow-worker-" + uuid.NewString()
	worker := startSubFlowPullWorker(t, runtime.PublicAddress, dispatchProbeFlowType, workerID)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	flowID := "pull-subflow-" + uuid.NewString()
	require.NoError(t, startDispatchProbe(ctx, runtime.FlowClient, flowID, dispatchProbeStep,
		&dexpb.WorkerTarget{IsPullMode: true}, nil))
	require.Equal(t, dispatchProbeFlowType, waitForPullTask(t, worker, flowID).queue)
	waitForFlowStickyWorker(t, ctx, runtime, flowID, workerID)
	childFlowID := "SubFlow:" + flowID + "-" + dispatchProbeStep + "-1-0"
	childTask := waitForPullTask(t, worker, childFlowID)
	require.Equal(t, "execute", childTask.method)
	require.Equal(t, dispatchProbeFlowType, childTask.queue)
	result, err := runtime.FlowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{FlowId: flowID, WaitTimeSeconds: 25})
	require.NoError(t, err)
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_COMPLETED, result.GetFlowStatus(), result.GetErrorMessage())
	require.Equal(t, workerID, waitForPullTask(t, worker, flowID).queue)
}

func testPullWorkerDisconnect(t *testing.T) {
	t.Helper()
	runtime := startPullDispatchService(t, serverZDTestConfig{

		Matching: &testconfig.MatchingConfig{DispatchWait: 2 * time.Second, StickyWait: 100 * time.Millisecond},
	})
	firstWorker := startDisconnectingPullWorker(t, runtime.PublicAddress, dispatchProbeFlowType, "pull-disconnect-"+uuid.NewString())
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	flowID := "pull-disconnect-" + uuid.NewString()
	require.NoError(t, startDispatchProbe(ctx, runtime.FlowClient, flowID, dispatchProbeStep,
		&dexpb.WorkerTarget{IsPullMode: true},
		&dexpb.StepOptions{WaitForRetryPolicy: &dexpb.RetryPolicy{MaximumAttempts: 1}},
	))
	firstTask := waitForPullTask(t, firstWorker, flowID)
	require.Equal(t, "wait_for", firstTask.method)
	require.Equal(t, int32(1), firstTask.attempt)
	secondWorker := startRawPullWorker(t, runtime.PublicAddress, dispatchProbeFlowType, "pull-replacement-"+uuid.NewString(), nil)
	secondTask := waitForPullTask(t, secondWorker, flowID)
	require.Equal(t, "wait_for", secondTask.method)
	require.Equal(t, int32(1), secondTask.attempt)
	_, err := runtime.FlowClient.PublishToChannel(ctx, &dexpb.PublishToChannelRequest{
		FlowId: flowID, Messages: []*dexpb.ChannelMessage{{ChannelName: dispatchProbeChannel}},
	})
	require.NoError(t, err)
	result, err := runtime.FlowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{FlowId: flowID, WaitTimeSeconds: 25})
	require.NoError(t, err)
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_COMPLETED, result.GetFlowStatus())
}

func testPullWorkerStickyCapacityRecovery(t *testing.T) {
	t.Helper()
	stickyWait := 5 * time.Second
	runtime := startPullDispatchService(t, serverZDTestConfig{

		Matching: &testconfig.MatchingConfig{DispatchWait: 7 * time.Second, StickyWait: stickyWait},
	})
	workerID := "pull-capacity-" + uuid.NewString()
	worker := startRawPullWorker(t, runtime.PublicAddress, dispatchProbeFlowType, workerID, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	flowID := "pull-capacity-" + uuid.NewString()
	require.NoError(t, startDispatchProbe(ctx, runtime.FlowClient, flowID, dispatchProbeStep,
		&dexpb.WorkerTarget{IsPullMode: true}, nil))
	require.Equal(t, dispatchProbeFlowType, waitForPullTask(t, worker, flowID).queue)
	waitForFlowStickyWorker(t, ctx, runtime, flowID, workerID)
	requirePullActiveRouting(t, ctx, runtime.FlowClient, flowID, workerID)
	worker.rejectNextStickyTask <- struct{}{}
	_, err := runtime.FlowClient.PublishToChannel(ctx, &dexpb.PublishToChannelRequest{
		FlowId: flowID, Messages: []*dexpb.ChannelMessage{{ChannelName: dispatchProbeChannel}},
	})
	require.NoError(t, err)
	select {
	case rejected := <-worker.rejectedStickyTasks:
		require.Equal(t, workerID, rejected.queue)
	case <-ctx.Done():
		t.Fatal("sticky Worker did not reject the task")
	}
	rejectedAt := time.Now()
	served := waitForPullTask(t, worker, flowID)
	require.Equal(t, workerID, served.queue)
	require.Equal(t, "execute", served.method)
	require.Equal(t, int32(1), served.attempt)
	require.Less(t, time.Since(rejectedAt), stickyWait/2)
	result, err := runtime.FlowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{FlowId: flowID, WaitTimeSeconds: 25})
	require.NoError(t, err)
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_COMPLETED, result.GetFlowStatus(), result.GetErrorMessage())
	requirePullHistoryRouting(t, ctx, runtime.FlowClient, flowID, workerID, workerID, dexpb.PullRoutingSource_PULL_ROUTING_SOURCE_STICKY_QUEUE)
}

func testPullWorkerAppearsDuringRetries(t *testing.T) {
	t.Helper()
	testConfig := serverZDTestConfig{

		Matching: &testconfig.MatchingConfig{DispatchWait: 100 * time.Millisecond, StickyWait: 50 * time.Millisecond},
	}
	// server-zd has no Worker wake hook; binary mode waits for the first failed dispatch instead.

	runtime := startPullDispatchService(t, testConfig)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	flowID := "pull-retry-" + uuid.NewString()
	require.NoError(t, startDispatchProbe(ctx, runtime.FlowClient, flowID, dispatchProbeStep,
		&dexpb.WorkerTarget{IsPullMode: true},
		&dexpb.StepOptions{WaitForRetryPolicy: &dexpb.RetryPolicy{MaximumAttempts: 5, InitialIntervalSeconds: 1}},
	))

	require.Eventually(t, func() bool {
		state, err := runtime.FlowClient.GetFlowState(ctx, &dexpb.GetFlowStateRequest{FlowId: flowID})
		if err != nil {
			return false
		}
		for _, step := range state.GetActiveStepExecutions() {
			if step.GetStepType() == dispatchProbeStep && step.GetLastFailureInfo() != nil {
				return true
			}
		}
		return false
	}, 10*time.Second, 50*time.Millisecond)

	worker := startRawPullWorker(t, runtime.PublicAddress, dispatchProbeFlowType, "pull-retry-worker-"+uuid.NewString(), nil)
	require.Equal(t, "wait_for", waitForPullTask(t, worker, flowID).method)
	_, err := runtime.FlowClient.PublishToChannel(ctx, &dexpb.PublishToChannelRequest{
		FlowId: flowID, Messages: []*dexpb.ChannelMessage{{ChannelName: dispatchProbeChannel}},
	})
	require.NoError(t, err)
	result, err := runtime.FlowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{FlowId: flowID, WaitTimeSeconds: 25})
	require.NoError(t, err)
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_COMPLETED, result.GetFlowStatus())
}

func testPullWorkerStickyFallback(t *testing.T) {
	t.Helper()
	stickyWait := 10 * time.Second
	runtime := startPullDispatchService(t, serverZDTestConfig{

		Matching: &testconfig.MatchingConfig{DispatchWait: 12 * time.Second, StickyWait: stickyWait},
	})
	workerID := "pull-sticky-old-" + uuid.NewString()
	firstWorker := startRawPullWorker(t, runtime.PublicAddress, dispatchProbeFlowType, workerID, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	flowID := "pull-sticky-fallback-" + uuid.NewString()
	require.NoError(t, startDispatchProbe(ctx, runtime.FlowClient, flowID, dispatchProbeStep,
		&dexpb.WorkerTarget{IsPullMode: true}, nil))
	require.Equal(t, dispatchProbeFlowType, waitForPullTask(t, firstWorker, flowID).queue)
	waitForFlowStickyWorker(t, ctx, runtime, flowID, workerID)
	requirePullActiveRouting(t, ctx, runtime.FlowClient, flowID, workerID)
	firstWorker.stop()
	secondWorkerID := "pull-sticky-new-" + uuid.NewString()
	secondWorker := startRawPullWorker(t, runtime.PublicAddress, dispatchProbeFlowType, secondWorkerID, nil)
	startedAt := time.Now()
	_, err := runtime.FlowClient.PublishToChannel(ctx, &dexpb.PublishToChannelRequest{
		FlowId: flowID, Messages: []*dexpb.ChannelMessage{{ChannelName: dispatchProbeChannel}},
	})
	require.NoError(t, err)
	served := waitForPullTask(t, secondWorker, flowID)
	require.Less(t, time.Since(startedAt), stickyWait/2)
	result, err := runtime.FlowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{FlowId: flowID, WaitTimeSeconds: 25})
	require.NoError(t, err)
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_COMPLETED, result.GetFlowStatus())
	require.Equal(t, dispatchProbeFlowType, served.queue)
	requirePullHistoryRouting(t, ctx, runtime.FlowClient, flowID, workerID, secondWorkerID, dexpb.PullRoutingSource_PULL_ROUTING_SOURCE_FLOW_TYPE_QUEUE)
}

func TestPullUnknownNameRedispatchTemporal(t *testing.T) {
	testPullUnknownNameRedispatch(t)
}

func TestPullUnknownRPCBoundedFailureTemporal(t *testing.T) {
	testPullUnknownRPCBoundedFailure(t)
}

func testPullUnknownRPCBoundedFailure(t *testing.T) {
	t.Helper()
	for _, isTransactional := range []bool{false, true} {
		t.Run(fmt.Sprintf("transactional=%t", isTransactional), func(t *testing.T) {
			var unknownNameCode, legacyCode codes.Code
			var unknownNameMessage, legacyMessage string
			// Separate subtests stop each runtime; transactional RPC activities share one task queue.
			t.Run("unknown-name rejection", func(t *testing.T) {
				unknownNameCode, unknownNameMessage = invokeUnknownPullRPC(t, isTransactional, true)
			})
			t.Run("legacy Worker error", func(t *testing.T) {
				legacyCode, legacyMessage = invokeUnknownPullRPC(t, isTransactional, false)
			})
			require.Equal(t, legacyCode, unknownNameCode)
			require.Equal(t, codes.FailedPrecondition, unknownNameCode)
			require.Contains(t, unknownNameMessage, "no pull Worker registered RPC \"getProbe\"")
			require.Contains(t, legacyMessage, "RPC is not registered: getProbe")
		})
	}
}

func invokeUnknownPullRPC(t *testing.T, isTransactional bool, rejectsUnknownName bool) (codes.Code, string) {
	t.Helper()
	dispatchWait := 700 * time.Millisecond
	runtime := startPullDispatchService(t, serverZDTestConfig{Matching: &testconfig.MatchingConfig{
		DispatchWait: dispatchWait, StickyWait: 100 * time.Millisecond,
	}})
	workerID := "pull-unknown-rpc-" + uuid.NewString()
	var worker *rawPullWorker
	if rejectsUnknownName {
		worker = startUnknownRejectingPullWorker(t, runtime.PublicAddress, dispatchProbeFlowType, workerID, rawPullWorkerConfig{unknownRPCName: "getProbe"})
	} else {
		worker = startRawPullWorker(t, runtime.PublicAddress, dispatchProbeFlowType, workerID, &dexpb.WorkerErrorResponse{
			Detail: "RPC is not registered: getProbe", GrpcStatus: int32(codes.NotFound),
		})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	flowID := "pull-unknown-rpc-" + uuid.NewString()
	_, err := runtime.FlowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId: newRequestID(), FlowId: flowID, FlowType: dispatchProbeFlowType,
		FlowTimeoutSeconds: 120, FlowStartOptions: withWorkerTarget(nil, &dexpb.WorkerTarget{IsPullMode: true}),
	})
	require.NoError(t, err)
	startedAt := time.Now()
	_, err = runtime.FlowClient.InvokeRPC(ctx, &dexpb.InvokeRPCRequest{
		RequestId: newRequestID(), FlowId: flowID, RpcName: "getProbe", IsTransactional: isTransactional, TimeoutSeconds: 10,
	})
	require.Error(t, err)
	require.Less(t, time.Since(startedAt), 8*time.Second)
	if rejectsUnknownName {
		select {
		case rejected := <-worker.unknownRejections:
			require.Equal(t, flowID, rejected.flowID)
			require.Equal(t, dispatchProbeFlowType, rejected.queue)
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for unknown RPC rejection")
		}
	} else {
		require.Equal(t, "rpc", waitForPullTask(t, worker, flowID).method)
	}
	_, stopErr := runtime.FlowClient.StopFlow(ctx, &dexpb.StopFlowRequest{FlowId: flowID})
	require.NoError(t, stopErr)
	return status.Code(err), status.Convert(err).Message()
}

func testPullUnknownNameRedispatch(t *testing.T) {
	t.Helper()
	t.Run("Step", func(t *testing.T) {
		runtime := startPullDispatchService(t, serverZDTestConfig{Matching: &testconfig.MatchingConfig{DispatchWait: 5 * time.Second, StickyWait: 2 * time.Second}})
		oldWorker := startUnknownRejectingPullWorker(t, runtime.PublicAddress, dispatchProbeFlowType, "pull-old-"+uuid.NewString(), rawPullWorkerConfig{unknownStepType: dispatchProbeStep})
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		flowID := "pull-unknown-step-" + uuid.NewString()
		require.NoError(t, startDispatchProbe(ctx, runtime.FlowClient, flowID, dispatchProbeStep, &dexpb.WorkerTarget{IsPullMode: true}, nil))
		select {
		case rejected := <-oldWorker.unknownRejections:
			require.Equal(t, int32(1), rejected.attempt)
		case <-ctx.Done():
			t.Fatal("old Worker did not reject the Step")
		}
		newWorker := startRawPullWorker(t, runtime.PublicAddress, dispatchProbeFlowType, "pull-new-"+uuid.NewString(), nil)
		require.Equal(t, int32(1), waitForPullTask(t, newWorker, flowID).attempt)
		_, err := runtime.FlowClient.PublishToChannel(ctx, &dexpb.PublishToChannelRequest{FlowId: flowID, Messages: []*dexpb.ChannelMessage{{ChannelName: dispatchProbeChannel}}})
		require.NoError(t, err)
		result, err := runtime.FlowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{FlowId: flowID, WaitTimeSeconds: 25})
		require.NoError(t, err)
		require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_COMPLETED, result.GetFlowStatus(), result.GetErrorMessage())
		require.Empty(t, oldWorker.unknownRejections)
	})
	t.Run("RPC and sticky fallback", func(t *testing.T) {
		stickyWait := 4 * time.Second
		runtime := startPullDispatchService(t, serverZDTestConfig{Matching: &testconfig.MatchingConfig{DispatchWait: 5 * time.Second, StickyWait: stickyWait}})
		oldWorkerID := "pull-old-" + uuid.NewString()
		oldWorker := startUnknownRejectingPullWorker(t, runtime.PublicAddress, dispatchProbeFlowType, oldWorkerID, rawPullWorkerConfig{unknownRPCName: "getProbe"})
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		flowID := "pull-unknown-rpc-" + uuid.NewString()
		require.NoError(t, startDispatchProbe(ctx, runtime.FlowClient, flowID, dispatchProbeStep, &dexpb.WorkerTarget{IsPullMode: true}, nil))
		require.Equal(t, int32(1), waitForPullTask(t, oldWorker, flowID).attempt)
		waitForFlowStickyWorker(t, ctx, runtime, flowID, oldWorkerID)
		newWorker := startRawPullWorker(t, runtime.PublicAddress, dispatchProbeFlowType, "pull-new-"+uuid.NewString(), nil)
		startedAt := time.Now()
		response, err := runtime.FlowClient.InvokeRPC(ctx, &dexpb.InvokeRPCRequest{RequestId: newRequestID(), FlowId: flowID, RpcName: "getProbe", TimeoutSeconds: 15})
		require.NoError(t, err)
		require.NotNil(t, response)
		require.Less(t, time.Since(startedAt), stickyWait/2)
		select {
		case rejected := <-oldWorker.unknownRejections:
			require.Equal(t, oldWorkerID, rejected.queue)
		case <-ctx.Done():
			t.Fatal("sticky Worker did not reject the RPC")
		}
		require.Equal(t, "rpc", waitForPullTask(t, newWorker, flowID).method)
		require.Empty(t, oldWorker.unknownRejections)
		_, err = runtime.FlowClient.StopFlow(ctx, &dexpb.StopFlowRequest{FlowId: flowID})
		require.NoError(t, err)
	})
	t.Run("sticky Step", func(t *testing.T) {
		stickyWait := 10 * time.Second
		runtime := startPullDispatchService(t, serverZDTestConfig{Matching: &testconfig.MatchingConfig{DispatchWait: 12 * time.Second, StickyWait: stickyWait}})
		oldWorkerID := "pull-old-" + uuid.NewString()
		oldWorker := startUnknownRejectingPullWorker(t, runtime.PublicAddress, dispatchProbeFlowType, oldWorkerID, rawPullWorkerConfig{unknownExecuteStepType: dispatchProbeStep})
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		flowID := "pull-unknown-sticky-step-" + uuid.NewString()
		require.NoError(t, startDispatchProbe(ctx, runtime.FlowClient, flowID, dispatchProbeStep, &dexpb.WorkerTarget{IsPullMode: true}, nil))
		require.Equal(t, int32(1), waitForPullTask(t, oldWorker, flowID).attempt)
		waitForFlowStickyWorker(t, ctx, runtime, flowID, oldWorkerID)
		newWorker := startRawPullWorker(t, runtime.PublicAddress, dispatchProbeFlowType, "pull-new-"+uuid.NewString(), nil)
		startedAt := time.Now()
		_, err := runtime.FlowClient.PublishToChannel(ctx, &dexpb.PublishToChannelRequest{FlowId: flowID, Messages: []*dexpb.ChannelMessage{{ChannelName: dispatchProbeChannel}}})
		require.NoError(t, err)
		result, err := runtime.FlowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{FlowId: flowID, WaitTimeSeconds: 25})
		require.NoError(t, err)
		require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_COMPLETED, result.GetFlowStatus(), result.GetErrorMessage())
		require.Less(t, time.Since(startedAt), stickyWait)
		require.Equal(t, int32(1), waitForPullTask(t, newWorker, flowID).attempt)
		select {
		case rejected := <-oldWorker.unknownRejections:
			require.Equal(t, oldWorkerID, rejected.queue)
			require.Equal(t, int32(1), rejected.attempt)
		case <-ctx.Done():
			t.Fatal("sticky Worker did not reject the Step")
		}
		require.Empty(t, oldWorker.unknownRejections)
	})
	t.Run("bounded failure", func(t *testing.T) {
		dispatchWait := 700 * time.Millisecond
		runtime := startPullDispatchService(t, serverZDTestConfig{Matching: &testconfig.MatchingConfig{DispatchWait: dispatchWait, StickyWait: 100 * time.Millisecond}})
		oldWorker := startUnknownRejectingPullWorker(t, runtime.PublicAddress, dispatchProbeFlowType, "pull-old-"+uuid.NewString(), rawPullWorkerConfig{unknownStepType: dispatchProbeStep})
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		flowID := "pull-unknown-bound-" + uuid.NewString()
		require.NoError(t, startDispatchProbe(ctx, runtime.FlowClient, flowID, dispatchProbeStep, &dexpb.WorkerTarget{IsPullMode: true}, &dexpb.StepOptions{WaitForRetryPolicy: &dexpb.RetryPolicy{MaximumAttempts: 1}}))
		result, err := runtime.FlowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{FlowId: flowID, WaitTimeSeconds: 25})
		require.NoError(t, err)
		require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_FAILED, result.GetFlowStatus())
		require.Contains(t, result.GetErrorMessage(), dispatchProbeStep)
		require.Len(t, oldWorker.unknownRejections, 1)
	})
	t.Run("legacy Worker error", func(t *testing.T) {
		runtime := startPullDispatchService(t, serverZDTestConfig{Matching: &testconfig.MatchingConfig{DispatchWait: time.Second, StickyWait: 100 * time.Millisecond}})
		oldWorker := startRawPullWorker(t, runtime.PublicAddress, dispatchProbeFlowType, "pull-legacy-"+uuid.NewString(), &dexpb.WorkerErrorResponse{Detail: "RPC is not registered: getProbe", GrpcStatus: int32(codes.NotFound)})
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		flowID := "pull-legacy-rpc-" + uuid.NewString()
		_, err := runtime.FlowClient.StartFlow(ctx, &dexpb.StartFlowRequest{RequestId: newRequestID(), FlowId: flowID, FlowType: dispatchProbeFlowType, FlowTimeoutSeconds: 120, FlowStartOptions: withWorkerTarget(nil, &dexpb.WorkerTarget{IsPullMode: true})})
		require.NoError(t, err)
		_, err = runtime.FlowClient.InvokeRPC(ctx, &dexpb.InvokeRPCRequest{RequestId: newRequestID(), FlowId: flowID, RpcName: "getProbe", TimeoutSeconds: 10})
		require.Error(t, err)
		require.Equal(t, codes.FailedPrecondition, status.Code(err))
		require.Contains(t, err.Error(), "RPC is not registered: getProbe")
		require.Equal(t, "rpc", waitForPullTask(t, oldWorker, flowID).method)
		require.Empty(t, oldWorker.unknownRejections)
		_, err = runtime.FlowClient.StopFlow(ctx, &dexpb.StopFlowRequest{FlowId: flowID})
		require.NoError(t, err)
	})
}

func TestPullWorkerErrorMatchesPushTemporal(t *testing.T) {
	testPullWorkerErrorMatchesPush(t)
}

func TestPullStepErrorMatchesPushTemporal(t *testing.T) {
	testPullStepErrorMatchesPush(t)
}

func testPullStepErrorMatchesPush(t *testing.T) {
	t.Helper()
	retryAfterSeconds := int32(0)

	retryAfterSeconds = 1

	runtime := startPullDispatchService(t, serverZDTestConfig{Matching: &testconfig.MatchingConfig{}})
	var results []*dexpb.FlowResult
	targets := []*dexpb.WorkerTarget{{IsPullMode: true}}

	// server-zd has only pull Workers (C18); compare with the push result the Go Server returns.
	results = append(results, &dexpb.FlowResult{
		FlowStatus: dexpb.FlowStatus_FLOW_STATUS_FAILED, ErrorType: dexpb.FlowErrorType_FLOW_ERROR_TYPE_WORKER_API_FAIL,
		ErrorMessage: "invalid Worker input",
	})

	startRawPullWorker(t, runtime.PublicAddress, dispatchProbeFlowType, "step-error-"+uuid.NewString(),
		&dexpb.WorkerErrorResponse{Detail: "invalid Worker input", ErrorType: "InputError", StackTrace: "worker stack",
			GrpcStatus: int32(codes.InvalidArgument), RetryAfterSeconds: retryAfterSeconds})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, target := range targets {
		flowID := "step-error-" + uuid.NewString()
		startedAt := time.Now()
		require.NoError(t, startDispatchProbe(ctx, runtime.FlowClient, flowID, dispatchProbeStep, target,
			&dexpb.StepOptions{WaitForRetryPolicy: &dexpb.RetryPolicy{MaximumAttempts: 2, InitialIntervalSeconds: 1}}))
		result, err := runtime.FlowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{FlowId: flowID, WaitTimeSeconds: 25})
		require.NoError(t, err)
		require.GreaterOrEqual(t, time.Since(startedAt), time.Second)
		results = append(results, result)
	}
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_FAILED, results[0].GetFlowStatus())
	require.Equal(t, results[0].GetFlowStatus(), results[1].GetFlowStatus())
	require.Equal(t, results[0].GetErrorType(), results[1].GetErrorType())
	require.Equal(t, results[0].GetErrorMessage(), results[1].GetErrorMessage())
	require.Equal(t, dexpb.FlowErrorType_FLOW_ERROR_TYPE_WORKER_API_FAIL, results[1].GetErrorType())
}

func TestPullRPCDispatchRespectsCallerDeadlineTemporal(t *testing.T) {
	testPullRPCDispatchRespectsCallerDeadline(t)
}

func TestPullTransactionalRPCShortBudgetTemporal(t *testing.T) {
	runtime := startPullDispatchService(t, serverZDTestConfig{Matching: &testconfig.MatchingConfig{}})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	flowID := "pull-short-rpc-" + uuid.NewString()
	_, err := runtime.FlowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId: newRequestID(), FlowId: flowID, FlowType: dispatchProbeFlowType,
		FlowTimeoutSeconds: 120, FlowStartOptions: withWorkerTarget(nil, &dexpb.WorkerTarget{IsPullMode: true}),
	})
	require.NoError(t, err)
	worker := startRawPullWorker(t, runtime.PublicAddress, dispatchProbeFlowType, "short-rpc-worker-"+uuid.NewString(), nil)
	startedAt := time.Now()
	response, err := runtime.FlowClient.InvokeRPC(ctx, &dexpb.InvokeRPCRequest{
		RequestId: newRequestID(), FlowId: flowID, RpcName: "getProbe", IsTransactional: true, TimeoutSeconds: 3,
	})
	require.NoError(t, err)
	require.NotNil(t, response)
	require.Less(t, time.Since(startedAt), 3*time.Second)
	require.Equal(t, "rpc", waitForPullTask(t, worker, flowID).method)
	_, err = runtime.FlowClient.StopFlow(ctx, &dexpb.StopFlowRequest{FlowId: flowID})
	require.NoError(t, err)
}

func TestPullTransactionalRPCTimeoutReleasesLockTemporal(t *testing.T) {
	runtime := startPullDispatchService(t, serverZDTestConfig{Matching: &testconfig.MatchingConfig{}})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	flowID := "pull-rpc-lock-" + uuid.NewString()
	_, err := runtime.FlowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId: newRequestID(), FlowId: flowID, FlowType: dispatchProbeFlowType,
		FlowTimeoutSeconds: 120, FlowStartOptions: withWorkerTarget(nil, &dexpb.WorkerTarget{IsPullMode: true}),
	})
	require.NoError(t, err)
	startedAt := time.Now()
	_, err = runtime.FlowClient.InvokeRPC(ctx, &dexpb.InvokeRPCRequest{
		RequestId: newRequestID(), FlowId: flowID, RpcName: "getProbe", IsTransactional: true, TimeoutSeconds: 3,
	})
	require.Error(t, err)
	require.Less(t, time.Since(startedAt), 5*time.Second)
	worker := startRawPullWorker(t, runtime.PublicAddress, dispatchProbeFlowType, "rpc-lock-worker-"+uuid.NewString(), nil)
	startedAt = time.Now()
	_, err = runtime.FlowClient.InvokeRPC(ctx, &dexpb.InvokeRPCRequest{
		RequestId: newRequestID(), FlowId: flowID, RpcName: "getProbe", IsTransactional: true, TimeoutSeconds: 3,
	})
	require.NoError(t, err)
	require.Less(t, time.Since(startedAt), 3*time.Second)
	require.Equal(t, "rpc", waitForPullTask(t, worker, flowID).method)
	_, err = runtime.FlowClient.StopFlow(ctx, &dexpb.StopFlowRequest{FlowId: flowID})
	require.NoError(t, err)
}

func testPullRPCDispatchRespectsCallerDeadline(t *testing.T) {
	t.Helper()
	runtime := startPullDispatchService(t, serverZDTestConfig{

		Matching: &testconfig.MatchingConfig{DispatchWait: 5 * time.Second, StickyWait: time.Second},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	flowID := "pull-rpc-deadline-" + uuid.NewString()
	_, err := runtime.FlowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId: newRequestID(), FlowId: flowID, FlowType: dispatchProbeFlowType,
		FlowTimeoutSeconds: 120, FlowStartOptions: withWorkerTarget(nil, &dexpb.WorkerTarget{IsPullMode: true}),
	})
	require.NoError(t, err)
	waitForFlowStickyWorker(t, ctx, runtime, flowID, "")
	for _, isTransactional := range []bool{false, true} {
		callCtx, cancelCall := context.WithTimeout(ctx, 400*time.Millisecond)
		startedAt := time.Now()
		_, err = runtime.FlowClient.InvokeRPC(callCtx, &dexpb.InvokeRPCRequest{
			RequestId: newRequestID(), FlowId: flowID, RpcName: "getProbe", IsTransactional: isTransactional,
			TimeoutSeconds: 10,
		})
		cancelCall()
		require.Error(t, err)
		require.Less(t, time.Since(startedAt), 2*time.Second)
	}
	_, err = runtime.FlowClient.StopFlow(ctx, &dexpb.StopFlowRequest{FlowId: flowID})
	require.NoError(t, err)
}

func testPullWorkerErrorMatchesPush(t *testing.T) {
	t.Helper()
	runtime := startPullDispatchService(t, serverZDTestConfig{Matching: &testconfig.MatchingConfig{}})
	var observedErrors []*dexpb.ServiceErrorResponse
	var observedStatusCodes []codes.Code
	targets := []*dexpb.WorkerTarget{{IsPullMode: true}}

	// server-zd has only pull Workers (C18); compare with the push error the Go Server returns.
	observedStatusCodes = append(observedStatusCodes, codes.FailedPrecondition)
	observedErrors = append(observedErrors, &dexpb.ServiceErrorResponse{
		SubStatus: dexpb.ErrorSubStatus_ERROR_SUB_STATUS_WORKER_API_ERROR, OriginalWorkerErrorDetail: "invalid Worker input",
		OriginalWorkerErrorType: "InputError", OriginalWorkerErrorStatus: int32(codes.InvalidArgument),
		OriginalWorkerErrorStackTrace: "worker stack",
	})

	startRawPullWorker(t, runtime.PublicAddress, dispatchProbeFlowType, "pull-error-"+uuid.NewString(),
		&dexpb.WorkerErrorResponse{Detail: "invalid Worker input", ErrorType: "InputError", StackTrace: "worker stack", GrpcStatus: int32(codes.InvalidArgument)})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, target := range targets {
		flowID := "pull-error-compare-" + uuid.NewString()
		_, err := runtime.FlowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
			RequestId: newRequestID(), FlowId: flowID, FlowType: dispatchProbeFlowType,
			FlowTimeoutSeconds: 120, FlowStartOptions: withWorkerTarget(nil, target),
		})
		require.NoError(t, err)
		_, err = runtime.FlowClient.InvokeRPC(ctx, &dexpb.InvokeRPCRequest{
			RequestId: newRequestID(), FlowId: flowID, RpcName: "getProbe", TimeoutSeconds: 15,
		})
		require.Error(t, err)
		observedStatusCodes = append(observedStatusCodes, status.Code(err))
		observedErrors = append(observedErrors, grpcServiceErrorResponse(t, err))
		_, err = runtime.FlowClient.StopFlow(ctx, &dexpb.StopFlowRequest{FlowId: flowID})
		require.NoError(t, err)
	}
	require.Equal(t, observedStatusCodes[0], observedStatusCodes[1])
	require.Equal(t, observedErrors[0].GetSubStatus(), observedErrors[1].GetSubStatus())
	require.Equal(t, observedErrors[0].GetOriginalWorkerErrorStatus(), observedErrors[1].GetOriginalWorkerErrorStatus())
	require.Equal(t, int32(codes.InvalidArgument), observedErrors[1].GetOriginalWorkerErrorStatus())
	require.Equal(t, observedErrors[0].GetOriginalWorkerErrorDetail(), observedErrors[1].GetOriginalWorkerErrorDetail())
	require.Equal(t, observedErrors[0].GetOriginalWorkerErrorType(), observedErrors[1].GetOriginalWorkerErrorType())
	require.Equal(t, observedErrors[0].GetOriginalWorkerErrorStackTrace(), observedErrors[1].GetOriginalWorkerErrorStackTrace())
}

func testPullWorkerUnavailable(t *testing.T) {
	t.Helper()
	runtime := startPullDispatchService(t, serverZDTestConfig{

		Matching: &testconfig.MatchingConfig{DispatchWait: 100 * time.Millisecond, StickyWait: 50 * time.Millisecond},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	flowID := "pull-unavailable-" + uuid.NewString()
	require.NoError(t, startDispatchProbe(ctx, runtime.FlowClient, flowID, dispatchProbeStep,
		&dexpb.WorkerTarget{IsPullMode: true},
		&dexpb.StepOptions{WaitForRetryPolicy: &dexpb.RetryPolicy{MaximumAttempts: 2, InitialIntervalSeconds: 1}},
	))
	startedAt := time.Now()
	result, err := runtime.FlowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{FlowId: flowID, WaitTimeSeconds: 25})
	require.NoError(t, err)
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_FAILED, result.GetFlowStatus())
	require.True(t, strings.Contains(result.GetErrorMessage(), "no Worker available for queue "+dispatchProbeFlowType), result.GetErrorMessage())
	require.GreaterOrEqual(t, time.Since(startedAt), time.Second)
	summary, err := runtime.FlowClient.GetFlowSummary(ctx, &dexpb.GetFlowSummaryRequest{FlowId: flowID})
	require.NoError(t, err)
	history, err := runtime.FlowClient.GetHistoryEvents(ctx, &dexpb.GetHistoryEventsRequest{FlowId: flowID, RunId: summary.GetFlowExecutionId().GetRunId(), EstimatePageSize: 100})
	require.NoError(t, err)
	var failedMethod *dexpb.StepWaitForFailedEvent
	for _, event := range history.GetEvents() {
		if event.GetStepWaitForFailed() != nil {
			failedMethod = event.GetStepWaitForFailed()
		}
	}
	require.NotNil(t, failedMethod)
	require.NotNil(t, failedMethod.GetContext().GetPullRouting())
	require.Empty(t, failedMethod.GetContext().GetPullRouting().GetServedByAppWorkerId())
	failureDetails := failedMethod.GetOutput().GetFailure().GetDetails()
	require.Equal(t, dexpb.DispatchTaskErrorReason_DISPATCH_TASK_ERROR_REASON_NO_WORKER_AVAILABLE, failureDetails.GetDispatchTaskErrorReason())
	require.Contains(t, failureDetails.GetDetail(), "no Worker available")
}

func testPullWorkerDispatch(t *testing.T) {
	t.Helper()
	runtime := startPullDispatchService(t, serverZDTestConfig{
		Matching:    &testconfig.MatchingConfig{},
		StreamStore: testconfig.StreamStoreConfig{Backend: testconfig.StreamStoreBackendMemory},
	})
	workerID := "pull-worker-" + uuid.NewString()
	worker := startProgressPullWorker(t, runtime.PublicAddress, dispatchProbeFlowType, workerID)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	flowID := "pull-dispatch-" + uuid.NewString()
	require.NoError(t, startDispatchProbe(ctx, runtime.FlowClient, flowID, dispatchProbeStep,
		&dexpb.WorkerTarget{IsPullMode: true}, nil))
	first := waitForPullTask(t, worker, flowID)
	require.Equal(t, pullTaskObservation{queue: dispatchProbeFlowType, method: "wait_for", flowID: flowID, attempt: 1}, first)
	waitForFlowStickyWorker(t, ctx, runtime, flowID, workerID)
	_, err := runtime.FlowClient.PublishToChannel(ctx, &dexpb.PublishToChannelRequest{
		FlowId: flowID, Messages: []*dexpb.ChannelMessage{{ChannelName: dispatchProbeChannel}},
	})
	require.NoError(t, err)
	result, err := runtime.FlowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{FlowId: flowID, WaitTimeSeconds: 30})
	require.NoError(t, err)
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_COMPLETED, result.GetFlowStatus())
	second := waitForPullTask(t, worker, flowID)
	require.Equal(t, pullTaskObservation{queue: workerID, method: "execute", flowID: flowID, attempt: 1}, second)
	for _, method := range []string{"wait_for", "execute"} {
		messages, listErr := runtime.FlowClient.ListStreamMessages(ctx, &dexpb.ListStreamMessagesRequest{
			FlowId: flowID, FlowType: dispatchProbeFlowType, StreamName: method + "-progress", PageSize: 10,
		})
		require.NoError(t, listErr)
		require.Len(t, messages.GetMessages(), 1)
		require.Equal(t, method+"-progress", messages.GetMessages()[0].GetValue().GetStringValue())
	}

	rpcFlowID := "pull-rpc-" + uuid.NewString()
	_, err = runtime.FlowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId: newRequestID(), FlowId: rpcFlowID, FlowType: dispatchProbeFlowType,
		FlowTimeoutSeconds: 120, FlowStartOptions: withWorkerTarget(nil, &dexpb.WorkerTarget{IsPullMode: true}),
	})
	require.NoError(t, err)
	for rpcIndex, transactionMode := range []bool{false, true, false} {
		_, err = runtime.FlowClient.InvokeRPC(ctx, &dexpb.InvokeRPCRequest{
			RequestId: newRequestID(), FlowId: rpcFlowID, RpcName: "getProbe", IsTransactional: transactionMode,
			TimeoutSeconds: 15,
		})
		require.NoError(t, err)
		observed := waitForPullTask(t, worker, rpcFlowID)
		require.Equal(t, "rpc", observed.method)
		expectedQueue := dispatchProbeFlowType
		if rpcIndex == 2 {
			expectedQueue = workerID
		}
		require.Equal(t, expectedQueue, observed.queue)
	}
	_, err = runtime.FlowClient.StopFlow(ctx, &dexpb.StopFlowRequest{FlowId: rpcFlowID})
	require.NoError(t, err)
}

// pullStepHeartbeatTimeout returns the shortest Step heartbeat timeout each server accepts; server-zd requires 10s.
func pullStepHeartbeatTimeout() time.Duration {

	return 10 * time.Second

}

// startPullDispatchService starts the in-process Go Server, or server-zd in binary mode, for raw pull Workers.
func startPullDispatchService(t *testing.T, testConfig serverZDTestConfig) *integRuntime {
	t.Helper()

	runtime := startBinaryDexService(t, testConfig)
	// Raw pull Workers register their own queues, so bypass the adapter that startWorker registers.
	runtime.FlowClient = runtime.FlowClient.(*pullWorkerFlowClient).FlowServiceClient
	return runtime
}

func waitForPullTask(t *testing.T, worker *rawPullWorker, flowID string) pullTaskObservation {
	t.Helper()
	for {
		select {
		case event := <-worker.events:
			if event.flowID == flowID {
				return event
			}
		case err := <-worker.errors:
			require.NoError(t, err)
		case <-time.After(30 * time.Second):
			t.Fatal("timed out waiting for pull task")
		}
	}
}

// waitForFlowStickyWorker waits until the Flow's sticky Worker is workerID; empty means none.
func waitForFlowStickyWorker(t *testing.T, ctx context.Context, runtime *integRuntime, flowID string, workerID string) {
	t.Helper()

	// server-zd has no Temporal task queue query; GetFlowState reports the sticky Worker instead.
	require.Eventually(t, func() bool {
		state, err := runtime.FlowClient.GetFlowState(ctx, &dexpb.GetFlowStateRequest{FlowId: flowID})
		return err == nil && state.GetStickyAppWorkerId() == workerID
	}, 10*time.Second, 50*time.Millisecond)
}

func requirePullHistoryRouting(t *testing.T, ctx context.Context, client dexpb.FlowServiceClient, flowID string, waitWorkerID string, executeWorkerID string, executeSource dexpb.PullRoutingSource) {
	t.Helper()
	summary, err := client.GetFlowSummary(ctx, &dexpb.GetFlowSummaryRequest{FlowId: flowID})
	require.NoError(t, err)
	history, err := client.GetHistoryEvents(ctx, &dexpb.GetHistoryEventsRequest{FlowId: flowID, RunId: summary.GetFlowExecutionId().GetRunId(), EstimatePageSize: 100})
	require.NoError(t, err)
	var waitRouting, executeRouting *dexpb.PullRouting
	for _, event := range history.GetEvents() {
		if event.GetStepWaitForCompleted() != nil {
			waitRouting = event.GetStepWaitForCompleted().GetContext().GetPullRouting()
		}
		if event.GetStepExecuteCompleted() != nil {
			executeRouting = event.GetStepExecuteCompleted().GetContext().GetPullRouting()
		}
	}
	require.NotNil(t, waitRouting)
	require.Equal(t, waitWorkerID, waitRouting.GetServedByAppWorkerId())
	require.Empty(t, waitRouting.GetStickyAppWorkerId())
	require.Equal(t, dexpb.PullRoutingSource_PULL_ROUTING_SOURCE_FLOW_TYPE_QUEUE, waitRouting.GetSource())
	require.NotNil(t, executeRouting)
	require.Equal(t, executeWorkerID, executeRouting.GetServedByAppWorkerId())
	require.Equal(t, waitWorkerID, executeRouting.GetStickyAppWorkerId())
	require.Equal(t, executeSource, executeRouting.GetSource())
}

func requirePullActiveRouting(t *testing.T, ctx context.Context, client dexpb.FlowServiceClient, flowID string, workerID string) {
	t.Helper()
	probe := &pullRoutingStateProbe{test: t, ctx: ctx, client: client, flowID: flowID, workerID: workerID, source: dexpb.PullRoutingSource_PULL_ROUTING_SOURCE_FLOW_TYPE_QUEUE}
	require.Eventually(t, probe.hasExpectedRouting, 10*time.Second, 50*time.Millisecond)
}

func (p *pullRoutingStateProbe) hasExpectedRouting() bool {
	state, err := p.client.GetFlowState(p.ctx, &dexpb.GetFlowStateRequest{FlowId: p.flowID})
	if err != nil {
		p.test.Logf("waiting for routing state: %v", err)
		return false
	}
	for _, step := range state.GetActiveStepExecutions() {
		if step.GetStepType() == flowTimeoutStepType {
			continue
		}
		routing := step.GetWaitForPullRouting()
		if state.GetStickyAppWorkerId() == p.workerID && routing.GetServedByAppWorkerId() == p.workerID && routing.GetSource() == p.source {
			return true
		}
	}
	return false
}
