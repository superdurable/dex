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
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/examples/go/products/engagement"
	"github.com/superdurable/dex/examples/go/products/job-post"
	"github.com/superdurable/dex/examples/go/registry"
	"github.com/superdurable/dex/sdk-go/dex"
	"github.com/superdurable/dex/sdk-go/dex/ptr"
)

func TestSharedSearchAttributeSlotIsScopedByFlowType(t *testing.T) {
	ctx := integrationContext(t)
	engagementID := newFlowID(t, "shared-slot-engagement")
	clientApisID := newFlowID(t, "shared-slot-client")
	_, err := integClient.StartFlow(ctx, registry.Engagement, engagementID,
		engagement.EngagementInput{EmployerID: "shared-slot-employer", JobSeekerID: "shared-slot-job-seeker"},
		dex.StartFlowOptions{})
	require.NoError(t, err)
	_, err = integClient.StartFlow(ctx, registry.ClientApis, clientApisID,
		string(engagement.StatusInitiated), dex.StartFlowOptions{})
	require.NoError(t, err)

	// Both Flow types write the same value to keyword1 in the same namespace.
	// The FlowType predicate must separate their search results.
	for _, scenario := range []struct{ flowType, flowID string }{
		{"engagement.EngagementFlow", engagementID},
		{"clientapis.ClientApisFlow", clientApisID},
	} {
		query := fmt.Sprintf("FlowType = '%s' AND ExecutionStatus != 'ContinuedAsNew' AND keyword1 = '%s' AND WorkflowId IN ('%s', '%s')",
			scenario.flowType, engagement.StatusInitiated, engagementID, clientApisID)
		var searchErr error
		var searchPage dex.SearchFlowsPage
		require.Eventually(t, func() bool {
			searchPage, searchErr = integClient.SearchFlows(ctx, query, 20, "")
			return searchErr == nil && len(searchPage.Flows) == 1 && searchPage.Flows[0].FlowID == scenario.flowID
		}, 20*time.Second, 200*time.Millisecond, "scoped shared-slot search failed for %s", scenario.flowType)
		require.Contains(t, searchPage.Flows[0].IndexedAttributes, "keyword1")
	}
	callerQuery := fmt.Sprintf("WorkflowId = '%s' OR WorkflowId = '%s'", engagementID, clientApisID)
	for _, scenario := range []struct{ path, flowID string }{
		{"/products/engagement/list", engagementID},
		{"/primitives/client-apis/search", clientApisID},
	} {
		response, err := http.Get(examplesAPIURL + scenario.path + "?query=" + url.QueryEscape(callerQuery))
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, response.StatusCode)
		var result struct {
			Flows   []dex.SearchFlowEntry
			FlowIDs []string `json:"flowIDs"`
		}
		require.NoError(t, json.NewDecoder(response.Body).Decode(&result))
		require.NoError(t, response.Body.Close())
		if scenario.path == "/products/engagement/list" {
			require.Len(t, result.Flows, 1)
			require.Equal(t, scenario.flowID, result.Flows[0].FlowID)
		} else {
			require.Equal(t, []string{scenario.flowID}, result.FlowIDs)
		}
	}
}

func TestApplicationSearchExcludesContinuedAsNewRuns(t *testing.T) {
	ctx := integrationContext(t)
	flowID := newFlowID(t, "search-continued-runs")
	_, err := integClient.StartFlow(ctx, registry.JobPosting, flowID, nil, dex.StartFlowOptions{
		Attributes:     jobPostingInitialAttributes(t),
		ConfigOverride: &dex.FlowConfig{ContinueAsNewThreshold: ptr.Any(int32(5))},
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, integClient.StopFlow(ctx, flowID, dex.StopOptions{})) })
	require.NoError(t, integClient.WaitForStepCompletion(ctx, flowID,
		dex.StepExecutionID{StepType: "Init"}, dex.WaitForStepCompletionOptions{}))

	for updateNumber := 1; updateNumber <= 6; updateNumber++ {
		var version int
		require.NoError(t, integClient.InvokeRPC(ctx, flowID, registry.JobPosting.Update,
			jobpost.JobInfo{Title: "ArchiveSearch Engineer", Description: "Search visibility", Notes: fmt.Sprint(updateNumber)},
			&version))
		for _, stepType := range []string{jobpost.UpdateLinkedInPostingStepType, jobpost.UpdateIndeedPostingStepType} {
			require.NoError(t, integClient.WaitForStepCompletion(ctx, flowID,
				dex.StepExecutionID{StepType: stepType, ExecutionNumber: ptr.Any(int32(version))},
				dex.WaitForStepCompletionOptions{}))
		}
	}

	baseQuery := fmt.Sprintf("FlowType = 'jobpost.JobPostingFlow' AND WorkflowId = '%s' AND text1 = 'ArchiveSearch'", flowID)
	// Intentionally inspect earlier runs to prove this fixture actually continued as new.
	require.Eventually(t, func() bool {
		page, err := integClient.SearchFlows(ctx, baseQuery+" AND ExecutionStatus = 'ContinuedAsNew'", 20, "")
		return err == nil && len(page.Flows) > 0
	}, 20*time.Second, 200*time.Millisecond, "expected at least one prior Continue-as-New run")

	var currentPage dex.SearchFlowsPage
	require.Eventually(t, func() bool {
		var err error
		currentPage, err = integClient.SearchFlows(ctx, baseQuery+" AND ExecutionStatus != 'ContinuedAsNew'", 20, "")
		return err == nil && len(currentPage.Flows) == 1
	}, 20*time.Second, 200*time.Millisecond, "expected only the current run")
	require.Equal(t, flowID, currentPage.Flows[0].FlowID)
	require.NotEqual(t, dex.FlowContinuedAsNew, currentPage.Flows[0].Status)

	response, err := http.Get(examplesAPIURL + "/products/job-post/search?query=" + url.QueryEscape("WorkflowId = '"+flowID+"'"))
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusOK, response.StatusCode)
	var result struct {
		FlowIDs []string `json:"flowIDs"`
	}
	require.NoError(t, json.NewDecoder(response.Body).Decode(&result))
	require.Equal(t, []string{flowID}, result.FlowIDs)
}
