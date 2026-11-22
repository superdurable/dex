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
	"runtime"
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
	requireRefundLedgerPlatform(t)
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
	start := make(chan struct{})
	var wait sync.WaitGroup
	for index, approve := range decisions {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			results[index] = bed.decide(flowID, approve, "Decided concurrently")
		}()
	}
	close(start)
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
		var closing *dex.FlowClosingError
		var closed *dex.FlowNotActiveOrNotFoundError
		var refused *dex.WorkerInvocationError
		switch {
		case errors.As(err, &lockConflict), errors.As(err, &closing), errors.As(err, &closed):
		case errors.As(err, &refused):
			require.NotNil(t, refused.Worker)
			require.Equal(t, "managerapproval.RefundNotAwaitingManagerError", refused.Worker.Type)
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
	requireRefundLedgerPlatform(t)
	flowID := newFlowID(t, "customer-refund")
	client := refundHTTPTestClient{t: t}
	client.start(flowID, "fail-then-succeed")
	client.requireError("start", url.Values{"flowId": {flowID}}, http.StatusConflict, "")
	client.requireError("start", url.Values{"scenario": {"unknown"}}, http.StatusBadRequest, "")
	client.requireError("describe", nil, http.StatusBadRequest, "flowId is required")
	client.requireError("decide", nil, http.StatusBadRequest, "flowId is required")

	unknownFlowID := newFlowID(t, "unknown")
	client.requireError("decide", refundDecisionQuery(unknownFlowID, "invalid", "Reason"), http.StatusBadRequest, "decision must be approve or reject")
	client.requireError("decide", refundDecisionQuery(unknownFlowID, "approve", "  "), http.StatusBadRequest, "a reason is required")
	client.requireRefundNotFound(unknownFlowID)
	status, order := getExamplesJSON(t, "/products/order-processing/start", nil)
	require.Equal(t, http.StatusOK, status, "%v", order)
	orderFlowID, ok := order["flowID"].(string)
	require.True(t, ok, "%v", order)
	client.requireRefundNotFound(orderFlowID)

	describeQuery := url.Values{"workflowId": {flowID}}
	client.requireDescription(describeQuery, flowID, "running", managerapproval.AwaitingManager, nil)
	client.requireDecision(flowID, "approve")
	status, running := client.get("describe", describeQuery)
	require.Equal(t, http.StatusOK, status, "%v", running)
	require.Equal(t, "running", running["status"])
	client.requireError("decide", refundDecisionQuery(flowID, "reject", "Already decided"), http.StatusConflict, "this refund is not waiting for a manager")
	require.Equal(t, dex.FlowCompleted, waitForFlow(t, flowID).Status)

	client.requireCompletedRefund(describeQuery, flowID, "INV-1249", 1249.00)
	client.requireError("decide", refundDecisionQuery(flowID, "reject", "Too late"), http.StatusNotFound, "no refund with this Flow ID")

	rejectedFlowID := client.start("", "")
	client.requireDecision(rejectedFlowID, "reject")
	require.Equal(t, dex.FlowCompleted, waitForFlow(t, rejectedFlowID).Status)
	client.requireDescription(url.Values{"flowId": {rejectedFlowID}, "workflowId": {""}}, rejectedFlowID, "completed", "closed-denied", nil)
}

func TestManagerApprovalRefundHTTPAutomaticOutcomes(t *testing.T) {
	requireRefundLedgerPlatform(t)
	client := refundHTTPTestClient{t: t}

	smallFlowID := client.start("", "small")
	require.Equal(t, dex.FlowCompleted, waitForFlow(t, smallFlowID).Status)
	client.requireCompletedRefund(url.Values{"flowId": {smallFlowID}}, smallFlowID, "INV-0049", 49.00)

	deniedFlowID := client.start("", "deny")
	require.Equal(t, dex.FlowCompleted, waitForFlow(t, deniedFlowID).Status)
	client.requireDescription(url.Values{"flowId": {deniedFlowID}}, deniedFlowID, "completed", "closed-denied", nil)
}

func TestManagerApprovalRefundHTTPConcurrentDecisions(t *testing.T) {
	requireRefundLedgerPlatform(t)
	client := refundHTTPTestClient{t: t}
	flowID := client.start("", "normal")
	query := refundDecisionQuery(flowID, "reject", "Concurrent decision")
	responses := make([]int, 2)
	start := make(chan struct{})
	var wait sync.WaitGroup
	for index := range responses {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			responses[index], _ = client.get("decide", query)
		}()
	}
	close(start)
	wait.Wait()
	if responses[0] > responses[1] {
		responses[0], responses[1] = responses[1], responses[0]
	}
	require.Equal(t, http.StatusOK, responses[0], "%v", responses)
	switch responses[1] {
	case http.StatusConflict, http.StatusNotFound:
	default:
		require.Failf(t, "unexpected concurrent decision status", "%v", responses)
	}
	require.Equal(t, dex.FlowCompleted, waitForFlow(t, flowID).Status)
	client.requireDescription(url.Values{"flowId": {flowID}}, flowID, "completed", "closed-denied", nil)
}

type refundHTTPTestClient struct {
	t *testing.T
}

func (client refundHTTPTestClient) get(route string, query url.Values) (int, map[string]any) {
	client.t.Helper()
	return getExamplesJSON(client.t, "/products/customer-refund/manager-approval/"+route, query)
}

func (client refundHTTPTestClient) start(flowID, scenario string) string {
	client.t.Helper()
	status, started := client.get("start", url.Values{"flowId": {flowID}, "scenario": {scenario}})
	require.Equal(client.t, http.StatusOK, status, "%v", started)
	require.Len(client.t, started, 2)
	actualFlowID, ok := started["flowID"].(string)
	require.True(client.t, ok, "%v", started)
	if flowID == "" {
		require.True(client.t, strings.HasPrefix(actualFlowID, "customer-refund-"), "%v", started)
	} else {
		require.Equal(client.t, flowID, actualFlowID)
	}
	runID, ok := started["runID"].(string)
	require.True(client.t, ok, "%v", started)
	require.NotEmpty(client.t, runID)
	return actualFlowID
}

func (client refundHTTPTestClient) requireDecision(flowID, decision string) {
	client.t.Helper()
	status, body := client.get("decide", refundDecisionQuery(flowID, decision, "Policy reviewed"))
	require.Equal(client.t, http.StatusOK, status, "%v", body)
	require.Equal(client.t, map[string]any{"flowID": flowID, "decision": decision}, body)
}

func (client refundHTTPTestClient) requireRefundNotFound(flowID string) {
	client.t.Helper()
	client.requireError("describe", url.Values{"flowId": {flowID}}, http.StatusNotFound, "no refund with this Flow ID")
	client.requireError("decide", refundDecisionQuery(flowID, "approve", "Policy reviewed"), http.StatusNotFound, "no refund with this Flow ID")
}

func (client refundHTTPTestClient) requireError(route string, query url.Values, expectedStatus int, expectedError string) {
	client.t.Helper()
	status, body := client.get(route, query)
	require.Equal(client.t, expectedStatus, status, "%v", body)
	require.Len(client.t, body, 1)
	message, ok := body["error"].(string)
	require.True(client.t, ok, "%v", body)
	require.NotEmpty(client.t, message)
	if expectedError != "" {
		require.Equal(client.t, expectedError, message)
	}
}

func (client refundHTTPTestClient) requireDescription(query url.Values, flowID, flowStatus, caseStatus string, refundID any) {
	client.t.Helper()
	status, body := client.get("describe", query)
	require.Equal(client.t, http.StatusOK, status, "%v", body)
	require.Equal(client.t, refundDescription(flowID, flowStatus, caseStatus, refundID), body)
}

func (client refundHTTPTestClient) requireCompletedRefund(query url.Values, flowID, orderID string, amountUSD float64) {
	client.t.Helper()
	status, described := client.get("describe", query)
	require.Equal(client.t, http.StatusOK, status, "%v", described)
	refundID, _ := described["refundId"].(string)
	require.True(client.t, strings.HasPrefix(refundID, "rf_"), "%v", described)
	require.Equal(client.t, refundDescription(flowID, "completed", "closed-refunded", refundID), described)
	refunds, err := managerapproval.NewRefundServices(sharedRefundLedgerDir).Billing.ListRefunds()
	require.NoError(client.t, err)
	key := "refund-" + flowID
	var recorded []managerapproval.RecordedRefund
	for _, refund := range refunds {
		if refund.IdempotencyKey == key {
			recorded = append(recorded, refund)
		}
	}
	require.Equal(client.t, []managerapproval.RecordedRefund{{
		RefundID: refundID, OrderID: orderID, AmountUSD: amountUSD, IdempotencyKey: key,
	}}, recorded)
}

func refundDescription(flowID, flowStatus, caseStatus string, refundID any) map[string]any {
	return map[string]any{"flowID": flowID, "status": flowStatus, "caseStatus": caseStatus, "refundId": refundID}
}

func refundDecisionQuery(flowID, decision, reason string) url.Values {
	return url.Values{"flowId": {flowID}, "decision": {decision}, "reason": {reason}}
}

func requireRefundLedgerPlatform(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("the customer refund ledger is supported only on macOS and Linux")
	}
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
