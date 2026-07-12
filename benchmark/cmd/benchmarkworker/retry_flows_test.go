package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseRetryFinalOutcome(t *testing.T) {
	assert.Equal(t, "succeed", parseRetryFinalOutcome(""))
	assert.Equal(t, "succeed", parseRetryFinalOutcome("succeed"))
	assert.Equal(t, "fail", parseRetryFinalOutcome("fail"))
	assert.Equal(t, "fail", parseRetryFinalOutcome("FAILED"))
}

func TestRetryErrorHelpers(t *testing.T) {
	require.EqualError(t, retryTransientError(2), "transient benchmark failure attempt 2")
	require.EqualError(t, retryPermanentError(5), "permanent failure on attempt 5")
}
