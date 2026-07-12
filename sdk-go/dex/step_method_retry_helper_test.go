package dex

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestComputeRetryDelay(t *testing.T) {
	policy := &RetryPolicy{
		InitialInterval:    1 * time.Second,
		BackoffCoefficient: 2.0,
		MaximumInterval:    10 * time.Second,
	}
	assert.Equal(t, 1*time.Second, computeRetryDelay(policy, 1))
	assert.Equal(t, 2*time.Second, computeRetryDelay(policy, 2))
	assert.Equal(t, 4*time.Second, computeRetryDelay(policy, 3))
}

func TestTruncateUTF8Bytes(t *testing.T) {
	long := string(make([]byte, 3000))
	truncated := truncateUTF8Bytes(long, 100)
	assert.LessOrEqual(t, len(truncated), 103)
}

func TestNewRetryStateAfterFailure_TruncatesErrorAndStack(t *testing.T) {
	longErr := strings.Repeat("e", 5000)
	longStack := strings.Repeat("s", 5000)
	state := newRetryStateAfterFailure(nil, fmt.Errorf("%s", longErr), longStack, time.Now().UnixMilli(), 2048)
	require.NotNil(t, state)
	assert.LessOrEqual(t, len(state.LastError), 2048+3)
	assert.LessOrEqual(t, len(state.LastErrorStackTrace), 2048+3)
	assert.NotEmpty(t, state.LastErrorStackTrace)
}

func TestBestDiagnosticFailure_SkipsTimeoutTail(t *testing.T) {
	attempts := []methodAttemptFailure{
		{err: errors.New("root cause"), stack: "stack-a"},
		{err: context.DeadlineExceeded, stack: "stack-b"},
	}
	errMsg, stack := bestDiagnosticFailure(attempts)
	assert.Equal(t, "root cause", errMsg)
	assert.Equal(t, "stack-a", stack)
}

func TestBestDiagnosticFailure_AllTimeoutUsesLast(t *testing.T) {
	attempts := []methodAttemptFailure{
		{err: context.DeadlineExceeded, stack: "stack-a"},
		{err: errors.New("context deadline exceeded"), stack: "stack-b"},
	}
	errMsg, stack := bestDiagnosticFailure(attempts)
	assert.Contains(t, errMsg, "deadline exceeded")
	assert.Equal(t, "stack-b", stack)
}

func TestBuildMethodReportRecovered_NilWhenCleanSuccess(t *testing.T) {
	assert.Nil(t, buildMethodReportIfRecoveredFromFailure(nil, 1, 2048))
}

func TestBuildFailureCompletion_ExecuteAndWaitFor(t *testing.T) {
	failures := []methodAttemptFailure{{err: errors.New("boom"), stack: "stack"}}
	executeTask := &stepInvocationTask{stepExeId: "s1", kind: stepTaskMethodKindExecute}
	executeCompletion := buildFailureCompletion(executeTask, failures, 1, 2048)
	require.NotNil(t, executeCompletion.methodReport)
	assert.False(t, executeCompletion.waitForMethodFailed)
	assert.Equal(t, Fail("boom"), executeCompletion.decision)

	waitTask := &stepInvocationTask{stepExeId: "s2", kind: stepTaskMethodKindWaitFor}
	waitCompletion := buildFailureCompletion(waitTask, failures, 1, 2048)
	require.NotNil(t, waitCompletion.methodReport)
	assert.True(t, waitCompletion.waitForMethodFailed)
	assert.Nil(t, waitCompletion.decision)
}
