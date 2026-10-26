// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package api

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Temporal SQL visibility rejects ORDER BY, so integration tests cannot cover it locally.
func TestBuildFlowSearchQueryKeepsOrderByLast(t *testing.T) {
	testCases := []struct {
		name                  string
		callerQuery           string
		includeContinuedAsNew bool
		wantQuery             string
	}{
		{
			name:                  "empty query",
			includeContinuedAsNew: true,
			wantQuery:             `WorkflowType = "Engine"`,
		},
		{
			name:        "current runs",
			callerQuery: `FlowType = 'A' OR FlowType = 'B'`,
			wantQuery:   `((FlowType = 'A' OR FlowType = 'B') AND (WorkflowType = "Engine")) AND (ExecutionStatus != "ContinuedAsNew")`,
		},
		{
			name:                  "trailing order by",
			callerQuery:           `FlowType = 'A' order  by StartTime DESC`,
			includeContinuedAsNew: true,
			wantQuery:             `(FlowType = 'A') AND (WorkflowType = "Engine") order  by StartTime DESC`,
		},
		{
			name:        "order by only",
			callerQuery: ` ORDER BY StartTime`,
			wantQuery:   `(WorkflowType = "Engine") AND (ExecutionStatus != "ContinuedAsNew") ORDER BY StartTime`,
		},
		{
			name:                  "quoted order by",
			callerQuery:           `Note = "sort ORDER BY \" here" AND MyOrder_By = 1`,
			includeContinuedAsNew: true,
			wantQuery:             `(Note = "sort ORDER BY \" here" AND MyOrder_By = 1) AND (WorkflowType = "Engine")`,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			require.Equal(t, testCase.wantQuery,
				buildFlowSearchQuery(testCase.callerQuery, "Engine", testCase.includeContinuedAsNew))
		})
	}
}
