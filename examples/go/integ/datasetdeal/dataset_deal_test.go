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

package datasetdeal_test

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
	"github.com/superdurable/dex/examples/go/products/dataset-deal"
	"github.com/superdurable/dex/sdk-go/dex"
)

type datasetDealStartResponse struct {
	FlowID string `json:"flowID"`
	RunID  string `json:"runID"`
}

type datasetDealListResponse struct {
	Executions []datasetdeal.DealExecution `json:"executions"`
}

type datasetDealProcessListResponse struct {
	Processes []datasetdeal.DealProcess `json:"processes"`
}

func TestDatasetDealSmokeStart(t *testing.T) {
	processID := newFlowID(t, "dataset-deal-smoke-process")
	buyerID := newFlowID(t, "dataset-deal-smoke-buyer")
	process := datasetdeal.DealProcess{
		ProcessID:    processID,
		InitialState: "terminal",
		InitialStateData: map[string]string{
			"smoke": "true",
		},
		States: []datasetdeal.StateDefinition{{
			Name: "terminal",
		}},
	}
	requestDatasetDealAPI(t, http.MethodPost, "/products/dataset-deal/api/processes", process, http.StatusCreated, nil)
	execution := startDatasetDealExecution(t, processID, buyerID)
	completed := waitForDatasetDealExecution(t, execution.FlowID, executionCompleted)
	require.Equal(t, "terminal", completed.CurrentState)
}

func TestDatasetDealDSLComprehensiveProcess(t *testing.T) {
	processID := newFlowID(t, "dataset-deal-process")
	process := comprehensiveDealProcess(processID)
	requestDatasetDealAPI(t, http.MethodPost, "/products/dataset-deal/api/processes", process, http.StatusCreated, nil)
	assertDatasetDealProcessList(t, process)

	buyerFullID := newFlowID(t, "buyer-full")
	buyerRefundID := newFlowID(t, "buyer-refund")
	buyerPendingID := newFlowID(t, "buyer-pending")
	full := startDatasetDealExecution(t, processID, buyerFullID)
	refund := startDatasetDealExecution(t, processID, buyerRefundID)
	pending := startDatasetDealExecution(t, processID, buyerPendingID)

	assertDatasetDealPostgresStoresOnlyProcess(t, process)
	assertProcessDefinitionSnapshot(t, full.FlowID, process)
	updateStoredProcessDefinition(t, processID)

	waitForDatasetDealExecution(t, full.FlowID, currentState("buyer-negotiation"))
	sendDatasetDealMessage(t, full.FlowID, "buyer-proposal", map[string]string{
		"proposedSamplePrice":       "10",
		"proposedFullPrice":         "100",
		"proposedSampleRefundPrice": "5",
	})
	waitForDatasetDealExecution(t, full.FlowID, pendingCondition("seller-price-response"))
	sendDatasetDealMessage(t, full.FlowID, "seller-price-response", map[string]string{
		"acceptedProposedPrice": "false",
	})
	waitForDatasetDealExecution(t, full.FlowID, currentState("buyer-negotiation"))
	sendDatasetDealMessage(t, full.FlowID, "buyer-proposal", map[string]string{
		"proposedSamplePrice":       "11",
		"proposedFullPrice":         "105",
		"proposedSampleRefundPrice": "5",
	})
	waitForDatasetDealExecution(t, full.FlowID, pendingCondition("seller-price-response"))
	sendDatasetDealMessage(t, full.FlowID, "seller-price-response", map[string]string{
		"acceptedProposedPrice": "true",
	})
	waitForDatasetDealExecution(t, full.FlowID, pendingCondition("sample-feedback"))
	sendDatasetDealMessage(t, full.FlowID, "sample-feedback", map[string]string{
		"proceedToFullDataset": "true",
	})
	fullExecution := waitForDatasetDealExecution(t, full.FlowID, executionCompleted)
	require.Equal(t, "process-full-order", fullExecution.CurrentState)
	require.Equal(t, process, fullExecution.ProcessDefinition)
	require.Equal(t, "full", fullExecution.StateData["deliveredDataset"])
	require.Equal(t, datasetdeal.TransportFullDatasetToBuyer, fullExecution.StateData["lastAction"])

	waitForDatasetDealExecution(t, refund.FlowID, currentState("buyer-negotiation"))
	sendDatasetDealMessage(t, refund.FlowID, "buyer-proposal", map[string]string{
		"proposedSamplePrice":       "12",
		"proposedFullPrice":         "120",
		"proposedSampleRefundPrice": "6",
	})
	waitForDatasetDealExecution(t, refund.FlowID, pendingCondition("seller-price-response"))
	sendDatasetDealMessage(t, refund.FlowID, "seller-price-response", map[string]string{
		"acceptedProposedPrice": "true",
	})
	waitForDatasetDealExecution(t, refund.FlowID, pendingCondition("sample-feedback"))
	sendDatasetDealMessage(t, refund.FlowID, "sample-feedback", map[string]string{
		"proceedToFullDataset": "false",
	})
	refundExecution := waitForDatasetDealExecution(t, refund.FlowID, executionCompleted)
	require.Equal(t, "process-refund", refundExecution.CurrentState)
	require.Equal(t, process, refundExecution.ProcessDefinition)
	require.Equal(t, datasetdeal.TransferMoneyFromSellerToBuyer, refundExecution.StateData["lastAction"])

	pendingExecution := waitForDatasetDealExecution(t, pending.FlowID, currentState("buyer-negotiation"))
	require.Equal(t, buyerPendingID, pendingExecution.BuyerID)
	require.Equal(t, "RUNNING", pendingExecution.Status)
	require.Empty(t, pendingExecution.PendingPreConditionName)

	var buyerList datasetDealListResponse
	requestDatasetDealAPI(
		t,
		http.MethodGet,
		"/products/dataset-deal/api/executions?buyerID="+buyerRefundID,
		nil,
		http.StatusOK,
		&buyerList,
	)
	require.Len(t, buyerList.Executions, 1)
	require.Equal(t, refund.FlowID, buyerList.Executions[0].FlowID)

	var buyerAndProcessList datasetDealListResponse
	requestDatasetDealAPI(
		t,
		http.MethodGet,
		"/products/dataset-deal/api/executions?buyerID="+buyerRefundID+"&processID="+processID,
		nil,
		http.StatusOK,
		&buyerAndProcessList,
	)
	require.Len(t, buyerAndProcessList.Executions, 1)
	require.Equal(t, refund.FlowID, buyerAndProcessList.Executions[0].FlowID)

	var processList datasetDealListResponse
	requestDatasetDealAPI(
		t,
		http.MethodGet,
		"/products/dataset-deal/api/executions?processID="+processID,
		nil,
		http.StatusOK,
		&processList,
	)
	require.True(t, containsDatasetDealExecutions(
		processList.Executions,
		full.FlowID,
		refund.FlowID,
		pending.FlowID,
	))

	var allList datasetDealListResponse
	requestDatasetDealAPI(t, http.MethodGet, "/products/dataset-deal/api/executions", nil, http.StatusOK, &allList)
	require.True(t, containsDatasetDealExecutions(allList.Executions, full.FlowID, refund.FlowID, pending.FlowID))
	require.True(t, executionsStartedDescending(allList.Executions))
	require.NotNil(t, fullExecution.ClosedAt)
	require.NotNil(t, refundExecution.ClosedAt)

	ctx := integrationContext(t)
	var searchPage dex.SearchFlowsPage
	var searchErr error
	require.Eventually(t, func() bool {
		searchPage, searchErr = integClient.SearchFlows(
			ctx,
			datasetdeal.BuyerIDSearchKey+" = '"+buyerFullID+"'",
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

func assertDatasetDealProcessList(t *testing.T, process datasetdeal.DealProcess) {
	t.Helper()
	var response datasetDealProcessListResponse
	requestDatasetDealAPI(
		t,
		http.MethodGet,
		"/products/dataset-deal/api/processes",
		nil,
		http.StatusOK,
		&response,
	)
	require.Contains(t, response.Processes, process)
}

func assertDatasetDealPostgresStoresOnlyProcess(
	t *testing.T,
	process datasetdeal.DealProcess,
) {
	t.Helper()
	ctx := integrationContext(t)
	var definition []byte
	require.NoError(t, datasetDealDB.QueryRow(
		ctx,
		"SELECT definition FROM dataset_deal_processes WHERE process_id = $1",
		process.ProcessID,
	).Scan(&definition))
	var stored datasetdeal.DealProcess
	require.NoError(t, json.Unmarshal(definition, &stored))
	require.Equal(t, process, stored)
	var executionTable *string
	require.NoError(t, datasetDealDB.QueryRow(
		ctx,
		"SELECT to_regclass('public.dataset_deal_executions')",
	).Scan(&executionTable))
	require.Nil(t, executionTable)
}

func assertProcessDefinitionSnapshot(
	t *testing.T,
	flowID string,
	process datasetdeal.DealProcess,
) {
	t.Helper()
	var snapshot datasetdeal.DealProcess
	found, err := integClient.GetAttribute(
		integrationContext(t),
		flowID,
		datasetdeal.ProcessDefinition,
		&snapshot,
	)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, process, snapshot)

	values, err := integClient.GetAttributes(
		integrationContext(t),
		flowID,
		datasetdeal.StateData,
		datasetdeal.ProcessDefinition,
		datasetdeal.ProcessID,
		datasetdeal.BuyerID,
		datasetdeal.CurrentState,
		datasetdeal.CurrentActionIndexToExecute,
		datasetdeal.PendingPreConditionState,
		datasetdeal.PendingPreConditionName,
	)
	require.NoError(t, err)
	for _, key := range []string{
		"stateData",
		"processDefinition",
		"processID",
		"buyerID",
		"currentState",
		"currentActionIndexToExecute",
	} {
		require.Contains(t, values, key)
	}
}

func updateStoredProcessDefinition(t *testing.T, processID string) {
	t.Helper()
	replacement := datasetdeal.DealProcess{
		ProcessID:        processID,
		InitialState:     "database-only-state",
		InitialStateData: map[string]string{"database": "changed"},
		States: []datasetdeal.StateDefinition{{
			Name: "database-only-state",
		}},
	}
	requestDatasetDealAPI(
		t,
		http.MethodPut,
		"/products/dataset-deal/api/processes/"+processID,
		replacement,
		http.StatusOK,
		nil,
	)
	var stored datasetdeal.DealProcess
	requestDatasetDealAPI(
		t,
		http.MethodGet,
		"/products/dataset-deal/api/processes/"+processID,
		nil,
		http.StatusOK,
		&stored,
	)
	require.Equal(t, replacement, stored)
}

func comprehensiveDealProcess(processID string) datasetdeal.DealProcess {
	return datasetdeal.DealProcess{
		ProcessID:    processID,
		InitialState: "buyer-negotiation",
		InitialStateData: map[string]string{
			"acceptedProposedPrice":     "false",
			"proceedToFullDataset":      "false",
			"proposedFullPrice":         "",
			"proposedSamplePrice":       "",
			"proposedSampleRefundPrice": "",
		},
		States: []datasetdeal.StateDefinition{
			{
				Name: "buyer-negotiation",
				PostCondition: &datasetdeal.PostCondition{
					WaitFor:  &datasetdeal.ExternalCondition{Name: "buyer-proposal"},
					Decision: datasetdeal.DecisionExpression{ElseState: "seller-counteroffer"},
				},
			},
			{
				Name:         "seller-counteroffer",
				PreCondition: &datasetdeal.ExternalCondition{Name: "seller-price-response"},
				PostCondition: &datasetdeal.PostCondition{Decision: datasetdeal.DecisionExpression{
					Key: "acceptedProposedPrice",
					Cases: []datasetdeal.EqualCase{
						{Equals: "true", GoToState: "process-sample-order"},
					},
					ElseState: "buyer-negotiation",
				}},
			},
			{
				Name:        "process-sample-order",
				PreActions:  []string{datasetdeal.TransferMoneyFromBuyerToSeller},
				PostActions: []string{datasetdeal.TransportSampleDatasetToBuyer},
				PostCondition: &datasetdeal.PostCondition{Decision: datasetdeal.DecisionExpression{
					ElseState: "wait-sample-feedback",
				}},
			},
			{
				Name:         "wait-sample-feedback",
				PreCondition: &datasetdeal.ExternalCondition{Name: "sample-feedback"},
				PostCondition: &datasetdeal.PostCondition{Decision: datasetdeal.DecisionExpression{
					Key: "proceedToFullDataset",
					Cases: []datasetdeal.EqualCase{
						{Equals: "true", GoToState: "process-full-order"},
					},
					ElseState: "process-refund",
				}},
			},
			{
				Name:        "process-full-order",
				PreActions:  []string{datasetdeal.TransferMoneyFromBuyerToSeller},
				PostActions: []string{datasetdeal.TransportFullDatasetToBuyer},
			},
			{
				Name:       "process-refund",
				PreActions: []string{datasetdeal.TransferMoneyFromSellerToBuyer},
			},
		},
	}
}

func startDatasetDealExecution(
	t *testing.T,
	processID string,
	buyerID string,
) datasetDealStartResponse {
	t.Helper()
	var response datasetDealStartResponse
	requestDatasetDealAPI(t, http.MethodPost, "/products/dataset-deal/api/executions", map[string]string{
		"processID": processID,
		"buyerID":   buyerID,
	}, http.StatusCreated, &response)
	require.NotEmpty(t, response.FlowID)
	require.NotEmpty(t, response.RunID)
	return response
}

func sendDatasetDealMessage(
	t *testing.T,
	flowID string,
	conditionName string,
	data map[string]string,
) {
	t.Helper()
	requestDatasetDealAPI(
		t,
		http.MethodPost,
		fmt.Sprintf("/products/dataset-deal/api/executions/%s/channels/%s", flowID, conditionName),
		map[string]any{"data": data},
		http.StatusAccepted,
		nil,
	)
}

func waitForDatasetDealExecution(
	t *testing.T,
	flowID string,
	predicate func(datasetdeal.DealExecution) bool,
) datasetdeal.DealExecution {
	t.Helper()
	var execution datasetdeal.DealExecution
	var responseErr error
	require.Eventually(t, func() bool {
		responseErr = requestDatasetDealAPIWithoutAssertions(
			t,
			http.MethodGet,
			"/products/dataset-deal/api/executions/"+flowID,
			nil,
			http.StatusOK,
			&execution,
		)
		return responseErr == nil && predicate(execution)
	}, 30*time.Second, 100*time.Millisecond, "execution %s did not converge: %v", flowID, responseErr)
	return execution
}

func pendingCondition(conditionName string) func(datasetdeal.DealExecution) bool {
	return func(execution datasetdeal.DealExecution) bool {
		return execution.PendingPreConditionName == conditionName
	}
}

func currentState(stateName string) func(datasetdeal.DealExecution) bool {
	return func(execution datasetdeal.DealExecution) bool {
		return execution.CurrentState == stateName
	}
}

func executionCompleted(execution datasetdeal.DealExecution) bool {
	return execution.Status == "COMPLETED"
}

func executionsStartedDescending(executions []datasetdeal.DealExecution) bool {
	for index := 1; index < len(executions); index++ {
		if executions[index].StartedAt.After(executions[index-1].StartedAt) {
			return false
		}
	}
	return true
}

func containsDatasetDealExecutions(
	executions []datasetdeal.DealExecution,
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

func requestDatasetDealAPI(
	t *testing.T,
	method string,
	path string,
	input any,
	expectedStatus int,
	output any,
) {
	t.Helper()
	require.NoError(t, requestDatasetDealAPIWithoutAssertions(
		t,
		method,
		path,
		input,
		expectedStatus,
		output,
	))
}

func requestDatasetDealAPIWithoutAssertions(
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
		datasetDealAPIURL+path,
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
