// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//go:build integration

package web_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type hostedStartHTTPIntegration struct {
	baseURL, workerTarget, targetRevision, actorID, csrfToken, definitionRevision string
	client                                                                        *http.Client
}

func TestHostedStartRealTemplateWorkerAndRecovery(t *testing.T) {
	probe := &hostedStartHTTPIntegration{
		baseURL:      os.Getenv("DEX_PROJECT_CONFIG_TEST_URL"),
		workerTarget: os.Getenv("DEX_HOSTED_START_TEST_WORKER_TARGET"), targetRevision: os.Getenv("DEX_HOSTED_START_TEST_TARGET_REVISION"),
		actorID: "hosted-start-integration-operator", csrfToken: os.Getenv("DEX_PROJECT_CONFIG_TEST_PROJECT_ID"), client: &http.Client{Timeout: 20 * time.Second},
	}
	require.NotEmpty(t, probe.baseURL, "an actual running Dex Server/Web is required")
	require.NotEmpty(t, probe.workerTarget, "the unchanged formal template v1.8.0 Worker must be running")
	require.Len(t, probe.targetRevision, 64)
	flowID, requestID := os.Getenv("DEX_HOSTED_START_TEST_FLOW_ID"), os.Getenv("DEX_HOSTED_START_TEST_REQUEST_ID")
	require.True(t, strings.HasPrefix(flowID, "flow-"), "retain the same owned FlowID when recovering a failed test")
	_, err := uuid.Parse(requestID)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	status, body, err := probe.request(ctx, "GET", "/api/v2/catalog", nil, "")
	require.NoError(t, err)
	require.Equal(t, 200, status)
	var catalog struct {
		DefinitionRevision string `json:"definitionRevision"`
		StartFlow          struct {
			Enabled bool `json:"enabled"`
		} `json:"startFlow"`
		Flows []struct {
			FlowType string `json:"flowType"`
		} `json:"flows"`
	}
	require.NoError(t, json.Unmarshal(body, &catalog))
	require.True(t, catalog.StartFlow.Enabled)
	require.Len(t, catalog.Flows, 1)
	require.Equal(t, "process.BasicProcessFlow", catalog.Flows[0].FlowType)
	probe.definitionRevision = catalog.DefinitionRevision
	input := map[string]any{"flowType": catalog.Flows[0].FlowType, "flowId": flowID, "requestId": requestID, "targetRevision": probe.targetRevision, "input": map[string]string{"title": "Real hosted Start admission"}}
	status, body, err = probe.request(ctx, "POST", "/api/v2/start", input, "")
	require.NoError(t, err)
	require.Equal(t, 200, status, "use dexcli diagnostics and time travel on this same Flow after an application failure")
	var accepted map[string]any
	require.NoError(t, json.Unmarshal(body, &accepted))
	require.Equal(t, flowID, accepted["flowId"])
	_, hasRunID := accepted["runId"]
	require.False(t, hasRunID, "hosted acceptance returns business identity only")
	status, _, err = probe.request(ctx, "POST", "/api/v2/start", input, "")
	require.NoError(t, err)
	require.Equal(t, 200, status)
	status, _, err = probe.request(ctx, "POST", "/api/v2/start/recover", input, "")
	require.NoError(t, err)
	require.Equal(t, 200, status)
	changedRequest := map[string]any{}
	for key, value := range input {
		changedRequest[key] = value
	}
	changedRequest["requestId"] = uuid.NewString()
	status, _, err = probe.request(ctx, "POST", "/api/v2/start/recover", changedRequest, "")
	require.NoError(t, err)
	require.Equal(t, 409, status)
	for _, caseName := range []string{"worker-override", "target-change", "stale-definition", "missing-permission", "invalid-csrf"} {
		body := map[string]any{}
		for key, value := range input {
			body[key] = value
		}
		mode, want := caseName, 403
		switch caseName {
		case "worker-override":
			body["workerTargetAddress"] = "127.0.0.1:1"
			want = 400
		case "target-change":
			body["targetRevision"] = strings.Repeat("0", 64)
			want = 409
		case "stale-definition":
			want = 409
		}
		status, _, err = probe.request(ctx, "POST", "/api/v2/start", body, mode)
		require.NoError(t, err)
		require.Equal(t, want, status, caseName)
	}
	snapshot := probe.waitForWebDisplayState(t, ctx, flowID, "waiting_for_approval")
	action := map[string]any{"flowType": "process.BasicProcessFlow", "flowId": flowID, "rpcName": "ApproveProcess", "input": map[string]any{}, "attributeSnapshot": snapshot}
	for _, mode := range []string{"missing-action-permission", "invalid-csrf", "missing-csrf", "duplicate-csrf", "missing-csrf-context", "duplicate-csrf-context", "stale-definition"} {
		status, _, err = probe.request(ctx, "POST", "/api/v2/actions", action, mode)
		require.NoError(t, err)
		want := 403
		if mode == "stale-definition" {
			want = 409
		}
		if mode == "duplicate-csrf-context" {
			want = 400
		}
		require.Equal(t, want, status, mode)
	}
	edit := map[string]any{"flowType": "process.BasicProcessFlow", "flowId": flowID, "attributeKey": "process-title", "value": "Unexpected browser mutation"}
	status, _, err = probe.request(ctx, "PATCH", "/api/v2/display", edit, "invalid-csrf")
	require.NoError(t, err)
	require.Equal(t, 403, status)
	probe.waitForWebDisplayState(t, ctx, flowID, "waiting_for_approval")
	status, body, err = probe.request(ctx, "POST", "/api/v2/actions", action, "")
	require.NoError(t, err)
	require.Equal(t, 200, status)
	var invoked struct {
		Invoked bool `json:"invoked"`
	}
	require.NoError(t, json.Unmarshal(body, &invoked))
	require.True(t, invoked.Invoked)
	probe.waitForWebDisplayState(t, ctx, flowID, "completed")
	t.Logf("Actual formal template Worker: FlowID=%s duplicate start and same-operation recovery accepted; identity, revision, permission, CSRF and Worker-override rejection verified; application completed", flowID)
}

func (probe *hostedStartHTTPIntegration) request(ctx context.Context, method, route string, body any, mode string) (int, []byte, error) {
	var contents []byte
	var err error
	if body != nil {
		contents, err = json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
	}
	request, err := http.NewRequestWithContext(ctx, method, probe.baseURL+route, bytes.NewReader(contents))
	if err != nil {
		return 0, nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Forwarded-Prefix", "/dex/hosted-start-integration")
	request.Header.Set("X-Forwarded-Proto", "https")
	request.Header.Set("X-Forwarded-Host", "test.dexai.dev")
	request.Header.Set("X-Dex-Web-Embedded", "true")
	request.Header.Set("X-Dex-Web-CSRF-Token", probe.csrfToken)
	request.Header.Set("X-CSRF-Token", probe.csrfToken)
	request.Header.Set("X-Dex-Actor-ID", probe.actorID)
	request.Header.Set("X-Dex-Start-Worker-Target", probe.workerTarget)
	request.Header.Set("X-Dex-Start-Target-Revision", probe.targetRevision)
	request.Header.Set("X-Dex-Work-Queue-Permissions", "flows.start,process.approve")
	if probe.definitionRevision != "" {
		request.Header.Set("X-Dex-Flow-Definition-Revision", probe.definitionRevision)
	}
	switch mode {
	case "stale-definition":
		request.Header.Set("X-Dex-Flow-Definition-Revision", strings.Repeat("0", 64))
	case "missing-permission":
		request.Header.Set("X-Dex-Work-Queue-Permissions", "process.approve")
	case "missing-action-permission":
		request.Header.Set("X-Dex-Work-Queue-Permissions", "flows.start")
	case "invalid-csrf":
		request.Header.Set("X-CSRF-Token", "wrong")
	case "missing-csrf":
		request.Header.Del("X-CSRF-Token")
	case "duplicate-csrf":
		request.Header.Add("X-CSRF-Token", "wrong")
	case "missing-csrf-context":
		request.Header.Del("X-Dex-Web-CSRF-Token")
	case "duplicate-csrf-context":
		request.Header.Add("X-Dex-Web-CSRF-Token", "wrong")
	}
	response, err := probe.client.Do(request)
	if err != nil {
		return 0, nil, err
	}
	contents, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	closeErr := response.Body.Close()
	if readErr != nil {
		return 0, nil, readErr
	}
	if closeErr != nil {
		return 0, nil, closeErr
	}
	return response.StatusCode, contents, nil
}

func (probe *hostedStartHTTPIntegration) waitForWebDisplayState(t *testing.T, ctx context.Context, flowID, wanted string) map[string]any {
	t.Helper()
	query := url.Values{"flowType": {"process.BasicProcessFlow"}, "flowId": {flowID}}
	for ctx.Err() == nil {
		status, contents, err := probe.request(ctx, "GET", "/api/v2/display?"+query.Encode(), nil, "")
		require.NoError(t, err)
		var snapshot struct {
			FlowID            string         `json:"flowId"`
			FlowStatus        string         `json:"flowStatus"`
			IsActive          bool           `json:"isActive"`
			Display           map[string]any `json:"display"`
			AttributeSnapshot map[string]any `json:"attributeSnapshot"`
			EligibleActions   []string       `json:"eligibleActions"`
		}
		if status == 200 && json.Unmarshal(contents, &snapshot) == nil && snapshot.Display["process-state"] == wanted && (wanted != "completed" || !snapshot.IsActive && snapshot.FlowStatus == "Completed") {
			require.Equal(t, flowID, snapshot.FlowID)
			require.Equal(t, "Real hosted Start admission", snapshot.Display["process-title"])
			if wanted == "completed" {
				require.Empty(t, snapshot.EligibleActions)
			} else {
				require.True(t, snapshot.IsActive)
				require.Equal(t, []string{"ApproveProcess"}, snapshot.EligibleActions)
			}
			return snapshot.AttributeSnapshot
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
	t.Fatal(fmt.Sprintf("the same Flow %s did not converge to %s; inspect and recover it instead of creating another", flowID, wanted))
	return nil
}
