// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package integ

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/examples/go/registry"
	"github.com/superdurable/dex/sdk-go/dex"
)

func TestAttributeReadReturnsCommittedValuesOfClosedRun(t *testing.T) {
	ctx := integrationContext(t)
	flowID := newFlowID(t, "primitive-attribute-read")
	_, err := integClient.StartFlow(ctx, registry.Attribute, flowID, "read", dex.StartFlowOptions{})
	require.NoError(t, err)
	result := waitForFlow(t, flowID)
	require.Equal(t, dex.FlowCompleted, result.Status)

	statusCode, body := getAttributeRead(t, url.Values{"workflowId": {flowID}})
	require.Equal(t, http.StatusOK, statusCode, body)
	var values map[string]any
	require.NoError(t, json.Unmarshal([]byte(body), &values))
	require.Equal(t, map[string]any{
		"status":          "completed",
		"paymentProgress": "authorized",
		"email":           nil,
	}, values)
}

func TestAttributeReadReturnsNotFoundForMissingFlow(t *testing.T) {
	flowID := newFlowID(t, "primitive-attribute-read-missing")
	statusCode, body := getAttributeRead(t, url.Values{"workflowId": {flowID}})
	require.Equal(t, http.StatusNotFound, statusCode, body)
	var errorBody map[string]string
	require.NoError(t, json.Unmarshal([]byte(body), &errorBody))
	require.Contains(t, errorBody["error"], flowID)
}

func TestAttributeReadRequiresWorkflowID(t *testing.T) {
	statusCode, body := getAttributeRead(t, url.Values{})
	require.Equal(t, http.StatusBadRequest, statusCode, body)
	require.JSONEq(t, `{"error":"workflowId is required"}`, body)
}

func getAttributeRead(t *testing.T, query url.Values) (int, string) {
	t.Helper()
	readURL := examplesAPIURL + "/primitives/attribute/read?" + query.Encode()
	request, err := http.NewRequestWithContext(integrationContext(t), http.MethodGet, readURL, nil)
	require.NoError(t, err)
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, response.Body.Close())
	}()
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	return response.StatusCode, string(body)
}
