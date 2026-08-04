// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package integ

import (
	"context"
	"fmt"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"github.com/superdurable/dex/sdk-go/dex"
	"github.com/stretchr/testify/assert"
	"strconv"
	"testing"
	"time"
)

func TestPersistenceWorkflow(t *testing.T) {
	wfId := "TestPersistenceWorkflow" + strconv.Itoa(int(time.Now().Unix()))
	input := ExampleDataObjectModel{
		IntValue: time.Now().UnixNano(),
		StrValue: wfId,
		Datetime: time.Now(),
	}
	opt := dex.WorkflowOptions{
		InitialSearchAttributes: map[string]interface{}{
			testSearchAttributeKeyword:  "init-keyword",
			testSearchAttributeText:     "init-text",
			testSearchAttributeBool:     false,
			testSearchAttributeDatetime: time.Now(),
			testSearchAttributeInt:      1,
			testSearchAttributeDouble:   2.1,
		},
	}
	runId, err := client.StartWorkflow(context.Background(), &persistenceWorkflow{}, wfId, 10, input, &opt)
	assert.Nil(t, err)
	assert.NotEmpty(t, runId)
	err = client.GetSimpleWorkflowResult(context.Background(), wfId, "", nil)
	assert.Nil(t, err)
	info, err := client.DescribeWorkflow(context.Background(), wfId, "")
	assert.Nil(t, err)
	assert.Equal(t, dexpb.COMPLETED, info.Status)
	dos, err := client.GetWorkflowDataAttributes(context.Background(), &persistenceWorkflow{}, wfId, "", []string{
		testDataObjectKey,
	})
	assert.Nil(t, err)
	assert.Equal(t, 1, len(dos))
	var do ExampleDataObjectModel
	dos[testDataObjectKey].Get(&do)
	assert.Equal(t, wfId, do.StrValue)

	dos, err = client.GetAllWorkflowDataAttributes(context.Background(), wfId, "")
	assert.Nil(t, err)
	assert.Equal(t, 2, len(dos))
	var str string
	dos[testDataObjectKey2].Get(&str)
	assert.Equal(t, "a string", str)

	sas, err := client.GetWorkflowSearchAttributes(context.Background(), &persistenceWorkflow{}, wfId, "", []string{
		testSearchAttributeKeyword,
		testSearchAttributeText,
		testSearchAttributeBool,
		// testSearchAttributeDatetime, // TODO https://github.com/superdurable/dex/issues/261
		testSearchAttributeInt,
		testSearchAttributeDouble,
	})
	assert.Nil(t, err)
	expectedSas := map[string]interface{}{
		testSearchAttributeKeyword: "Dex",
		testSearchAttributeText:    "Hail Dex!",
		testSearchAttributeBool:    true,
		// testSearchAttributeDatetime: sas[testSearchAttributeDatetime], // // TODO https://github.com/superdurable/dex/issues/261
		testSearchAttributeInt:    int64(1),
		testSearchAttributeDouble: 1.0,
	}
	assert.Equal(t, expectedSas, sas)

	time.Sleep(time.Second * 2) // wait for 2 seconds so that the index is updated
	resp, err := client.SearchWorkflow(context.Background(), dexpb.WorkflowSearchRequest{
		Query:         fmt.Sprintf("DexWorkflowType='%v'", dex.GetFinalWorkflowType(&persistenceWorkflow{})),
		PageSize:      dexpb.PtrInt32(1),
		NextPageToken: nil,
	})
	assert.Nil(t, err, dex.GetOpenApiErrorBody(err))
	assert.True(t, len(resp.WorkflowExecutions) > 0)
}
