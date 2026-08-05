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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/examples/go/workflows/datasetdeal"
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

func TestDatasetDealDSLComprehensiveProcess(t *testing.T) {
	processID := newFlowID(t, "dataset-deal-process")
	process := comprehensiveDealProcess(processID)
	requestDatasetDealAPI(t, http.MethodPost, "/api/dataset-deal/processes", process, http.StatusCreated, nil)
	assertDatasetDealProcessList(t, process)

	buyerFullID := newFlowID(t, "buyer-full")
	buyerRefundID := newFlowID(t, "buyer-refund")
	buyerPendingID := newFlowID(t, "buyer-pending")
	buyerConcurrentID := newFlowID(t, "buyer-concurrent")
	full := startDatasetDealExecution(t, processID, buyerFullID)
	refund := startDatasetDealExecution(t, processID, buyerRefundID)
	pending := startDatasetDealExecution(t, processID, buyerPendingID)
	concurrent := startDatasetDealExecution(t, processID, buyerConcurrentID)

	assertDatasetDealPostgresState(
		t,
		process,
		full.FlowID,
		refund.FlowID,
		pending.FlowID,
		concurrent.FlowID,
	)
	assertProcessDefinitionSnapshot(t, full.FlowID, process)
	updateStoredProcessDefinition(t, processID)

	fullExecution := waitForDatasetDealExecution(t, full.FlowID, pendingCondition("buyer-proposal"))
	require.Equal(t, full.RunID, fullExecution.LatestRunID)
	fullExecution = sendDatasetDealMessage(t, full.FlowID, "buyer-proposal", map[string]string{
		"proposedSamplePrice":       "10",
		"proposedFullPrice":         "100",
		"proposedSampleRefundPrice": "5",
	})
	require.Equal(t, "seller-price-response", fullExecution.PendingConditionName)
	fullExecution = sendDatasetDealMessage(t, full.FlowID, "seller-price-response", map[string]string{
		"acceptedProposedPrice": "false",
	})
	require.Equal(t, "buyer-proposal", fullExecution.PendingConditionName)
	fullExecution = sendDatasetDealMessage(t, full.FlowID, "buyer-proposal", map[string]string{
		"proposedSamplePrice":       "11",
		"proposedFullPrice":         "105",
		"proposedSampleRefundPrice": "5",
	})
	require.Equal(t, "seller-price-response", fullExecution.PendingConditionName)
	fullExecution = sendDatasetDealMessage(t, full.FlowID, "seller-price-response", map[string]string{
		"acceptedProposedPrice": "true",
	})
	require.Equal(t, "sample-feedback", fullExecution.PendingConditionName)
	fullExecution = sendDatasetDealMessage(t, full.FlowID, "sample-feedback", map[string]string{
		"proceedToFullDataset": "true",
	})
	require.Equal(t, datasetdeal.ExecutionCompleted, fullExecution.Status)
	require.Equal(t, "process-full-order", fullExecution.CurrentState)
	require.Equal(t, process, fullExecution.ProcessDefinition)
	require.Equal(t, "full", fullExecution.StateData["deliveredDataset"])
	require.Equal(t, datasetdeal.TransportFullDatasetToBuyer, fullExecution.StateData["lastAction"])
	require.NotEqual(t, full.RunID, fullExecution.LatestRunID)
	require.NotNil(t, fullExecution.CompletedAt)
	assertDatasetDealTriggerRuns(t, full.FlowID, full.RunID, fullExecution.LatestRunID, 6)
	assertActionStepsCommitted(t, full.FlowID)

	waitForDatasetDealExecution(t, refund.FlowID, pendingCondition("buyer-proposal"))
	sendDatasetDealMessage(t, refund.FlowID, "buyer-proposal", map[string]string{
		"proposedSamplePrice":       "12",
		"proposedFullPrice":         "120",
		"proposedSampleRefundPrice": "6",
	})
	sendDatasetDealMessage(t, refund.FlowID, "seller-price-response", map[string]string{
		"acceptedProposedPrice": "true",
	})
	refundExecution := sendDatasetDealMessage(t, refund.FlowID, "sample-feedback", map[string]string{
		"proceedToFullDataset": "false",
	})
	require.Equal(t, datasetdeal.ExecutionCompleted, refundExecution.Status)
	require.Equal(t, "process-refund", refundExecution.CurrentState)
	require.Equal(t, process, refundExecution.ProcessDefinition)
	require.Equal(t, datasetdeal.TransferMoneyFromSellerToBuyer, refundExecution.StateData["lastAction"])
	require.NotNil(t, refundExecution.CompletedAt)

	pendingExecution := waitForDatasetDealExecution(t, pending.FlowID, pendingCondition("buyer-proposal"))
	require.Equal(t, buyerPendingID, pendingExecution.BuyerID)
	require.Equal(t, datasetdeal.ExecutionWaiting, pendingExecution.Status)
	require.Equal(t, datasetdeal.PostConditionPhase, pendingExecution.PendingConditionPhase)

	assertConcurrentDatasetDealTrigger(t, concurrent)
	assertDatasetDealConflicts(t, full.FlowID, pending.FlowID)
	assertDatasetDealLists(t, processID, buyerRefundID, full, refund, pending, concurrent)
}

func assertDatasetDealProcessList(t *testing.T, process datasetdeal.DealProcess) {
	t.Helper()
	var response datasetDealProcessListResponse
	requestDatasetDealAPI(
		t,
		http.MethodGet,
		"/api/dataset-deal/processes",
		nil,
		http.StatusOK,
		&response,
	)
	require.Contains(t, response.Processes, process)
}

func assertDatasetDealPostgresState(
	t *testing.T,
	process datasetdeal.DealProcess,
	flowIDs ...string,
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
	for _, flowID := range flowIDs {
		var count int
		require.NoError(t, datasetDealDB.QueryRow(
			ctx,
			"SELECT COUNT(*) FROM dataset_deal_executions WHERE flow_id = $1",
			flowID,
		).Scan(&count))
		require.Equal(t, 1, count)
	}
	for _, indexName := range []string{
		"dataset_deal_executions_process_idx",
		"dataset_deal_executions_buyer_process_idx",
		"dataset_deal_executions_status_idx",
		"dataset_deal_executions_current_state_idx",
		"dataset_deal_executions_pending_condition_idx",
	} {
		var index *string
		require.NoError(t, datasetDealDB.QueryRow(
			ctx,
			"SELECT to_regclass($1)",
			"public."+indexName,
		).Scan(&index))
		require.NotNil(t, index)
	}
}

func assertProcessDefinitionSnapshot(
	t *testing.T,
	flowID string,
	process datasetdeal.DealProcess,
) {
	t.Helper()
	execution := getDatasetDealExecution(t, flowID)
	require.Equal(t, process, execution.ProcessDefinition)
	require.Equal(t, process.InitialStateData, execution.StateData)
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
		"/api/dataset-deal/processes/"+processID,
		replacement,
		http.StatusOK,
		nil,
	)
	var stored datasetdeal.DealProcess
	requestDatasetDealAPI(
		t,
		http.MethodGet,
		"/api/dataset-deal/processes/"+processID,
		nil,
		http.StatusOK,
		&stored,
	)
	require.Equal(t, replacement, stored)
}

func assertDatasetDealTriggerRuns(
	t *testing.T,
	flowID string,
	firstRunID string,
	latestRunID string,
	minimumRuns int,
) {
	t.Helper()
	ctx := integrationContext(t)
	var page dex.SearchFlowsPage
	var searchErr error
	require.Eventually(t, func() bool {
		page, searchErr = integClient.SearchFlows(
			ctx,
			"WorkflowId='"+flowID+"'",
			100,
			"",
		)
		if searchErr != nil || len(page.Flows) < minimumRuns {
			return false
		}
		seen := make(map[string]bool, len(page.Flows))
		for _, entry := range page.Flows {
			seen[entry.RunID] = true
		}
		return seen[firstRunID] && seen[latestRunID]
	}, 20*time.Second, 200*time.Millisecond, "SearchFlows failed: %v", searchErr)
}

func assertActionStepsCommitted(t *testing.T, flowID string) {
	t.Helper()
	var version int64
	var lastStepExecutionID string
	require.NoError(t, datasetDealDB.QueryRow(
		integrationContext(t),
		`SELECT version, last_step_execution_id
		 FROM dataset_deal_executions
		 WHERE flow_id = $1`,
		flowID,
	).Scan(&version, &lastStepExecutionID))
	require.Greater(t, version, int64(10))
	require.NotEmpty(t, lastStepExecutionID)
}

func assertDatasetDealConflicts(t *testing.T, completedFlowID string, waitingFlowID string) {
	t.Helper()
	requestDatasetDealAPI(
		t,
		http.MethodPost,
		fmt.Sprintf("/api/dataset-deal/executions/%s/conditions/sample-feedback", completedFlowID),
		map[string]any{"data": map[string]string{}},
		http.StatusConflict,
		nil,
	)
	requestDatasetDealAPI(
		t,
		http.MethodPost,
		fmt.Sprintf("/api/dataset-deal/executions/%s/conditions/wrong-condition", waitingFlowID),
		map[string]any{"data": map[string]string{}},
		http.StatusConflict,
		nil,
	)
}

func assertConcurrentDatasetDealTrigger(
	t *testing.T,
	start datasetDealStartResponse,
) {
	t.Helper()
	waitForDatasetDealExecution(t, start.FlowID, pendingCondition("buyer-proposal"))
	ctx := integrationContext(t)
	transaction, err := datasetDealDB.Begin(ctx)
	require.NoError(t, err)
	transactionOpen := true
	t.Cleanup(func() {
		if transactionOpen {
			require.NoError(t, transaction.Rollback(context.Background()))
		}
	})
	var lockedFlowID string
	require.NoError(t, transaction.QueryRow(
		ctx,
		`SELECT flow_id
		 FROM dataset_deal_executions
		 WHERE flow_id = $1
		 FOR UPDATE`,
		start.FlowID,
	).Scan(&lockedFlowID))
	require.Equal(t, start.FlowID, lockedFlowID)

	firstResult := make(chan error, 1)
	go func() {
		firstResult <- requestDatasetDealAPIWithoutAssertions(
			t,
			http.MethodPost,
			fmt.Sprintf(
				"/api/dataset-deal/executions/%s/conditions/buyer-proposal",
				start.FlowID,
			),
			map[string]any{"data": map[string]string{
				"proposedSamplePrice":       "20",
				"proposedFullPrice":         "200",
				"proposedSampleRefundPrice": "10",
			}},
			http.StatusOK,
			new(datasetdeal.DealExecution),
		)
	}()

	var runningRunID string
	var searchErr error
	require.Eventually(t, func() bool {
		var page dex.SearchFlowsPage
		page, searchErr = integClient.SearchFlows(
			ctx,
			"WorkflowId='"+start.FlowID+"'",
			100,
			"",
		)
		if searchErr != nil {
			return false
		}
		for _, entry := range page.Flows {
			if entry.RunID != start.RunID && entry.Status == dex.FlowRunning {
				runningRunID = entry.RunID
				return true
			}
		}
		return false
	}, 20*time.Second, 100*time.Millisecond, "trigger Run did not start: %v", searchErr)
	require.NotEmpty(t, runningRunID)

	requestDatasetDealAPI(
		t,
		http.MethodPost,
		fmt.Sprintf(
			"/api/dataset-deal/executions/%s/conditions/buyer-proposal",
			start.FlowID,
		),
		map[string]any{"data": map[string]string{}},
		http.StatusConflict,
		nil,
	)
	require.NoError(t, transaction.Rollback(ctx))
	transactionOpen = false

	var firstErr error
	require.Eventually(t, func() bool {
		select {
		case firstErr = <-firstResult:
			return true
		default:
			return false
		}
	}, 20*time.Second, 100*time.Millisecond)
	require.NoError(t, firstErr)
	execution := getDatasetDealExecution(t, start.FlowID)
	require.Equal(t, runningRunID, execution.LatestRunID)
	require.Equal(t, datasetdeal.ExecutionWaiting, execution.Status)
	require.Equal(t, "seller-price-response", execution.PendingConditionName)
}

func assertDatasetDealLists(
	t *testing.T,
	processID string,
	buyerRefundID string,
	full datasetDealStartResponse,
	refund datasetDealStartResponse,
	pending datasetDealStartResponse,
	concurrent datasetDealStartResponse,
) {
	t.Helper()
	var buyerList datasetDealListResponse
	requestDatasetDealAPI(
		t,
		http.MethodGet,
		"/api/dataset-deal/executions?buyerID="+buyerRefundID,
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
		"/api/dataset-deal/executions?buyerID="+buyerRefundID+"&processID="+processID,
		nil,
		http.StatusOK,
		&buyerAndProcessList,
	)
	require.Len(t, buyerAndProcessList.Executions, 1)
	require.Equal(t, refund.FlowID, buyerAndProcessList.Executions[0].FlowID)

	var completedList datasetDealListResponse
	requestDatasetDealAPI(
		t,
		http.MethodGet,
		"/api/dataset-deal/executions?processID="+processID+"&status=COMPLETED",
		nil,
		http.StatusOK,
		&completedList,
	)
	require.True(t, containsDatasetDealExecutions(completedList.Executions, full.FlowID, refund.FlowID))

	var waitingList datasetDealListResponse
	requestDatasetDealAPI(
		t,
		http.MethodGet,
		"/api/dataset-deal/executions?status=WAITING&currentState=buyer-negotiation&pendingConditionName=buyer-proposal",
		nil,
		http.StatusOK,
		&waitingList,
	)
	require.True(t, containsDatasetDealExecutions(waitingList.Executions, pending.FlowID))

	var allList datasetDealListResponse
	requestDatasetDealAPI(t, http.MethodGet, "/api/dataset-deal/executions", nil, http.StatusOK, &allList)
	require.True(t, containsDatasetDealExecutions(
		allList.Executions,
		full.FlowID,
		refund.FlowID,
		pending.FlowID,
		concurrent.FlowID,
	))
	require.True(t, executionsCreatedDescending(allList.Executions))

	requestDatasetDealAPI(
		t,
		http.MethodGet,
		"/api/dataset-deal/executions?status=invalid",
		nil,
		http.StatusBadRequest,
		nil,
	)
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
	requestDatasetDealAPI(t, http.MethodPost, "/api/dataset-deal/executions", map[string]string{
		"processID": processID,
		"buyerID":   buyerID,
	}, http.StatusCreated, &response)
	require.NotEmpty(t, response.FlowID)
	require.NotEmpty(t, response.RunID)
	var count int
	require.NoError(t, datasetDealDB.QueryRow(
		integrationContext(t),
		"SELECT COUNT(*) FROM dataset_deal_executions WHERE flow_id = $1",
		response.FlowID,
	).Scan(&count))
	require.Equal(t, 1, count)
	return response
}

func sendDatasetDealMessage(
	t *testing.T,
	flowID string,
	conditionName string,
	data map[string]string,
) datasetdeal.DealExecution {
	t.Helper()
	var execution datasetdeal.DealExecution
	requestDatasetDealAPI(
		t,
		http.MethodPost,
		fmt.Sprintf("/api/dataset-deal/executions/%s/conditions/%s", flowID, conditionName),
		map[string]any{"data": data},
		http.StatusOK,
		&execution,
	)
	return execution
}

func getDatasetDealExecution(t *testing.T, flowID string) datasetdeal.DealExecution {
	t.Helper()
	var execution datasetdeal.DealExecution
	requestDatasetDealAPI(
		t,
		http.MethodGet,
		"/api/dataset-deal/executions/"+flowID,
		nil,
		http.StatusOK,
		&execution,
	)
	return execution
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
			"/api/dataset-deal/executions/"+flowID,
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
		return execution.Status == datasetdeal.ExecutionWaiting &&
			execution.PendingConditionName == conditionName
	}
}

func executionCompleted(execution datasetdeal.DealExecution) bool {
	return execution.Status == datasetdeal.ExecutionCompleted
}

func executionsCreatedDescending(executions []datasetdeal.DealExecution) bool {
	for index := 1; index < len(executions); index++ {
		if executions[index].CreatedAt.After(executions[index-1].CreatedAt) {
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
