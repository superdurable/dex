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
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/iwf/gen/iwfpb"
	"github.com/superdurable/iwf/integ/workflow/persistence"
	"github.com/superdurable/iwf/service"
	"github.com/superdurable/iwf/service/common/timeparser"
	"google.golang.org/protobuf/proto"
)

func TestPersistenceWorkflowTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestPersistenceWorkflow(t, service.BackendTypeTemporal, false, nil)
		smallWaitForFastTest()
	}
}

func TestPersistenceWorkflowTemporalWithEncryption(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestPersistenceWorkflow(t, service.BackendTypeTemporal, true, nil)
		smallWaitForFastTest()
	}
}

func TestPersistenceWorkflowTemporalContinueAsNew(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestPersistenceWorkflow(
			t,
			service.BackendTypeTemporal,
			false,
			minimumContinueAsNewConfig(iwfpb.StepDurability_STEP_DURABILITY_ASYNC),
		)
		smallWaitForFastTest()
	}
}

func TestPersistenceWorkflowTemporalContinueAsNewWithEncryption(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestPersistenceWorkflow(
			t,
			service.BackendTypeTemporal,
			true,
			minimumContinueAsNewConfig(iwfpb.StepDurability_STEP_DURABILITY_ASYNC),
		)
		smallWaitForFastTest()
	}
}

func TestPersistenceWorkflowCadence(t *testing.T) {
	if !*cadenceIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestPersistenceWorkflow(t, service.BackendTypeCadence, false, nil)
		smallWaitForFastTest()
	}
}

func TestPersistenceWorkflowCadenceContinueAsNew(t *testing.T) {
	if !*cadenceIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestPersistenceWorkflow(
			t,
			service.BackendTypeCadence,
			false,
			minimumContinueAsNewConfig(iwfpb.StepDurability_STEP_DURABILITY_SYNC),
		)
		smallWaitForFastTest()
	}
}

func doTestPersistenceWorkflow(
	t *testing.T,
	backendType service.BackendType,
	memoEncryption bool,
	flowConfig *iwfpb.FlowConfig,
) {
	workerHandler := persistence.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startIwfService(t, IwfServiceTestConfig{
		BackendType:    backendType,
		MemoEncryption: memoEncryption,
	})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	flowId := persistence.WorkflowType + uuid.NewString()
	nowTime := time.Now()
	notTimeNanoStr := fmt.Sprintf("%v", nowTime.UnixNano())
	nowTimeStr := nowTime.Format(timeparser.DateTimeFormat)

	expectedDataAttribute := dataObjectAttribute("TestKey", `"TestValue"`)
	expectedDatetimeSearchAttribute := indexedDatetimeAttribute("CustomDatetimeField", nowTimeStr)

	startRequest := &iwfpb.StartFlowRequest{
		FlowId:             flowId,
		FlowType:           persistence.WorkflowType,
		FlowTimeoutSeconds: 20,
		WorkerTarget:       workerTarget,
		StartStepType:      persistence.State1,
		FlowStartOptions: &iwfpb.FlowStartOptions{
			Attributes: []*iwfpb.AttributeWrite{
				expectedDatetimeSearchAttribute,
				expectedDataAttribute,
			},
			FlowConfigOverride: flowConfig,
		},
	}
	_, err := flowClient.StartFlow(ctx, startRequest)
	require.NoError(t, err)

	queryResult, err := getFlowAttributes(ctx, flowClient, flowId, []string{
		persistence.TestDataAttributeKey,
		expectedDataAttribute.GetKey(),
	})

	retryCount := 0
	if flowConfig != nil {
		for err != nil && retryCount < 5 {
			time.Sleep(time.Second)
			retryCount++
			queryResult, err = getFlowAttributes(ctx, flowClient, flowId, []string{
				persistence.TestDataAttributeKey,
				expectedDataAttribute.GetKey(),
			})
		}
	}
	require.NoError(t, err)
	requireAttributePresent(t, queryResult.GetAttributes(), expectedDataAttribute)

	waitResp, err := flowClient.WaitForFlow(ctx, &iwfpb.WaitForFlowRequest{
		FlowId: flowId,
	})
	require.NoError(t, err)
	runId := waitResp.GetRunId()

	queryResult1, err := getFlowAttributes(ctx, flowClient, flowId, []string{
		persistence.TestDataAttributeKey,
		expectedDataAttribute.GetKey(),
	})
	require.NoError(t, err)

	queryResult2, err := flowClient.GetAttributes(ctx, &iwfpb.GetAttributesRequest{
		FlowId: flowId,
		Keys: []string{
			persistence.TestDataAttributeKey,
			persistence.TestDataAttributeKey2,
			expectedDataAttribute.GetKey(),
		},
	})
	require.NoError(t, err)

	searchResult1, err := getFlowAttributes(ctx, flowClient, flowId, []string{
		persistence.TestSearchAttributeKeywordKey,
	})
	require.NoError(t, err)

	searchResult2, err := getFlowAttributes(ctx, flowClient, flowId, []string{
		persistence.TestSearchAttributeIntKey,
	})
	require.NoError(t, err)

	result := workerHandler.GetTestResult()
	history := result.InvokeHistory
	data := result.InvokeData
	require.Equal(t, map[string]int64{
		"S1_waitFor": 1,
		"S1_execute": 1,
		"S2_waitFor": 1,
		"S2_execute": 1,
		"S3_waitFor": 1,
		"S3_execute": 1,
	}, history)

	require.Equal(t, map[string]interface{}{
		"S1_decide_intSaFounds": 1,
		"S1_decide_kwSaFounds":  1,
		"S2_decide_intSaFounds": 1,
		"S2_decide_kwSaFounds":  1,
		"S2_start_intSaFounds":  1,
		"S2_start_kwSaFounds":   1,

		"S1_decide_localAttFound": true,
		"S1_decide_queryAttFound": 2,
		"S2_decide_queryAttFound": true,
		"S2_start_queryAttFound":  true,
	}, data)

	expectedVal1 := dataObjectAttribute(persistence.TestDataAttributeKey, "test-data-attribute-value2")
	expectedVal2 := dataObjectAttribute(persistence.TestDataAttributeKey2, "test-data-attribute-value1")

	requireAttributesMatch(t, []*iwfpb.AttributeWrite{
		expectedVal1,
		expectedDataAttribute,
	}, queryResult1.GetAttributes())
	requireAttributesMatch(t, []*iwfpb.AttributeWrite{
		expectedVal1,
		expectedVal2,
		expectedDataAttribute,
	}, queryResult2.GetAttributes())

	expectedSearchKeyword := indexedKeywordAttribute(
		persistence.TestSearchAttributeKeywordKey,
		persistence.TestSearchAttributeKeywordValue2,
	)
	expectedSearchInt := indexedIntAttribute(
		persistence.TestSearchAttributeIntKey,
		persistence.TestSearchAttributeIntValue2,
	)
	expectedSearchBool := indexedBoolAttribute(persistence.TestSearchAttributeBoolKey, false)

	requireAttributesMatch(t, []*iwfpb.AttributeWrite{expectedSearchKeyword}, searchResult1.GetAttributes())
	requireAttributesMatch(t, []*iwfpb.AttributeWrite{expectedSearchInt}, searchResult2.GetAttributes())

	allIndexed, err := flowClient.GetAttributes(ctx, &iwfpb.GetAttributesRequest{
		FlowId: flowId,
		RunId:  runId,
		Keys: []string{
			"CustomDatetimeField",
			persistence.TestSearchAttributeKeywordKey,
			persistence.TestSearchAttributeIntKey,
			persistence.TestSearchAttributeBoolKey,
		},
	})
	require.NoError(t, err)
	requireAttributesMatch(t, []*iwfpb.AttributeWrite{
		expectedDatetimeSearchAttribute,
		expectedSearchKeyword,
		expectedSearchInt,
		expectedSearchBool,
	}, allIndexed.GetAttributes())

	if *testSearchIntegTest {
		firstFlowId := flowId
		startMore := func(id string, attrs []*iwfpb.AttributeWrite) {
			_, startErr := flowClient.StartFlow(ctx, &iwfpb.StartFlowRequest{
				FlowId:             id,
				FlowType:           persistence.WorkflowType,
				FlowTimeoutSeconds: 20,
				WorkerTarget:       workerTarget,
				StartStepType:      persistence.State1,
				FlowStartOptions: &iwfpb.FlowStartOptions{
					Attributes:         attrs,
					FlowConfigOverride: flowConfig,
				},
			})
			require.NoError(t, startErr)
		}

		startMore(firstFlowId+"-1", []*iwfpb.AttributeWrite{
			indexedBoolAttribute("CustomBoolField", true),
			indexedDatetimeAttribute("CustomDatetimeField", notTimeNanoStr),
		})
		startMore(firstFlowId+"-2", []*iwfpb.AttributeWrite{
			indexedBoolAttribute("CustomBoolField", true),
			indexedDatetimeAttribute("CustomDatetimeField", notTimeNanoStr),
			indexedDoubleAttribute("CustomDoubleField", 0.01),
		})
		attrs3 := []*iwfpb.AttributeWrite{
			indexedBoolAttribute("CustomBoolField", true),
			indexedDatetimeAttribute("CustomDatetimeField", notTimeNanoStr),
			indexedDoubleAttribute("CustomDoubleField", 0.01),
		}
		attrs4 := []*iwfpb.AttributeWrite{
			indexedBoolAttribute("CustomBoolField", true),
			indexedDatetimeAttribute("CustomDatetimeField", notTimeNanoStr),
			indexedDoubleAttribute("CustomDoubleField", 0.01),
		}
		// Cadence has no KeywordList search attribute registration.
		if backendType == service.BackendTypeTemporal {
			keywordArray := indexedKeywordArrayAttribute(
				persistence.TestSearchAttributeKeywordArrayKey,
				"keyword1",
				"keyword2",
			)
			attrs3 = append(attrs3, keywordArray)
			attrs4 = append(attrs4, keywordArray)
		}
		attrs4 = append(attrs4, indexedTextAttribute("CustomStringField", "My name is Quanzheng Long"))
		startMore(firstFlowId+"-3", attrs3)
		startMore(firstFlowId+"-4", attrs4)

		for _, suffix := range []string{"-1", "-2", "-3", "-4"} {
			resp, waitErr := flowClient.WaitForFlow(ctx, &iwfpb.WaitForFlowRequest{
				FlowId: firstFlowId + suffix,
			})
			require.NoError(t, waitErr)
			require.Equal(t, iwfpb.FlowStatus_FLOW_STATUS_COMPLETED, resp.GetFlowStatus())
		}

		time.Sleep(time.Duration(*searchWaitTimeIntegTest) * time.Millisecond)

		boolQuery := fmt.Sprintf("CustomDatetimeField='%v' AND CustomBoolField=%v", nowTimeStr, true)
		if backendType == service.BackendTypeCadence {
			// Cadence ES visibility expects a quoted bool literal.
			boolQuery = fmt.Sprintf("CustomDatetimeField='%v' AND CustomBoolField='%v'", nowTimeStr, "true")
		}

		if flowConfig != nil {
			assertSearchFlows(t, flowClient, fmt.Sprintf("CustomDatetimeField='%v'", nowTimeStr), 15)
			assertSearchFlows(t, flowClient, fmt.Sprintf("CustomDatetimeField='%v' AND CustomStringField='%v'", nowTimeStr, "Quanzheng"), 3)
			assertSearchFlows(t, flowClient, fmt.Sprintf("CustomDatetimeField='%v' AND CustomDoubleField='%v'", nowTimeStr, "0.01"), 9)
			assertSearchFlows(t, flowClient, boolQuery, 0)
		} else {
			assertSearchFlows(t, flowClient, fmt.Sprintf("CustomDatetimeField='%v'", nowTimeStr), 5)
			assertSearchFlows(t, flowClient, fmt.Sprintf("CustomDatetimeField='%v' AND CustomStringField='%v'", nowTimeStr, "Quanzheng"), 1)
			assertSearchFlows(t, flowClient, fmt.Sprintf("CustomDatetimeField='%v' AND CustomDoubleField='%v'", nowTimeStr, "0.01"), 3)
			assertSearchFlows(t, flowClient, boolQuery, 0)
		}
	}
}

func getFlowAttributes(
	ctx context.Context,
	flowClient iwfpb.FlowServiceClient,
	flowId string,
	keys []string,
) (*iwfpb.GetAttributesResponse, error) {
	return flowClient.GetAttributes(ctx, &iwfpb.GetAttributesRequest{
		FlowId: flowId,
		Keys:   keys,
	})
}

func requireAttributePresent(t *testing.T, attributes []*iwfpb.KV, expected *iwfpb.AttributeWrite) {
	t.Helper()
	for _, attribute := range attributes {
		if attribute.GetKey() == expected.GetKey() &&
			proto.Equal(attribute.GetValue(), expected.GetValue()) {
			return
		}
	}
	require.Fail(t, "expected attribute not found", expected.GetKey())
}

func requireAttributesMatch(t *testing.T, expected []*iwfpb.AttributeWrite, actual []*iwfpb.KV) {
	t.Helper()
	require.Len(t, actual, len(expected))
	for _, want := range expected {
		found := false
		for _, got := range actual {
			if got.GetKey() == want.GetKey() && proto.Equal(got.GetValue(), want.GetValue()) {
				found = true
				break
			}
		}
		require.True(t, found, "missing attribute %s", want.GetKey())
	}
}
