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

package dealdsl_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/examples/go/products/deal-dsl"
	"github.com/superdurable/dex/sdk-go/dex"
)

type dealDSLStartResponse struct {
	FlowID string `json:"flowID"`
	RunID  string `json:"runID"`
}

type dealDSLListResponse struct {
	Executions []dealdsl.DealExecution `json:"executions"`
}

type dealDSLProcessListResponse struct {
	Processes []dealdsl.DealProcess `json:"processes"`
}

func TestDealDSLSmokeStart(t *testing.T) {
	processID := newFlowID(t, "deal-dsl-smoke-process")
	buyerID := newFlowID(t, "deal-dsl-smoke-buyer")
	process := dealdsl.DealProcess{
		ProcessID:    processID,
		ItemID:       "smoke-item",
		ItemName:     "Smoke-test item",
		InitialState: "terminal",
		InitialStateData: map[string]string{
			"smoke": "true",
		},
		States: []dealdsl.StateDefinition{{
			Name: "terminal",
		}},
	}
	requestDealDSLAPI(t, http.MethodPost, "/products/deal-dsl/api/processes", process, http.StatusCreated, nil)
	execution := startDealDSLExecution(t, processID, buyerID)
	completed := waitForDealDSLExecution(t, execution.FlowID, executionCompleted)
	require.Equal(t, "terminal", completed.CurrentState)
}

func TestDealDSLComprehensiveProcess(t *testing.T) {
	processID := newFlowID(t, "deal-dsl-process")
	process := comprehensiveDealProcess(processID)
	requestDealDSLAPI(t, http.MethodPost, "/products/deal-dsl/api/processes", process, http.StatusCreated, nil)
	assertDealDSLProcessList(t, process)

	buyerFullID := newFlowID(t, "buyer-full")
	buyerRefundID := newFlowID(t, "buyer-refund")
	buyerPendingID := newFlowID(t, "buyer-pending")
	full := startDealDSLExecution(t, processID, buyerFullID)
	refund := startDealDSLExecution(t, processID, buyerRefundID)
	pending := startDealDSLExecution(t, processID, buyerPendingID)

	assertDealDSLPostgresStoresOnlyProcess(t, process)
	assertProcessDefinitionSnapshot(t, full.FlowID, process)
	updateStoredProcessDefinition(t, processID)

	waitForDealDSLExecution(t, full.FlowID, currentState("buyer-negotiation"))
	sendDealDSLMessage(t, full.FlowID, "buyer-proposal", map[string]string{
		"proposedItemSamplePrice":  "10",
		"proposedItemPrice":        "100",
		"proposedItemSampleRefund": "5",
	})
	waitForDealDSLExecution(t, full.FlowID, pendingCondition("seller-price-response"))
	sendDealDSLMessage(t, full.FlowID, "seller-price-response", map[string]string{
		"acceptedProposedPrice": "false",
	})
	waitForDealDSLExecution(t, full.FlowID, currentState("buyer-negotiation"))
	sendDealDSLMessage(t, full.FlowID, "buyer-proposal", map[string]string{
		"proposedItemSamplePrice":  "11",
		"proposedItemPrice":        "105",
		"proposedItemSampleRefund": "5",
	})
	waitForDealDSLExecution(t, full.FlowID, pendingCondition("seller-price-response"))
	sendDealDSLMessage(t, full.FlowID, "seller-price-response", map[string]string{
		"acceptedProposedPrice": "true",
	})
	waitForDealDSLExecution(t, full.FlowID, pendingCondition("item-sample-feedback"))
	sendDealDSLMessage(t, full.FlowID, "item-sample-feedback", map[string]string{
		"proceedWithItem": "true",
	})
	fullExecution := waitForDealDSLExecution(t, full.FlowID, executionCompleted)
	require.Equal(t, "process-item-order", fullExecution.CurrentState)
	require.Equal(t, process, fullExecution.ProcessDefinition)
	require.Equal(t, "delivered", fullExecution.StateData["itemDeliveryStatus"])
	require.Equal(t, dealdsl.DeliverItemToBuyer, fullExecution.StateData["lastAction"])

	waitForDealDSLExecution(t, refund.FlowID, currentState("buyer-negotiation"))
	sendDealDSLMessage(t, refund.FlowID, "buyer-proposal", map[string]string{
		"proposedItemSamplePrice":  "12",
		"proposedItemPrice":        "120",
		"proposedItemSampleRefund": "6",
	})
	waitForDealDSLExecution(t, refund.FlowID, pendingCondition("seller-price-response"))
	sendDealDSLMessage(t, refund.FlowID, "seller-price-response", map[string]string{
		"acceptedProposedPrice": "true",
	})
	waitForDealDSLExecution(t, refund.FlowID, pendingCondition("item-sample-feedback"))
	sendDealDSLMessage(t, refund.FlowID, "item-sample-feedback", map[string]string{
		"proceedWithItem": "false",
	})
	refundExecution := waitForDealDSLExecution(t, refund.FlowID, executionCompleted)
	require.Equal(t, "process-refund", refundExecution.CurrentState)
	require.Equal(t, process, refundExecution.ProcessDefinition)
	require.Equal(t, dealdsl.TransferMoneyFromSellerToBuyer, refundExecution.StateData["lastAction"])

	pendingExecution := waitForDealDSLExecution(t, pending.FlowID, currentState("buyer-negotiation"))
	require.Equal(t, buyerPendingID, pendingExecution.BuyerID)
	require.Equal(t, "RUNNING", pendingExecution.Status)
	require.Empty(t, pendingExecution.PendingPreConditionName)

	var buyerList dealDSLListResponse
	requestDealDSLAPI(
		t,
		http.MethodGet,
		"/products/deal-dsl/api/executions?buyerID="+buyerRefundID,
		nil,
		http.StatusOK,
		&buyerList,
	)
	require.Len(t, buyerList.Executions, 1)
	require.Equal(t, refund.FlowID, buyerList.Executions[0].FlowID)

	var buyerAndProcessList dealDSLListResponse
	requestDealDSLAPI(
		t,
		http.MethodGet,
		"/products/deal-dsl/api/executions?buyerID="+buyerRefundID+"&processID="+processID,
		nil,
		http.StatusOK,
		&buyerAndProcessList,
	)
	require.Len(t, buyerAndProcessList.Executions, 1)
	require.Equal(t, refund.FlowID, buyerAndProcessList.Executions[0].FlowID)

	var processList dealDSLListResponse
	requestDealDSLAPI(
		t,
		http.MethodGet,
		"/products/deal-dsl/api/executions?processID="+processID,
		nil,
		http.StatusOK,
		&processList,
	)
	require.True(t, containsDealDSLExecutions(
		processList.Executions,
		full.FlowID,
		refund.FlowID,
		pending.FlowID,
	))

	var allList dealDSLListResponse
	requestDealDSLAPI(t, http.MethodGet, "/products/deal-dsl/api/executions", nil, http.StatusOK, &allList)
	require.True(t, containsDealDSLExecutions(allList.Executions, full.FlowID, refund.FlowID, pending.FlowID))
	require.True(t, executionsStartedDescending(allList.Executions))
	require.NotNil(t, fullExecution.ClosedAt)
	require.NotNil(t, refundExecution.ClosedAt)

	ctx := integrationContext(t)
	var searchPage dex.SearchFlowsPage
	var searchErr error
	require.Eventually(t, func() bool {
		searchPage, searchErr = integClient.SearchFlows(
			ctx,
			dealdsl.BuyerIDSearchKey+" = '"+buyerFullID+"'",
			100,
			"",
		)
		if searchErr != nil {
			return false
		}
		for _, entry := range searchPage.Flows {
			if entry.FlowID == full.FlowID {
				return true
			}
		}
		return false
	}, 20*time.Second, 200*time.Millisecond, "SearchFlows failed: %v", searchErr)
}

func assertDealDSLProcessList(t *testing.T, process dealdsl.DealProcess) {
	t.Helper()
	var response dealDSLProcessListResponse
	requestDealDSLAPI(
		t,
		http.MethodGet,
		"/products/deal-dsl/api/processes",
		nil,
		http.StatusOK,
		&response,
	)
	require.Contains(t, response.Processes, process)
}

func assertDealDSLPostgresStoresOnlyProcess(
	t *testing.T,
	process dealdsl.DealProcess,
) {
	t.Helper()
	ctx := integrationContext(t)
	var definition []byte
	require.NoError(t, dealDSLDB.QueryRow(
		ctx,
		"SELECT definition FROM deal_dsl_processes WHERE process_id = $1",
		process.ProcessID,
	).Scan(&definition))
	var stored dealdsl.DealProcess
	require.NoError(t, json.Unmarshal(definition, &stored))
	require.Equal(t, process, stored)
	var executionTable *string
	require.NoError(t, dealDSLDB.QueryRow(
		ctx,
		"SELECT to_regclass('public.deal_dsl_executions')",
	).Scan(&executionTable))
	require.Nil(t, executionTable)
}

func assertProcessDefinitionSnapshot(
	t *testing.T,
	flowID string,
	process dealdsl.DealProcess,
) {
	t.Helper()
	var snapshot dealdsl.DealProcess
	found, err := integClient.GetAttribute(
		integrationContext(t),
		flowID,
		dealdsl.ProcessDefinition,
		&snapshot,
	)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, process, snapshot)

	values, err := integClient.GetAttributes(
		integrationContext(t),
		flowID,
		dealdsl.StateData,
		dealdsl.ProcessDefinition,
		dealdsl.ProcessID,
		dealdsl.ItemID,
		dealdsl.BuyerID,
		dealdsl.CurrentState,
		dealdsl.CurrentActionIndexToExecute,
		dealdsl.PendingPreConditionState,
		dealdsl.PendingPreConditionName,
	)
	require.NoError(t, err)
	for _, key := range []string{
		"stateData",
		"processDefinition",
		"processID",
		"itemID",
		"buyerID",
		"currentState",
		"currentActionIndexToExecute",
	} {
		require.Contains(t, values, key)
	}
}

func updateStoredProcessDefinition(t *testing.T, processID string) {
	t.Helper()
	replacement := dealdsl.DealProcess{
		ProcessID:        processID,
		ItemID:           "replacement-item",
		ItemName:         "Replacement item",
		InitialState:     "database-only-state",
		InitialStateData: map[string]string{"database": "changed"},
		States: []dealdsl.StateDefinition{{
			Name: "database-only-state",
		}},
	}
	requestDealDSLAPI(
		t,
		http.MethodPut,
		"/products/deal-dsl/api/processes/"+processID,
		replacement,
		http.StatusOK,
		nil,
	)
	var stored dealdsl.DealProcess
	requestDealDSLAPI(
		t,
		http.MethodGet,
		"/products/deal-dsl/api/processes/"+processID,
		nil,
		http.StatusOK,
		&stored,
	)
	require.Equal(t, replacement, stored)
}

func comprehensiveDealProcess(processID string) dealdsl.DealProcess {
	return dealdsl.DealProcess{
		ProcessID:    processID,
		ItemID:       "item-42",
		ItemName:     "Premium research package",
		InitialState: "buyer-negotiation",
		InitialStateData: map[string]string{
			"acceptedProposedPrice":    "false",
			"proceedWithItem":          "false",
			"proposedItemPrice":        "",
			"proposedItemSamplePrice":  "",
			"proposedItemSampleRefund": "",
		},
		States: []dealdsl.StateDefinition{
			{
				Name: "buyer-negotiation",
				PostCondition: &dealdsl.PostCondition{
					WaitFor:  &dealdsl.ExternalCondition{Name: "buyer-proposal"},
					Decision: dealdsl.DecisionExpression{ElseState: "seller-counteroffer"},
				},
			},
			{
				Name:         "seller-counteroffer",
				PreCondition: &dealdsl.ExternalCondition{Name: "seller-price-response"},
				PostCondition: &dealdsl.PostCondition{Decision: dealdsl.DecisionExpression{
					Key: "acceptedProposedPrice",
					Cases: []dealdsl.EqualCase{
						{Equals: "true", GoToState: "process-item-sample"},
					},
					ElseState: "buyer-negotiation",
				}},
			},
			{
				Name:        "process-item-sample",
				PreActions:  []string{dealdsl.TransferMoneyFromBuyerToSeller},
				PostActions: []string{dealdsl.DeliverItemSampleToBuyer},
				PostCondition: &dealdsl.PostCondition{Decision: dealdsl.DecisionExpression{
					ElseState: "wait-item-sample-feedback",
				}},
			},
			{
				Name:         "wait-item-sample-feedback",
				PreCondition: &dealdsl.ExternalCondition{Name: "item-sample-feedback"},
				PostCondition: &dealdsl.PostCondition{Decision: dealdsl.DecisionExpression{
					Key: "proceedWithItem",
					Cases: []dealdsl.EqualCase{
						{Equals: "true", GoToState: "process-item-order"},
					},
					ElseState: "process-refund",
				}},
			},
			{
				Name:        "process-item-order",
				PreActions:  []string{dealdsl.TransferMoneyFromBuyerToSeller},
				PostActions: []string{dealdsl.DeliverItemToBuyer},
			},
			{
				Name:       "process-refund",
				PreActions: []string{dealdsl.TransferMoneyFromSellerToBuyer},
			},
		},
	}
}

func startDealDSLExecution(
	t *testing.T,
	processID string,
	buyerID string,
) dealDSLStartResponse {
	t.Helper()
	var response dealDSLStartResponse
	requestDealDSLAPI(t, http.MethodPost, "/products/deal-dsl/api/executions", map[string]string{
		"processID": processID,
		"buyerID":   buyerID,
	}, http.StatusCreated, &response)
	require.NotEmpty(t, response.FlowID)
	require.NotEmpty(t, response.RunID)
	return response
}

func sendDealDSLMessage(
	t *testing.T,
	flowID string,
	conditionName string,
	data map[string]string,
) {
	t.Helper()
	requestDealDSLAPI(
		t,
		http.MethodPost,
		fmt.Sprintf("/products/deal-dsl/api/executions/%s/channels/%s", flowID, conditionName),
		map[string]any{"data": data},
		http.StatusAccepted,
		nil,
	)
}

func waitForDealDSLExecution(
	t *testing.T,
	flowID string,
	predicate func(dealdsl.DealExecution) bool,
) dealdsl.DealExecution {
	t.Helper()
	var execution dealdsl.DealExecution
	var responseErr error
	require.Eventually(t, func() bool {
		responseErr = requestDealDSLAPIWithoutAssertions(
			t,
			http.MethodGet,
			"/products/deal-dsl/api/executions/"+flowID,
			nil,
			http.StatusOK,
			&execution,
		)
		return responseErr == nil && predicate(execution)
	}, 30*time.Second, 100*time.Millisecond, "execution %s did not converge: %v", flowID, responseErr)
	return execution
}

func pendingCondition(conditionName string) func(dealdsl.DealExecution) bool {
	return func(execution dealdsl.DealExecution) bool {
		return execution.PendingPreConditionName == conditionName
	}
}

func currentState(stateName string) func(dealdsl.DealExecution) bool {
	return func(execution dealdsl.DealExecution) bool {
		return execution.CurrentState == stateName
	}
}

func executionCompleted(execution dealdsl.DealExecution) bool {
	return execution.Status == "COMPLETED"
}

func executionsStartedDescending(executions []dealdsl.DealExecution) bool {
	for index := 1; index < len(executions); index++ {
		if executions[index].StartedAt.After(executions[index-1].StartedAt) {
			return false
		}
	}
	return true
}

func containsDealDSLExecutions(
	executions []dealdsl.DealExecution,
	flowIDs ...string,
) bool {
	found := make(map[string]bool, len(flowIDs))
	for _, execution := range executions {
		found[execution.FlowID] = true
	}
	for _, flowID := range flowIDs {
		if !found[flowID] {
			return false
		}
	}
	return true
}

func requestDealDSLAPI(
	t *testing.T,
	method string,
	path string,
	input any,
	expectedStatus int,
	output any,
) {
	t.Helper()
	require.NoError(t, requestDealDSLAPIWithoutAssertions(
		t,
		method,
		path,
		input,
		expectedStatus,
		output,
	))
}

func requestDealDSLAPIWithoutAssertions(
	t *testing.T,
	method string,
	path string,
	input any,
	expectedStatus int,
	output any,
) error {
	t.Helper()
	var requestBody io.Reader
	if input != nil {
		payload, err := json.Marshal(input)
		if err != nil {
			return err
		}
		requestBody = bytes.NewReader(payload)
	}
	requestContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(
		requestContext,
		method,
		dealDSLAPIURL+path,
		requestBody,
	)
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		return err
	}
	if response.StatusCode != expectedStatus {
		return fmt.Errorf("%s %s returned %d: %s", method, path, response.StatusCode, payload)
	}
	if output == nil {
		return nil
	}
	if err := json.Unmarshal(payload, output); err != nil {
		return fmt.Errorf("decode %s %s response: %w", method, path, err)
	}
	return nil
}
