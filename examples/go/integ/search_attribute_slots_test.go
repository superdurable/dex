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
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/examples/go/products/engagement"
	"github.com/superdurable/dex/examples/go/registry"
	"github.com/superdurable/dex/sdk-go/dex"
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
		query := fmt.Sprintf("FlowType = '%s' AND keyword1 = '%s' AND WorkflowId IN ('%s', '%s')",
			scenario.flowType, engagement.StatusInitiated, engagementID, clientApisID)
		var searchErr error
		var searchPage dex.SearchFlowsPage
		require.Eventually(t, func() bool {
			searchPage, searchErr = integClient.SearchFlows(ctx, query, 20, "")
			return searchErr == nil && len(searchPage.Flows) == 1 && searchPage.Flows[0].FlowID == scenario.flowID
		}, 20*time.Second, 200*time.Millisecond, "scoped shared-slot search failed for %s", scenario.flowType)
		require.Contains(t, searchPage.Flows[0].IndexedAttributes, "keyword1")
	}
}
