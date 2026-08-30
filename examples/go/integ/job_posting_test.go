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
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/examples/go/products/job-post"
	"github.com/superdurable/dex/examples/go/registry"
	"github.com/superdurable/dex/sdk-go/dex"
)

func TestJobPostingUpdateReachesBothJobBoards(t *testing.T) {
	ctx := integrationContext(t)
	flowID := newFlowID(t, "job-posting")
	t.Logf("Job Posting flowID: %s", flowID)
	initialAttributes := jobPostingInitialAttributes(t)
	_, err := integClient.StartFlow(
		ctx,
		registry.JobPosting,
		flowID,
		nil,
		dex.StartFlowOptions{Attributes: initialAttributes},
	)
	require.NoError(t, err)

	updated := jobpost.JobInfo{
		Title:       "Senior Software Engineer",
		Description: "Build durable systems",
		Notes:       "expanded scope",
	}
	var none dex.None
	require.NoError(t, integClient.InvokeRPC(
		ctx,
		flowID,
		registry.JobPosting.Update,
		updated,
		&none,
		dex.InvokeOptions{},
	))
	require.NoError(t, integClient.WaitForStepCompletion(
		ctx,
		flowID,
		dex.StepExecutionID{StepType: jobpost.UpdateLinkedInPostingStepType},
	))
	require.NoError(t, integClient.WaitForStepCompletion(
		ctx,
		flowID,
		dex.StepExecutionID{StepType: jobpost.UpdateIndeedPostingStepType},
	))

	var actual jobpost.JobInfo
	require.NoError(t, integClient.InvokeRPC(
		ctx,
		flowID,
		registry.JobPosting.Get,
		nil,
		&actual,
		dex.InvokeOptions{},
	))
	require.Equal(t, updated, actual)
	require.NoError(t, integClient.StopFlow(ctx, flowID, dex.StopOptions{}))
}

func jobPostingInitialAttributes(t *testing.T) []dex.InitialAttributeDef {
	t.Helper()
	title, err := dex.InitialAttribute(jobpost.Title, "Software Engineer")
	require.NoError(t, err)
	description, err := dex.InitialAttribute(jobpost.JobDescription, "Build reliable systems")
	require.NoError(t, err)
	lastUpdate, err := dex.InitialAttribute(jobpost.LastUpdateTimeMillis, time.Now().UnixMilli())
	require.NoError(t, err)
	return []dex.InitialAttributeDef{title, description, lastUpdate}
}
