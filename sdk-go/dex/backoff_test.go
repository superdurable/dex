// Copyright (c) 2025 superdurable
// SPDX-License-Identifier: MIT

package dex

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestCallWithRetry_StopsRetryingBelowMinRemainingBudget(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), retryMinRemainingBudget/2)
	defer cancel()

	calls := 0
	wantErr := status.Error(codes.Unavailable, "transient")
	_, err := callWithRetry(ctx, func(context.Context) (struct{}, error) {
		calls++
		return struct{}{}, wantErr
	})

	assert.Equal(t, 1, calls, "must not retry once ctx has less than retryMinRemainingBudget left")
	assert.Equal(t, wantErr, err, "must return the last error, not ctx.Err()")
}

func TestCallWithRetry_RetriesWithAmpleBudget(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	calls := 0
	result, err := callWithRetry(ctx, func(context.Context) (int, error) {
		calls++
		if calls < 3 {
			return 0, status.Error(codes.Unavailable, "transient")
		}
		return 42, nil
	})

	require.NoError(t, err)
	assert.Equal(t, 3, calls)
	assert.Equal(t, 42, result)
}

func TestCallWithRetry_NonRetryableReturnsImmediately(t *testing.T) {
	calls := 0
	wantErr := status.Error(codes.InvalidArgument, "bad input")
	_, err := callWithRetry(context.Background(), func(context.Context) (struct{}, error) {
		calls++
		return struct{}{}, wantErr
	})

	assert.Equal(t, 1, calls)
	assert.Equal(t, wantErr, err)
}

func TestCallWithRetry_NoDeadlineIgnoresBudgetGuard(t *testing.T) {
	calls := 0
	result, err := callWithRetry(context.Background(), func(context.Context) (int, error) {
		calls++
		if calls < 2 {
			return 0, status.Error(codes.Unavailable, "transient")
		}
		return 7, nil
	})

	require.NoError(t, err)
	assert.Equal(t, 2, calls, "a ctx with no deadline must not trip the remaining-budget guard")
	assert.Equal(t, 7, result)
}
