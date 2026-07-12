package dex

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandleStepTaskCompletion_SuppressedSkipsRPC(t *testing.T) {
	task := &stepInvocationTask{stepExeId: "step-1", kind: stepTaskMethodKindExecute}
	re := &runExecutor{
		worker:           &Worker{},
		runningStepTasks: map[string]*stepInvocationTask{"step-1": task},
	}
	completion := stepTaskCompletion{
		task:  task,
		empty: true,
	}
	stop, err := re.handleStepTaskCompletion(completion)
	require.NoError(t, err)
	assert.False(t, stop)
	_, stillRunning := re.runningStepTasks["step-1"]
	assert.False(t, stillRunning)
}

func TestHandleStepTaskCompletion_EmptyClearsRunningTask(t *testing.T) {
	task := &stepInvocationTask{stepExeId: "step-1", kind: stepTaskMethodKindExecute}
	re := &runExecutor{
		worker:           &Worker{},
		runningStepTasks: map[string]*stepInvocationTask{"step-1": task},
	}
	completion := stepTaskCompletion{
		task:  task,
		empty: true,
	}
	stop, err := re.handleStepTaskCompletion(completion)
	require.NoError(t, err)
	assert.False(t, stop)
	assert.Empty(t, re.runningStepTasks)
}
