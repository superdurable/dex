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

func TestInterStateWorkflow(t *testing.T) {
	wfId := "TestInterStateWorkflow" + strconv.Itoa(int(time.Now().Unix()))
	runId, err := client.StartWorkflow(context.Background(), &interStateWorkflow{}, wfId, 10, nil, nil)
	assert.Nil(t, err)
	assert.NotEmpty(t, runId)
	err = client.GetSimpleWorkflowResult(context.Background(), wfId, "", nil)
	assert.Nil(t, err)
}
