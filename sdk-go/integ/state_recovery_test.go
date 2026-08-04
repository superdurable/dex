// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

package integ

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestStateRecovery(t *testing.T) {
	wfId := "TestStateRecovery" + strconv.Itoa(int(time.Now().Unix()))
	runId, err := client.StartWorkflow(context.Background(), executeApiFailRecoveryWorkflow{}, wfId, 10, nil, nil)
	assert.Nil(t, err)
	assert.NotEmpty(t, runId)

	var output string
	err = client.GetSimpleWorkflowResult(context.Background(), wfId, "", &output)
	assert.Nil(t, err)
	assert.Equal(t, "this is workflow state 2", output)
}
