// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package integ

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"github.com/superdurable/dex/server-zd/integ/workflow/result_loss"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Drop decisions hash the Flow ID, so reproducible runs need fixed IDs; each run has its own server.
const (
	resultLossPipelineFlowID = "result-loss-pipeline"
	resultLossHoldFlowID     = "result-loss-hold"
	resultLossSeed           = "7"
	resultLossRPCCalls       = 8
	// injectedLossText appears in every error that the result loss injection causes.
	injectedLossText = "dev fault injection dropped the Worker result"
	// workerDisconnectedText starts the dispatch reason and detail of a Worker disconnect's Step failure.
	workerDisconnectedText = "DISPATCH_TASK_ERROR_REASON_WORKER_DISCONNECTED Worker disconnected during pull dispatch"
	// minimumRetryGap is the one-second retry interval, less a margin for timer granularity.
	minimumRetryGap = 950 * time.Millisecond
)

// resultLossDispatchPath selects how the first Step attempt reaches the Worker.
type resultLossDispatchPath string

const (
	// directSlotPath registers the Worker first, so the attempt takes an idle Worker slot.
	directSlotPath resultLossDispatchPath = "direct-slot"
	// claimPath registers the Worker after the attempt is queued, so matching delivers it by Claim.
	claimPath resultLossDispatchPath = "claim"
)

type pipelineRunOutcome struct {
	output        string
	externalCalls map[string]int64
	attemptsSeen  []string
	// failureTexts maps each retried method, such as "S2_execute", to its last failure text.
	failureTexts map[string]string
	// attributeKeys lists the Flow's Attribute keys after it completed, sorted.
	attributeKeys []string
}

func TestStepResultLossInjectionRetriesAndReproduces(t *testing.T) {
	stepLossFlags := []string{"--inject-step-result-loss", "0.5", "--inject-result-loss-seed", resultLossSeed}
	injectedAttempts := make(map[resultLossDispatchPath][]string)
	for _, dispatchPath := range []resultLossDispatchPath{directSlotPath, claimPath} {
		t.Run(string(dispatchPath), func(t *testing.T) {
			baseline := runResultLossPipeline(t, dispatchPath, nil)
			injected := runResultLossPipeline(t, dispatchPath, stepLossFlags)
			repeated := runResultLossPipeline(t, dispatchPath, stepLossFlags)
			t.Logf("injected external calls %v, attempts %v", injected.externalCalls, injected.attemptsSeen)

			require.Equal(t, "start/S1/S2/S3", baseline.output)
			require.Equal(t, map[string]int64{"S1_execute": 1, "S2_execute": 1, "S3_execute": 1}, baseline.externalCalls)
			require.Empty(t, baseline.failureTexts)
			require.Equal(t, baseline.output, injected.output, "dropped results must not change the Flow result")
			require.Greater(t, sumCalls(injected.externalCalls), sumCalls(baseline.externalCalls),
				"the fake external service must see repeated calls: %v", injected.externalCalls)
			require.Equal(t, injected.attemptsSeen, repeated.attemptsSeen, "the same seed must drop the same attempts")
			require.Equal(t, injected.externalCalls, repeated.externalCalls)
			require.Equal(t, injected.failureTexts, repeated.failureTexts)
			require.NotEmpty(t, injected.failureTexts)
			for method, failureText := range injected.failureTexts {
				require.True(t, strings.HasPrefix(failureText, workerDisconnectedText), "%s failed with %q", method, failureText)
				require.Equal(t, 1, strings.Count(failureText, injectedLossText), "%s failed with %q", method, failureText)
			}
			require.Equal(t, successfulAttemptKeys(t, injected.attemptsSeen), injected.attributeKeys,
				"only the results that arrived write Attributes")
			injectedAttempts[dispatchPath] = injected.attemptsSeen
		})
	}
	require.Equal(t, injectedAttempts[directSlotPath], injectedAttempts[claimPath],
		"the dispatch path must not change which results are dropped")
}

func TestRPCResultLossInjectionReturnsUnavailableAndReproduces(t *testing.T) {
	baselineCalls, baselineUnavailable := runResultLossRPCs(t, nil)
	rpcLossFlags := []string{"--inject-rpc-result-loss", "0.5", "--inject-result-loss-seed", resultLossSeed}
	injectedCalls, injectedUnavailable := runResultLossRPCs(t, rpcLossFlags)
	repeatedCalls, repeatedUnavailable := runResultLossRPCs(t, rpcLossFlags)
	t.Logf("injected RPC calls %d, UNAVAILABLE answers per call %v", injectedCalls, injectedUnavailable)

	require.Equal(t, int64(resultLossRPCCalls), baselineCalls)
	require.Equal(t, make([]int, resultLossRPCCalls), baselineUnavailable)
	require.Greater(t, injectedCalls, baselineCalls, "the fake external service must see repeated RPC calls")
	require.Equal(t, int64(resultLossRPCCalls)+int64(sumInts(injectedUnavailable)), injectedCalls,
		"each injected UNAVAILABLE answer must follow one Worker call")
	require.Equal(t, injectedUnavailable, repeatedUnavailable, "the same seed must drop the same RPC results")
	require.Equal(t, injectedCalls, repeatedCalls)
}

// runResultLossPipeline runs the S1 -> S2 -> S3 Flow on a new server started with devFlags.
func runResultLossPipeline(t *testing.T, dispatchPath resultLossDispatchPath, devFlags []string) pipelineRunOutcome {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	retryChecks := &retryStateChecks{}
	worker := result_loss.NewResultLossWorker(retryChecks.checkBeforeRetry)
	workerTarget := startWorker(t, worker)
	testConfig := serverZDTestConfig{DevFlags: devFlags}
	if dispatchPath == claimPath {
		metricsAddress, err := reserveBinaryDexAddress()
		require.NoError(t, err)
		testConfig.MetricsListen = metricsAddress
	}
	runtime := startDexService(t, testConfig)
	retryChecks.start(ctx, runtime.FlowClient)

	startRequest := &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             resultLossPipelineFlowID,
		FlowType:           result_loss.FlowType,
		FlowTimeoutSeconds: 100,
		StartStepType:      result_loss.Step1,
		StepInput:          stringValue("start"),
		StepOptions:        result_loss.FastRetryStepOptions(),
		FlowStartOptions:   withWorkerTarget(nil, workerTarget),
	}
	if dispatchPath == claimPath {
		// The unwrapped client starts the Flow without registering the Worker's streams.
		pullClient := runtime.FlowClient.(*pullWorkerFlowClient)
		_, err := pullClient.FlowServiceClient.StartFlow(ctx, startRequest)
		require.NoError(t, err)
		waitForQueuedStepTask(t, testConfig.MetricsListen)
		registerPullWorkerFlowType(t, pullClient, workerTarget, result_loss.FlowType)
	} else {
		_, err := runtime.FlowClient.StartFlow(ctx, startRequest)
		require.NoError(t, err)
	}
	response := waitForCompletedFlow(t, ctx, runtime.FlowClient, resultLossPipelineFlowID)
	require.Len(t, response.GetResults(), 1)
	require.Empty(t, retryChecks.violationList())
	attemptsSeen := worker.AttemptsSeen()
	requireRetryBackoff(t, worker, attemptsSeen)
	return pipelineRunOutcome{
		output:        response.GetResults()[0].GetCompletedStepOutput().GetStringValue(),
		externalCalls: worker.ExternalCalls(),
		attemptsSeen:  attemptsSeen,
		failureTexts:  methodFailureTexts(t, ctx, runtime.FlowClient, resultLossPipelineFlowID),
		attributeKeys: sortedFlowAttributeKeys(t, ctx, runtime.FlowClient, resultLossPipelineFlowID),
	}
}

// waitForQueuedStepTask polls the metrics listener until a Step task waits in the durable task queue.
func waitForQueuedStepTask(t *testing.T, metricsAddress string) {
	t.Helper()
	require.EventuallyWithT(t, func(collect *assert.CollectT) {
		queuedTasks, err := scrapeGaugeValue(metricsAddress, "dexzd_step_task_queue_entries")
		require.NoError(collect, err)
		require.Positive(collect, queuedTasks)
	}, 30*time.Second, 100*time.Millisecond)
}

// scrapeGaugeValue returns the value of an unlabeled gauge from the server's /metrics text.
func scrapeGaugeValue(metricsAddress string, name string) (_ float64, scrapeErr error) {
	response, err := http.Get("http://" + metricsAddress + "/metrics")
	if err != nil {
		return 0, err
	}
	defer func() { scrapeErr = errors.Join(scrapeErr, response.Body.Close()) }()
	scanner := bufio.NewScanner(response.Body)
	for scanner.Scan() {
		value, isGauge := strings.CutPrefix(scanner.Text(), name+" ")
		if isGauge {
			return strconv.ParseFloat(strings.TrimSpace(value), 64)
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, err
	}
	return 0, fmt.Errorf("metric %s is not exported", name)
}

// registerPullWorkerFlowType opens the Worker's pull streams, as StartFlow does on the wrapped client.
func registerPullWorkerFlowType(
	t *testing.T,
	pullClient *pullWorkerFlowClient,
	workerTarget *dexpb.WorkerTarget,
	flowType string,
) {
	t.Helper()
	loaded, isRegistered := pullWorkerAdapters.Load(workerTarget)
	require.True(t, isRegistered, "startWorker registers the pull Worker target")
	require.NoError(t, loaded.(*pullWorkerAdapter).registerFlowType(
		t, pullClient.serverAddress, pullClient.credential, flowType,
	))
}

// retryStateChecks checks, before a retried method answers, that the dropped attempts left no state.
type retryStateChecks struct {
	mutex      sync.Mutex
	ctx        context.Context
	flowClient dexpb.FlowServiceClient
	violations []string
}

func (checks *retryStateChecks) start(ctx context.Context, flowClient dexpb.FlowServiceClient) {
	checks.mutex.Lock()
	defer checks.mutex.Unlock()
	checks.ctx = ctx
	checks.flowClient = flowClient
}

// checkBeforeRetry runs on the Worker's handler goroutine, so it records violations instead of failing.
func (checks *retryStateChecks) checkBeforeRetry(handlerName string, attempt int32) {
	checks.mutex.Lock()
	ctx, flowClient := checks.ctx, checks.flowClient
	checks.mutex.Unlock()
	attributes, err := flowClient.GetAttributes(ctx, &dexpb.GetAttributesRequest{
		FlowId: resultLossPipelineFlowID, AllKeys: true,
	})
	if err != nil {
		checks.recordViolation(fmt.Sprintf("read Attributes before %s retry %d: %v", handlerName, attempt, err))
		return
	}
	messages, err := flowClient.GetChannelMessages(ctx, &dexpb.GetChannelMessagesRequest{
		FlowId: resultLossPipelineFlowID, ChannelName: result_loss.LogChannel,
	})
	if err != nil {
		checks.recordViolation(fmt.Sprintf("read Channel messages before %s retry %d: %v", handlerName, attempt, err))
		return
	}
	for droppedAttempt := int32(1); droppedAttempt < attempt; droppedAttempt++ {
		droppedKey := result_loss.AttemptAttributeKey(handlerName, droppedAttempt)
		for _, attribute := range attributes.GetAttributes() {
			if attribute.GetKey() == droppedKey {
				checks.recordViolation("dropped attempt wrote Attribute " + droppedKey)
			}
		}
		droppedName := result_loss.AttemptName(handlerName, droppedAttempt)
		for _, message := range messages.GetMessages() {
			if message.GetValue().GetStringValue() == droppedName {
				checks.recordViolation("dropped attempt published " + droppedName)
			}
		}
	}
}

func (checks *retryStateChecks) recordViolation(violation string) {
	checks.mutex.Lock()
	defer checks.mutex.Unlock()
	checks.violations = append(checks.violations, violation)
}

func (checks *retryStateChecks) violationList() []string {
	checks.mutex.Lock()
	defer checks.mutex.Unlock()
	return append([]string(nil), checks.violations...)
}

// attemptStartRecorder reports when the Worker received each method attempt.
type attemptStartRecorder interface {
	AttemptStart(attemptName string) (time.Time, bool)
}

// requireRetryBackoff requires every retry to start at least one retry interval after the dropped attempt started.
func requireRetryBackoff(t *testing.T, worker attemptStartRecorder, attemptsSeen []string) {
	t.Helper()
	for _, attemptName := range attemptsSeen {
		handlerName, attempt := parseAttemptName(t, attemptName)
		if attempt == 1 {
			continue
		}
		retryStart, hasRetryStart := worker.AttemptStart(attemptName)
		require.True(t, hasRetryStart, "%s was seen without a start time", attemptName)
		droppedStart, hasDroppedStart := worker.AttemptStart(result_loss.AttemptName(handlerName, attempt-1))
		require.True(t, hasDroppedStart, "%s has no earlier attempt", attemptName)
		require.GreaterOrEqual(t, retryStart.Sub(droppedStart), minimumRetryGap,
			"%s started before the retry interval elapsed", attemptName)
	}
}

// methodFailureTexts returns the last failure text of each Step method that needed a retry.
func methodFailureTexts(
	t *testing.T,
	ctx context.Context,
	flowClient dexpb.FlowServiceClient,
	flowID string,
) map[string]string {
	t.Helper()
	history, err := flowClient.GetHistoryEvents(ctx, &dexpb.GetHistoryEventsRequest{FlowId: flowID})
	require.NoError(t, err)
	require.Empty(t, history.GetNextPageToken(), "the run's history fits one page")
	failureTexts := make(map[string]string)
	for _, event := range history.GetEvents() {
		method, eventContext := "", (*dexpb.StepMethodEventContext)(nil)
		if completed := event.GetStepWaitForCompleted(); completed != nil {
			method, eventContext = "_waitFor", completed.GetContext()
		} else if completed := event.GetStepExecuteCompleted(); completed != nil {
			method, eventContext = "_execute", completed.GetContext()
		} else {
			continue
		}
		if eventContext.GetFinalAttempt() > 1 {
			failure := eventContext.GetLastFailureInfo().GetDetails()
			failureTexts[eventContext.GetStepType()+method] = failure.GetDispatchTaskErrorReason().String() + " " + failure.GetDetail()
		}
	}
	return failureTexts
}

func sortedFlowAttributeKeys(t *testing.T, ctx context.Context, flowClient dexpb.FlowServiceClient, flowID string) []string {
	t.Helper()
	attributes, err := flowClient.GetAttributes(ctx, &dexpb.GetAttributesRequest{FlowId: flowID, AllKeys: true})
	require.NoError(t, err)
	keys := attributeKeys(attributes.GetAttributes())
	sort.Strings(keys)
	return keys
}

// successfulAttemptKeys returns the sorted Attribute keys of each method's last attempt.
func successfulAttemptKeys(t *testing.T, attemptsSeen []string) []string {
	t.Helper()
	lastAttempts := make(map[string]int32)
	for _, attemptName := range attemptsSeen {
		handlerName, attempt := parseAttemptName(t, attemptName)
		lastAttempts[handlerName] = attempt
	}
	keys := make([]string, 0, len(lastAttempts))
	for handlerName, attempt := range lastAttempts {
		keys = append(keys, result_loss.AttemptAttributeKey(handlerName, attempt))
	}
	sort.Strings(keys)
	return keys
}

// parseAttemptName splits an attempt name such as "S1_execute#2" into its handler name and attempt.
func parseAttemptName(t *testing.T, attemptName string) (string, int32) {
	t.Helper()
	handlerName, attemptText, hasAttempt := strings.Cut(attemptName, "#")
	require.True(t, hasAttempt, "attempt name %q has no attempt number", attemptName)
	attempt, err := strconv.ParseInt(attemptText, 10, 32)
	require.NoError(t, err)
	return handlerName, int32(attempt)
}

// waitForCompletedFlow waits for the Flow's results; the server caps each wait, so it polls.
func waitForCompletedFlow(
	t *testing.T,
	ctx context.Context,
	flowClient dexpb.FlowServiceClient,
	flowID string,
) *dexpb.FlowResult {
	t.Helper()
	var response *dexpb.FlowResult
	require.EventuallyWithT(t, func(collect *assert.CollectT) {
		var err error
		response, err = flowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{
			FlowId: flowID, NeedsResults: true, WaitTimeSeconds: 10,
		})
		require.NoError(collect, err)
		require.NotEqual(collect, dexpb.FlowStatus_FLOW_STATUS_RUNNING, response.GetFlowStatus())
	}, 90*time.Second, 100*time.Millisecond)
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_COMPLETED, response.GetFlowStatus())
	return response
}

// runResultLossRPCs calls the charge RPC on a new server started with devFlags, retrying each UNAVAILABLE answer.
//
// Returns the fake service's RPC call count and the injected UNAVAILABLE answers of each logical call.
func runResultLossRPCs(t *testing.T, devFlags []string) (int64, []int) {
	t.Helper()
	worker := result_loss.NewResultLossWorker(nil)
	workerTarget := startWorker(t, worker)
	runtime := startDexService(t, serverZDTestConfig{DevFlags: devFlags})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	_, err := runtime.FlowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             resultLossHoldFlowID,
		FlowType:           result_loss.FlowType,
		FlowTimeoutSeconds: 100,
		StartStepType:      result_loss.StepHold,
		FlowStartOptions:   withWorkerTarget(nil, workerTarget),
	})
	require.NoError(t, err)
	injectedUnavailable := make([]int, resultLossRPCCalls)
	expectedAttributes := make([]string, 0, resultLossRPCCalls)
	for call := range resultLossRPCCalls {
		input := fmt.Sprintf("c%d", call+1)
		chargeKey := result_loss.ChargePrefix + input
		expectedAttributes = append(expectedAttributes, chargeKey)
		// A careful caller retries with the same request ID; nothing was recorded, so the handler runs again.
		requestID := newRequestID()
		for {
			response, rpcErr := runtime.FlowClient.InvokeRPC(ctx, &dexpb.InvokeRPCRequest{
				RequestId:       requestID,
				FlowId:          resultLossHoldFlowID,
				RpcName:         result_loss.RPCCharge,
				Input:           stringValue(input),
				TimeoutSeconds:  10,
				IsTransactional: true,
			})
			if status.Code(rpcErr) != codes.Unavailable {
				require.NoError(t, rpcErr)
				require.Equal(t, input, response.GetOutput().GetStringValue())
				break
			}
			// Only an answer that names the injection counts; any other UNAVAILABLE is a transient retry.
			if !strings.Contains(status.Convert(rpcErr).Message(), injectedLossText) {
				t.Logf("retrying a transient UNAVAILABLE answer: %v", rpcErr)
				continue
			}
			injectedUnavailable[call]++
			require.Less(t, injectedUnavailable[call], 20, "a 0.5 loss must let a retry through")
			require.NotContains(t, sortedFlowAttributeKeys(t, ctx, runtime.FlowClient, resultLossHoldFlowID), chargeKey,
				"a dropped RPC result must commit nothing")
		}
	}
	require.Equal(t, expectedAttributes, sortedFlowAttributeKeys(t, ctx, runtime.FlowClient, resultLossHoldFlowID),
		"only the RPC results that arrived commit")
	return worker.ExternalCalls()[result_loss.RPCCharge+"_rpc"], injectedUnavailable
}

func sumCalls(calls map[string]int64) int64 {
	var total int64
	for _, count := range calls {
		total += count
	}
	return total
}

func sumInts(values []int) int {
	total := 0
	for _, value := range values {
		total += value
	}
	return total
}
