package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseSagaMethodKind(t *testing.T) {
	assert.Equal(t, "execute", parseSagaMethodKind(""))
	assert.Equal(t, "execute", parseSagaMethodKind("execute"))
	assert.Equal(t, "waitFor", parseSagaMethodKind("waitFor"))
	assert.Equal(t, "waitFor", parseSagaMethodKind("wait_for"))
	assert.Equal(t, "waitFor", parseSagaMethodKind("WAIT"))
}

func TestSagaFlowsHaveHandlerStep(t *testing.T) {
	waitSteps := sagaWaitForBenchmarkFlow{}.GetSteps()
	assert.Len(t, waitSteps, 2)
	execSteps := sagaExecuteBenchmarkFlow{}.GetSteps()
	assert.Len(t, execSteps, 2)
}
