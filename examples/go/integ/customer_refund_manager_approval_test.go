// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package integ

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/blob-cache-go/blobcache"
	"github.com/superdurable/dex/examples/go/products/customer-refund/managerapproval"
	"github.com/superdurable/dex/examples/go/registry"
	"github.com/superdurable/dex/sdk-go/dex"
)

// managerApprovalTestbed gives one test its own ledger and task queue, so its Workers serve only its runs.
type managerApprovalTestbed struct {
	t          *testing.T
	ledgerDir  string
	taskQueue  string
	services   managerapproval.RefundServices
	encryption dex.PayloadEncryptionOptions
	client     *dex.Client
	flow       *managerapproval.ManagerApprovalRefundFlow
}

func newManagerApprovalTestbed(t *testing.T, encryption dex.PayloadEncryptionOptions) *managerApprovalTestbed {
	t.Helper()
	ledgerDir := t.TempDir()
	bed := &managerApprovalTestbed{
		t:          t,
		ledgerDir:  ledgerDir,
		taskQueue:  "refund-" + strconv.FormatInt(time.Now().UnixNano(), 36) + "-" + strconv.FormatInt(flowCounter.Add(1), 10),
		services:   managerapproval.NewRefundServices(ledgerDir),
		encryption: encryption,
		client:     integClient,
		flow:       registry.ManagerApprovalRefund,
	}
	if encryption.Codec != nil {
		bed.flow = bed.newFlow(managerapproval.BillingModeNormal)
		flowRegistry, err := dex.NewRegistry([]dex.Flow{bed.flow})
		require.NoError(t, err)
		bed.client, err = dex.NewClient(flowRegistry, bed.newCache(), dex.ClientOptions{
			FlowServiceAddress: flowServiceAddress(),
			PayloadEncryption:  encryption,
		})
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, bed.client.Close()) })
	}
	return bed
}

func (bed *managerApprovalTestbed) newFlow(billingMode string) *managerapproval.ManagerApprovalRefundFlow {
	flow, err := managerapproval.NewFlowForBillingMode(billingMode, bed.ledgerDir)
	require.NoError(bed.t, err)
	return flow
}

func (bed *managerApprovalTestbed) newCache() *blobcache.Cache {
	cache, err := blobcache.New(&blobcache.Config{Dir: bed.t.TempDir(), MaxBytes: 64 << 20})
	require.NoError(bed.t, err)
	bed.t.Cleanup(func() { require.NoError(bed.t, cache.Close()) })
	return cache
}

// startWorker runs a Worker in billingMode on the test's task queue; call the result to stop it.
func (bed *managerApprovalTestbed) startWorker(billingMode string) func() {
	bed.t.Helper()
	flowRegistry, err := dex.NewRegistry([]dex.Flow{bed.newFlow(billingMode)})
	require.NoError(bed.t, err)
	worker, err := dex.NewWorker(flowRegistry, bed.newCache(), dex.WorkerOptions{
		FlowServiceAddress: flowServiceAddress(),
		Pull: &dex.PullWorkerOptions{
			MaxConcurrentTasks: 8,
			AppWorkerID:        managerapproval.NewWorkerID(),
			TaskQueue:          bed.taskQueue,
		},
		PayloadEncryption: bed.encryption,
	})
	require.NoError(bed.t, err)
	workerResult := make(chan error, 1)
	go func() { workerResult <- worker.Start() }()
	require.NoError(bed.t, worker.WaitPullRegistered(integrationContext(bed.t)))
	var once sync.Once
	stop := func() {
		once.Do(func() {
			stopContext, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			require.NoError(bed.t, errors.Join(worker.Stop(stopContext), <-workerResult))
		})
	}
	bed.t.Cleanup(stop)
	return stop
}

func (bed *managerApprovalTestbed) startRefund(flowID string, scenario string) {
	bed.t.Helper()
	request, found := managerapproval.ScenarioRequest(scenario)
	require.True(bed.t, found)
	_, err := bed.client.StartFlow(
		integrationContext(bed.t), bed.flow, flowID, request, dex.StartFlowOptions{TaskQueue: bed.taskQueue},
	)
	require.NoError(bed.t, err)
}

func (bed *managerApprovalTestbed) waitForManager(flowID string) {
	bed.t.Helper()
	var status string
	require.NoError(bed.t, bed.client.WaitForAttributeMatch(
		integrationContext(bed.t), flowID, managerapproval.CaseStatus,
		dex.AttributeMatchEqual(managerapproval.AwaitingManager), &status,
		dex.WaitForAttributeOptions{RequestTimeout: 50 * time.Second},
	))
}

func (bed *managerApprovalTestbed) decide(flowID string, approve bool, reason string) error {
	rpc := bed.flow.RejectRefund
	if approve {
		rpc = bed.flow.ApproveRefund
	}
	return bed.client.InvokeRPC(
		integrationContext(bed.t), flowID, rpc, managerapproval.DecisionReason{Reason: reason}, nil,
	)
}

func (bed *managerApprovalTestbed) waitForOutcome(flowID string) string {
	bed.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	result, err := bed.client.WaitForFlow(ctx, flowID, dex.WaitForFlowOptions{NeedsResults: true})
	require.NoError(bed.t, err)
	require.Equal(bed.t, dex.FlowCompleted, result.Status)
	require.Len(bed.t, result.Completions, 1)
	var outcome string
	require.NoError(bed.t, result.Completions[0].Output.Decode(&outcome))
	return outcome
}

func (bed *managerApprovalTestbed) readStringAttribute(flowID string, attribute dex.Attribute[string]) string {
	bed.t.Helper()
	var value string
	_, err := bed.client.GetAttributes(integrationContext(bed.t), flowID, dex.ReadAttribute(attribute, &value))
	require.NoError(bed.t, err)
	return value
}

func (bed *managerApprovalTestbed) listRefunds() []managerapproval.RecordedRefund {
	bed.t.Helper()
	refunds, err := bed.services.Billing.ListRefunds()
	require.NoError(bed.t, err)
	return refunds
}

func (bed *managerApprovalTestbed) listSentEmailKeys() []string {
	bed.t.Helper()
	keys, err := bed.services.Email.ListSentKeys()
	require.NoError(bed.t, err)
	return keys
}

func TestManagerApprovalRefundApproved(t *testing.T) {
	bed := newManagerApprovalTestbed(t, dex.PayloadEncryptionOptions{})
	bed.startWorker(managerapproval.BillingModeNormal)
	flowID := newFlowID(t, "refund")
	bed.startRefund(flowID, "normal")
	bed.waitForManager(flowID)
	require.NoError(t, bed.decide(flowID, true, "Outage confirmed"))

	require.Equal(t, "refunded", bed.waitForOutcome(flowID))
	refunds := bed.listRefunds()
	require.Len(t, refunds, 1)
	require.Equal(t, "INV-1249", refunds[0].OrderID)
	require.Equal(t, 1249.00, refunds[0].AmountUSD)
	require.Equal(t, refunds[0].RefundID, bed.readStringAttribute(flowID, managerapproval.RefundID))
	require.Equal(t, "closed-refunded", bed.readStringAttribute(flowID, managerapproval.CaseStatus))
	require.Equal(t, []string{"email-" + flowID}, bed.listSentEmailKeys())
}

func TestManagerApprovalRefundRejected(t *testing.T) {
	bed := newManagerApprovalTestbed(t, dex.PayloadEncryptionOptions{})
	bed.startWorker(managerapproval.BillingModeNormal)
	flowID := newFlowID(t, "refund")
	bed.startRefund(flowID, "normal")
	bed.waitForManager(flowID)
	require.NoError(t, bed.decide(flowID, false, "Credit offered instead"))

	require.Equal(t, "denied", bed.waitForOutcome(flowID))
	require.Empty(t, bed.listRefunds())
	require.Contains(t, bed.readStringAttribute(flowID, managerapproval.CustomerMessage), "Credit offered instead")
}

func TestManagerApprovalRefundPolicyDenials(t *testing.T) {
	tests := []struct {
		name         string
		orderID      string
		expectedNote string
	}{
		{name: "outside the refund window", orderID: "INV-0388", expectedNote: "older than our 30-day refund window"},
		{name: "no service interruption", orderID: "INV-0455", expectedNote: "no service interruption"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			bed := newManagerApprovalTestbed(t, dex.PayloadEncryptionOptions{})
			bed.startWorker(managerapproval.BillingModeNormal)
			flowID := newFlowID(t, "refund")
			request := managerapproval.NorthstarRenewal
			request.OrderID = test.orderID
			_, err := bed.client.StartFlow(
				integrationContext(t), bed.flow, flowID, request, dex.StartFlowOptions{TaskQueue: bed.taskQueue},
			)
			require.NoError(t, err)

			require.Equal(t, "denied", bed.waitForOutcome(flowID))
			require.Equal(t, "closed-denied", bed.readStringAttribute(flowID, managerapproval.CaseStatus))
			require.Contains(t, bed.readStringAttribute(flowID, managerapproval.CustomerMessage), test.expectedNote)
			require.Empty(t, bed.listRefunds())
			require.Equal(t, []string{"email-" + flowID}, bed.listSentEmailKeys())
		})
	}
}

func TestManagerApprovalRefundBlankReasonIsRefused(t *testing.T) {
	bed := newManagerApprovalTestbed(t, dex.PayloadEncryptionOptions{})
	bed.startWorker(managerapproval.BillingModeNormal)
	flowID := newFlowID(t, "refund")
	bed.startRefund(flowID, "normal")
	bed.waitForManager(flowID)

	err := bed.decide(flowID, true, "   ")
	var refused *dex.WorkerInvocationError
	require.ErrorAs(t, err, &refused)
	require.Contains(t, refused.Worker.Detail, "a reason is required")
	require.Equal(t, managerapproval.AwaitingManager, bed.readStringAttribute(flowID, managerapproval.CaseStatus))

	require.NoError(t, bed.decide(flowID, true, "Outage confirmed"))
	require.Equal(t, "refunded", bed.waitForOutcome(flowID))
	require.Len(t, bed.listRefunds(), 1)
}

func TestManagerApprovalRefundConcurrentDecisionsCountOnce(t *testing.T) {
	bed := newManagerApprovalTestbed(t, dex.PayloadEncryptionOptions{})
	bed.startWorker(managerapproval.BillingModeNormal)
	flowID := newFlowID(t, "refund")
	bed.startRefund(flowID, "normal")
	bed.waitForManager(flowID)

	decisions := []bool{true, false}
	results := make([]error, len(decisions))
	var wait sync.WaitGroup
	for index, approve := range decisions {
		wait.Add(1)
		go func() {
			defer wait.Done()
			results[index] = bed.decide(flowID, approve, "Decided concurrently")
		}()
	}
	wait.Wait()

	winners := 0
	expectedOutcome := ""
	for index, err := range results {
		if err == nil {
			winners++
			expectedOutcome = map[bool]string{true: "refunded", false: "denied"}[decisions[index]]
			continue
		}
		var lockConflict *dex.RPCLockConflictError
		var refused *dex.WorkerInvocationError
		switch {
		case errors.As(err, &lockConflict):
		case errors.As(err, &refused):
			require.Contains(t, refused.Worker.Detail, "this refund is not waiting for a manager")
		default:
			require.Failf(t, "unexpected decision error", "%v", err)
		}
	}
	require.Equal(t, 1, winners, "exactly one decision must count: %v", results)
	require.Equal(t, expectedOutcome, bed.waitForOutcome(flowID))
	if expectedOutcome == "refunded" {
		require.Len(t, bed.listRefunds(), 1)
	} else {
		require.Empty(t, bed.listRefunds())
	}
}

func TestManagerApprovalRefundSucceedsAfterPaymentTimeouts(t *testing.T) {
	bed := newManagerApprovalTestbed(t, dex.PayloadEncryptionOptions{})
	bed.startWorker(managerapproval.BillingModeNormal)
	flowID := newFlowID(t, "refund")
	bed.startRefund(flowID, "fail-then-succeed")
	bed.waitForManager(flowID)
	require.NoError(t, bed.decide(flowID, true, "Outage confirmed"))

	require.Equal(t, "refunded", bed.waitForOutcome(flowID))
	require.Len(t, bed.listRefunds(), 1)
	completed := issueRefundExecuteCompletedContext(t, flowID, 0)
	require.EqualValues(t, 3, completed["finalAttempt"])
	lastFailure, ok := completed["lastFailureInfo"].(map[string]any)
	require.True(t, ok, "lastFailureInfo missing: %v", completed)
	require.EqualValues(t, 2, lastFailure["attempt"])
	encodedFailure, err := json.Marshal(lastFailure)
	require.NoError(t, err)
	require.Contains(t, string(encodedFailure), "PaymentTimeout")
}

func TestManagerApprovalRefundTimeTravelReusesIssuedRefund(t *testing.T) {
	bed := newManagerApprovalTestbed(t, dex.PayloadEncryptionOptions{})
	bed.startWorker(managerapproval.BillingModeNormal)
	flowID := newFlowID(t, "refund")
	bed.startRefund(flowID, "small")
	require.Equal(t, "refunded", bed.waitForOutcome(flowID))
	firstRefunds := bed.listRefunds()
	require.Len(t, firstRefunds, 1)
	firstAttempt := currentRunAttempt(t, flowID)

	newAttempt := timeTravelToStepWithDexcli(t, flowID, "IssueRefund")
	require.Greater(t, newAttempt, firstAttempt)
	require.Equal(t, "refunded", bed.waitForOutcome(flowID))
	issueRefundExecuteCompletedContext(t, flowID, newAttempt)
	require.Equal(t, firstRefunds, bed.listRefunds())
	require.Equal(t, firstRefunds[0].RefundID, bed.readStringAttribute(flowID, managerapproval.RefundID))
	require.Equal(t, []string{"email-" + flowID}, bed.listSentEmailKeys())
}

func TestManagerApprovalRefundTimeTravelIssuesTheMissingRefund(t *testing.T) {
	bed := newManagerApprovalTestbed(t, dex.PayloadEncryptionOptions{})
	stopCallMissingWorker := bed.startWorker(managerapproval.BillingModeCallMissing)
	flowID := newFlowID(t, "refund")
	bed.startRefund(flowID, "normal")
	bed.waitForManager(flowID)
	require.NoError(t, bed.decide(flowID, true, "Outage confirmed"))
	require.Equal(t, "refunded", bed.waitForOutcome(flowID))
	require.Empty(t, bed.readStringAttribute(flowID, managerapproval.RefundID))
	require.Empty(t, bed.listRefunds())
	stopCallMissingWorker()

	bed.startWorker(managerapproval.BillingModeNormal)
	timeTravelToStepWithDexcli(t, flowID, "IssueRefund")
	require.Equal(t, "refunded", bed.waitForOutcome(flowID))
	refunds := bed.listRefunds()
	require.Len(t, refunds, 1)
	require.Equal(t, "refund-"+flowID, refunds[0].IdempotencyKey)
	require.Equal(t, refunds[0].RefundID, bed.readStringAttribute(flowID, managerapproval.RefundID))
	require.Equal(t, []string{"email-" + flowID}, bed.listSentEmailKeys())
}

func TestManagerApprovalRefundEncryptionKeepsCaseStatusReadable(t *testing.T) {
	encryption, err := managerapproval.NewFilePayloadEncryption(writePayloadKeyFile(t))
	require.NoError(t, err)
	bed := newManagerApprovalTestbed(t, encryption)
	bed.startWorker(managerapproval.BillingModeNormal)
	flowID := newFlowID(t, "refund")
	bed.startRefund(flowID, "small")
	require.Equal(t, "refunded", bed.waitForOutcome(flowID))
	require.Contains(t, bed.readStringAttribute(flowID, managerapproval.CustomerMessage), "refunded 49.00 USD")

	var caseStatus string
	_, err = integClient.GetAttributes(
		integrationContext(t), flowID, dex.ReadAttribute(managerapproval.CaseStatus, &caseStatus),
	)
	require.NoError(t, err)
	require.Equal(t, "closed-refunded", caseStatus)
	var message string
	_, err = integClient.GetAttributes(
		integrationContext(t), flowID, dex.ReadAttribute(managerapproval.CustomerMessage, &message),
	)
	require.ErrorIs(t, err, dex.ErrPayloadCodecNotConfigured)
}

func TestManagerApprovalRefundHTTPRoutes(t *testing.T) {
	requestedFlowID := newFlowID(t, "customer-refund")
	status, started := getExamplesJSON(t, "/products/customer-refund/manager-approval/start",
		url.Values{"flowId": {requestedFlowID}, "scenario": {"normal"}})
	require.Equal(t, http.StatusOK, status, "%v", started)
	require.Equal(t, requestedFlowID, started["flowID"])
	require.NotEmpty(t, started["runID"])

	status, body := getExamplesJSON(t, "/products/customer-refund/manager-approval/start",
		url.Values{"scenario": {"unknown"}})
	require.Equal(t, http.StatusBadRequest, status, "%v", body)

	describePath := "/products/customer-refund/manager-approval/describe"
	describeQuery := url.Values{"flowId": {requestedFlowID}}
	require.Eventually(t, func() bool {
		status, described := getExamplesJSON(t, describePath, describeQuery)
		return status == http.StatusOK && described["caseStatus"] == managerapproval.AwaitingManager
	}, 30*time.Second, 200*time.Millisecond)

	decidePath := "/products/customer-refund/manager-approval/decide"
	status, body = getExamplesJSON(t, decidePath,
		url.Values{"flowId": {requestedFlowID}, "decision": {"approve"}, "reason": {"  "}})
	require.Equal(t, http.StatusBadRequest, status, "%v", body)
	require.Equal(t, "a reason is required", body["error"])

	status, body = getExamplesJSON(t, decidePath,
		url.Values{"flowId": {requestedFlowID}, "decision": {"approve"}, "reason": {"Outage confirmed"}})
	require.Equal(t, http.StatusOK, status, "%v", body)
	require.Equal(t, dex.FlowCompleted, waitForFlow(t, requestedFlowID).Status)

	status, described := getExamplesJSON(t, describePath, describeQuery)
	require.Equal(t, http.StatusOK, status, "%v", described)
	require.Equal(t, "completed", described["status"])
	require.Equal(t, "closed-refunded", described["caseStatus"])
	refundID, _ := described["refundId"].(string)
	require.True(t, strings.HasPrefix(refundID, "rf_"), "%v", described)
	refunds, err := managerapproval.NewRefundServices(sharedRefundLedgerDir).Billing.ListRefunds()
	require.NoError(t, err)
	require.Contains(t, refunds, managerapproval.RecordedRefund{
		RefundID: refundID, OrderID: "INV-1249", AmountUSD: 1249.00,
		IdempotencyKey: "refund-" + requestedFlowID,
	})

	status, body = getExamplesJSON(t, decidePath,
		url.Values{"flowId": {requestedFlowID}, "decision": {"reject"}, "reason": {"Too late"}})
	require.GreaterOrEqual(t, status, 400, "%v", body)
	require.Less(t, status, 500, "%v", body)
}

func getExamplesJSON(t *testing.T, path string, query url.Values) (int, map[string]any) {
	t.Helper()
	request, err := http.NewRequestWithContext(
		integrationContext(t), http.MethodGet, examplesAPIURL+path+"?"+query.Encode(), nil,
	)
	require.NoError(t, err)
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	defer response.Body.Close()
	encoded, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	var body map[string]any
	require.NoError(t, json.Unmarshal(encoded, &body), "decode %s", encoded)
	return response.StatusCode, body
}

type refundHistoryPage struct {
	Events []refundHistoryEvent `json:"events"`
}

type refundHistoryEvent struct {
	Type       string         `json:"type"`
	RunAttempt int            `json:"runAttempt"`
	Payload    map[string]any `json:"payload"`
}

func readRefundHistoryEvents(t *testing.T, flowID string) []refundHistoryEvent {
	t.Helper()
	var page refundHistoryPage
	runDexcliJSON(t, []string{
		"flow", "history", flowID, "--server", flowServiceAddress(), "--output", "json", "--all",
	}, &page)
	return page.Events
}

// issueRefundExecuteCompletedContext reads the one IssueRefund completion; runAttempt 0 matches any attempt.
func issueRefundExecuteCompletedContext(t *testing.T, flowID string, runAttempt int) map[string]any {
	t.Helper()
	var contexts []map[string]any
	for _, event := range readRefundHistoryEvents(t, flowID) {
		if event.Type != "StepExecuteCompleted" || historyEventStepType(event.Payload) != "IssueRefund" {
			continue
		}
		if runAttempt != 0 && event.RunAttempt != runAttempt {
			continue
		}
		stepContext, _ := event.Payload["context"].(map[string]any)
		contexts = append(contexts, stepContext)
	}
	require.Len(t, contexts, 1)
	return contexts[0]
}

func currentRunAttempt(t *testing.T, flowID string) int {
	t.Helper()
	events := readRefundHistoryEvents(t, flowID)
	require.NotEmpty(t, events)
	return events[len(events)-1].RunAttempt
}

// timeTravelToStepWithDexcli runs the README's Time Travel command and returns the new run attempt.
func timeTravelToStepWithDexcli(t *testing.T, flowID string, stepType string) int {
	t.Helper()
	var response struct {
		RunAttempt int `json:"runAttempt"`
	}
	runDexcliJSON(t, []string{
		"flow", "time-travel", flowID, "--server", flowServiceAddress(), "--output", "json",
		"--type", "step-type", "--target", stepType, "--reason", "billing call restored", "--yes",
	}, &response)
	return response.RunAttempt
}
