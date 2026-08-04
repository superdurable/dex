// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

package integ

import (
	"context"
	"github.com/stretchr/testify/assert"
	"strconv"
	"testing"
	"time"
)

func TestTimerWorkflow(t *testing.T) {
	wfId := "TestTimerWorkflow" + strconv.Itoa(int(time.Now().Unix()))
	runId, err := client.StartWorkflow(context.Background(), &timerWorkflow{}, wfId, 10, 5, nil)
	assert.Nil(t, err)
	assert.NotEmpty(t, runId)
	var output int
	startMs := time.Now().UnixMilli()
	err = client.GetSimpleWorkflowResult(context.Background(), wfId, "", &output)
	elapsedMs := time.Now().UnixMilli() - startMs
	assert.Nil(t, err)
	assert.Equal(t, 6, output)
	assert.True(t, elapsedMs >= 4000 && elapsedMs <= 7000)
}
