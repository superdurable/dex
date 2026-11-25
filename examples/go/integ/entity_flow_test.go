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
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	entityflow "github.com/superdurable/dex/examples/go/patterns/entity-flow"
	"github.com/superdurable/dex/examples/go/registry"
	"github.com/superdurable/dex/sdk-go/dex"
)

const entityFlowConcurrentDeposits = 8

func TestEntityFlowKeepsOneCustomerAccountUntilRetired(t *testing.T) {
	ctx := integrationContext(t)
	flowID := entityflow.CustomerAccountFlowID(newFlowID(t, "entity-flow"))
	options, err := entityflow.OpenAccountOptions(entityflow.ContactDetails{
		DisplayName: "Ada",
		Email:       "ada@example.com",
	})
	require.NoError(t, err)
	_, err = integClient.StartFlow(ctx, registry.CustomerAccount, flowID, nil, options)
	require.NoError(t, err)
	_, err = integClient.StartFlow(ctx, registry.CustomerAccount, flowID, nil, options)
	var alreadyStarted *dex.FlowAlreadyStartedError
	require.ErrorAs(t, err, &alreadyStarted)

	err = integClient.InvokeRPC(ctx, flowID, registry.CustomerAccount.UpdateContactDetails,
		entityflow.ContactDetails{DisplayName: "Ada Lovelace", Email: "ada@lovelace.example"}, nil)
	require.NoError(t, err)
	err = integClient.InvokeRPC(ctx, flowID, registry.CustomerAccount.UpdateContactDetails,
		entityflow.ContactDetails{DisplayName: "Ada", Email: "not-an-email"}, nil)
	require.ErrorContains(t, err, "email must contain @")

	var deposits sync.WaitGroup
	depositErrors := make(chan error, entityFlowConcurrentDeposits)
	for range entityFlowConcurrentDeposits {
		deposits.Add(1)
		go func() {
			defer deposits.Done()
			depositErrors <- depositCreditsWithRetry(ctx, flowID, 5)
		}()
	}
	deposits.Wait()
	close(depositErrors)
	for depositErr := range depositErrors {
		require.NoError(t, depositErr)
	}
	err = integClient.InvokeRPC(ctx, flowID, registry.CustomerAccount.AdjustCreditBalance,
		entityflow.CreditAdjustment{Amount: -1000}, nil)
	require.ErrorContains(t, err, "below zero")

	var displayName, email string
	var creditBalance int64
	_, err = integClient.GetAttributes(ctx, flowID,
		dex.ReadAttribute(entityflow.DisplayName, &displayName),
		dex.ReadAttribute(entityflow.Email, &email),
		dex.ReadAttribute(entityflow.CreditBalance, &creditBalance),
	)
	require.NoError(t, err)
	require.Equal(t, "Ada Lovelace", displayName)
	require.Equal(t, "ada@lovelace.example", email)
	require.Equal(t, int64(5*entityFlowConcurrentDeposits), creditBalance)

	err = integClient.InvokeRPC(ctx, flowID, registry.CustomerAccount.RetireAccount, nil, nil)
	require.ErrorContains(t, err, "before retiring")
	info, err := integClient.DescribeFlow(ctx, flowID)
	require.NoError(t, err)
	require.Equal(t, dex.FlowRunning, info.Status)

	err = integClient.InvokeRPC(ctx, flowID, registry.CustomerAccount.AdjustCreditBalance,
		entityflow.CreditAdjustment{Amount: -5 * entityFlowConcurrentDeposits}, nil)
	require.NoError(t, err)
	archive := registry.AccountArchive
	archive.MakeUnavailable(flowID)
	err = integClient.InvokeRPC(ctx, flowID, registry.CustomerAccount.RetireAccount, nil, nil)
	require.NoError(t, err)
	requireArchiveAttempts(t, archive, flowID, 1)
	requireEntityFlowRunning(t, ctx, flowID)
	err = integClient.InvokeRPC(ctx, flowID, registry.CustomerAccount.AdjustCreditBalance,
		entityflow.CreditAdjustment{Amount: 10}, nil)
	require.ErrorContains(t, err, "account is retired")
	err = integClient.InvokeRPC(ctx, flowID, registry.CustomerAccount.UpdateContactDetails,
		entityflow.ContactDetails{DisplayName: "Ada Byron", Email: "ada@byron.example"}, nil)
	require.ErrorContains(t, err, "account is retired")
	err = integClient.InvokeRPC(ctx, flowID, registry.CustomerAccount.RetireAccount, nil, nil)
	require.NoError(t, err)
	requireArchiveAttempts(t, archive, flowID, 2)
	requireEntityFlowRunning(t, ctx, flowID)

	archive.LoseNextResponse(flowID)
	archive.MakeAvailable(flowID)
	closeCtx, cancelClose := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancelClose()
	result, err := integClient.WaitForFlow(closeCtx, flowID, dex.WaitForFlowOptions{})
	require.NoError(t, err)
	require.Equal(t, dex.FlowCompleted, result.Status)
	attempts := archive.AttemptStepExecutionIDs(flowID)
	require.GreaterOrEqual(t, len(attempts), 4)
	distinctAttempts := map[string]bool{}
	for _, stepExecutionID := range attempts {
		distinctAttempts[stepExecutionID] = true
	}
	require.Len(t, distinctAttempts, 1)
	require.Equal(t, 1, archive.ArchiveRecordCount(flowID))
	var retired bool
	creditBalance = -1
	_, err = integClient.GetAttributes(ctx, flowID,
		dex.ReadAttribute(entityflow.CreditBalance, &creditBalance),
		dex.ReadAttribute(entityflow.Retired, &retired),
	)
	require.NoError(t, err)
	require.Equal(t, int64(0), creditBalance)
	require.True(t, retired)

	_, err = integClient.StartFlow(ctx, registry.CustomerAccount, flowID, nil, options)
	require.ErrorAs(t, err, &alreadyStarted)
}

func requireArchiveAttempts(
	t *testing.T,
	archive *entityflow.FakeAccountArchive,
	flowID string,
	count int,
) {
	t.Helper()
	require.Eventually(t, func() bool {
		return len(archive.AttemptStepExecutionIDs(flowID)) >= count
	}, 45*time.Second, 50*time.Millisecond)
}

func requireEntityFlowRunning(t *testing.T, ctx context.Context, flowID string) {
	t.Helper()
	info, err := integClient.DescribeFlow(ctx, flowID)
	require.NoError(t, err)
	require.Equal(t, dex.FlowRunning, info.Status)
}

func depositCreditsWithRetry(ctx context.Context, flowID string, amount int64) error {
	for attempt := 1; attempt <= 50; attempt++ {
		err := integClient.InvokeRPC(ctx, flowID, registry.CustomerAccount.AdjustCreditBalance,
			entityflow.CreditAdjustment{Amount: amount}, nil)
		var lockConflict *dex.RPCLockConflictError
		if !errors.As(err, &lockConflict) {
			return err
		}
		time.Sleep(time.Duration(attempt) * 50 * time.Millisecond)
	}
	return errors.New("credit deposit kept conflicting on the balance lock")
}
