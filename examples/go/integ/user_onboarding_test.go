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

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/examples/go/products/signup"
	"github.com/superdurable/dex/examples/go/registry"
	"github.com/superdurable/dex/sdk-go/dex"
)

func TestUserOnboardingCompletesEveryTask(t *testing.T) {
	ctx := integrationContext(t)
	flowID := newFlowID(t, "user-onboarding")
	form := signup.SignupForm{
		Username:  flowID,
		Email:     flowID + "@example.com",
		FirstName: "Test",
		LastName:  "User",
	}

	_, err := integClient.StartFlow(
		ctx,
		registry.UserOnboarding,
		flowID,
		form,
		dex.StartFlowOptions{},
	)
	require.NoError(t, err)
	require.NoError(t, integClient.WaitForAttributeEqual(
		ctx,
		flowID,
		signup.Status,
		signup.StatusWaitingForVerification,
	))

	var output string
	require.NoError(t, integClient.InvokeRPC(
		ctx,
		flowID,
		registry.UserOnboarding.Verify,
		nil,
		&output,
		dex.InvokeOptions{},
	))
	require.Equal(t, "verified", output)
	require.NoError(t, integClient.WaitForAttributeEqual(
		ctx,
		flowID,
		signup.Status,
		signup.StatusWaitingForTask1,
	))

	require.NoError(t, integClient.InvokeRPC(
		ctx,
		flowID,
		registry.UserOnboarding.AccomplishTask1,
		nil,
		&output,
		dex.InvokeOptions{},
	))
	require.Equal(t, "task 1 accomplished", output)
	require.NoError(t, integClient.WaitForAttributeEqual(
		ctx,
		flowID,
		signup.Status,
		signup.StatusWaitingForTask2,
	))

	require.NoError(t, integClient.InvokeRPC(
		ctx,
		flowID,
		registry.UserOnboarding.AccomplishTask2,
		nil,
		&output,
		dex.InvokeOptions{},
	))
	require.Equal(t, "task 2 accomplished", output)

	result := waitForFlow(t, flowID)
	require.Equal(t, dex.FlowCompleted, result.Status)
	var completion string
	require.NoError(t, result.Completions[0].Output.Decode(&completion))
	require.Equal(t, "onboarding completed", completion)
}
