// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package temporal

import (
	"errors"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/superdurable/dex/config"
	"go.temporal.io/api/serviceerror"
)

func TestAlreadyStartedErrorForWorkflow(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockRealTemporalClient := NewMockClient(ctrl)
	mockDataConverter := NewMockDataConverter(ctrl)

	client := NewTemporalClient(mockRealTemporalClient, "test-ns", mockDataConverter, false, &config.RetryPolicy{
		InitialInterval: time.Second,
		MaximumAttempts: 5,
	})

	err := &serviceerror.WorkflowExecutionAlreadyStarted{}
	assert.Equal(t, true, client.IsWorkflowAlreadyStartedError(err))
}

func TestAlreadyStartedErrorForCronWorkflow(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockRealTemporalClient := NewMockClient(ctrl)
	mockDataConverter := NewMockDataConverter(ctrl)

	client := NewTemporalClient(mockRealTemporalClient, "test-ns", mockDataConverter, false, &config.RetryPolicy{
		InitialInterval: time.Second,
		MaximumAttempts: 5,
	})

	err := errors.New("schedule with this ID is already registered")

	assert.Equal(t, true, client.IsWorkflowAlreadyStartedError(err))
}
