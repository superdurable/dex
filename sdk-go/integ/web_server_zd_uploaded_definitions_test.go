// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//go:build webserverzd

package integ

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/dex"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"github.com/superdurable/dex/sdk-go/integ/webv2approval"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// uploadedOnlyFlowType has an uploaded definition and no directory file.
const uploadedOnlyFlowType = "UploadedOnlyFlow"

type dexWebFlowTypeDefinitions struct {
	DefinitionRevision string `json:"definitionRevision"`
	Definitions        []struct {
		Source     string          `json:"source"`
		SourcePath string          `json:"sourcePath"`
		UploadedAt string          `json:"uploadedAt"`
		Graph      json.RawMessage `json:"graph"`
	} `json:"definitions"`
	V2 *struct {
		DefinitionRevision string `json:"definitionRevision"`
	} `json:"v2"`
}

// A definition uploaded through FlowService reaches a Dex Web that has no Flow rendering directory, and a
// directory file still wins for its Flow type in a Dex Web that has one.
func TestDexWebOnServerZDShowsUploadedDefinitions(t *testing.T) {
	directoryWeb := newDexWebOnServerZDClient(t)
	uploadedAddress := os.Getenv("DEX_WEB_UPLOADED_ADDRESS")
	definitionsDirectory := os.Getenv("DEX_WEB_E2E_DEFINITIONS_DIR")
	require.NotEmpty(t, uploadedAddress, "run with web/scripts/run-server-zd-e2e.sh")
	require.NotEmpty(t, definitionsDirectory, "run with web/scripts/run-server-zd-e2e.sh")
	uploadedWeb := &dexWebV2Client{baseURL: "http://" + uploadedAddress, httpClient: &http.Client{Timeout: 10 * time.Second}}

	approvalFlow := &webv2approval.ApprovalFlow{}
	approvalFlowType := dex.GetFinalFlowType(approvalFlow)
	approvalGraph, err := os.ReadFile(filepath.Join(definitionsDirectory, "webv2approval.json"))
	require.NoError(t, err)
	uploadedApproval := withGraphFields(t, approvalGraph, approvalFlowType, "uploaded/workflow.go")
	uploadedOnly := withGraphFields(t, approvalGraph, uploadedOnlyFlowType, "uploaded/only.go")

	ctx := integrationContext(t)
	uploadResponse, err := newFlowServiceClient(t).UploadFlowDefinitions(ctx, &dexpb.UploadFlowDefinitionsRequest{
		Definitions: []*dexpb.FlowDefinitionUpload{
			{FlowType: approvalFlowType, Content: uploadedApproval, GeneratedAt: timestamppb.Now()},
			{FlowType: uploadedOnlyFlowType, Content: uploadedOnly, GeneratedAt: timestamppb.Now()},
		},
		Uploader: "dex-web-e2e",
		Force:    true,
	})
	require.NoError(t, err)
	require.Len(t, uploadResponse.GetResults(), 2)

	// Without a directory, both Flow types come from the Dex Server.
	var types struct {
		Configured bool `json:"configured"`
		FlowTypes  []struct {
			FlowType string `json:"flowType"`
			Source   string `json:"source"`
		} `json:"flowTypes"`
	}
	status, err := uploadedWeb.getJSON("/api/flow-definition-types", &types)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, status)
	require.True(t, types.Configured)
	sources := make(map[string]string, len(types.FlowTypes))
	for _, name := range types.FlowTypes {
		sources[name.FlowType] = name.Source
	}
	require.Equal(t, "uploaded", sources[approvalFlowType])
	require.Equal(t, "uploaded", sources[uploadedOnlyFlowType])

	uploadedOnlyDefinitions := readDexWebFlowTypeDefinitions(t, uploadedWeb, uploadedOnlyFlowType)
	require.Len(t, uploadedOnlyDefinitions.Definitions, 1)
	require.Equal(t, "uploaded", uploadedOnlyDefinitions.Definitions[0].Source)
	require.NotEmpty(t, uploadedOnlyDefinitions.Definitions[0].Graph)
	require.NotEmpty(t, uploadedOnlyDefinitions.Definitions[0].UploadedAt)

	approvalDefinitions := readDexWebFlowTypeDefinitions(t, uploadedWeb, approvalFlowType)
	require.Equal(t, "uploaded/workflow.go", approvalDefinitions.Definitions[0].SourcePath)
	require.NotNil(t, approvalDefinitions.V2)

	// The run page's Actions work from the uploaded definition alone.
	flowID := newFlowID(t, "web-uploaded-definition")
	_, err = integClient.StartFlow(ctx, approvalFlow, flowID, webv2approval.Input{Requester: flowID}, dex.StartFlowOptions{})
	require.NoError(t, err)
	uploadedWeb.revision = approvalDefinitions.V2.DefinitionRevision
	testFlow := dexWebV2TestFlow{flowType: approvalFlowType, permission: webv2approval.DecidePermission}
	uploadedWeb.approveDecision(t, testFlow, flowID)
	require.Equal(t, dex.FlowCompleted, waitForFlow(t, flowID, false).Status)

	// With a directory, the directory file wins for its Flow type and the upload fills in the rest.
	directoryApproval := readDexWebFlowTypeDefinitions(t, directoryWeb, approvalFlowType)
	require.Len(t, directoryApproval.Definitions, 1)
	require.Equal(t, "directory", directoryApproval.Definitions[0].Source)
	require.NotEqual(t, "uploaded/workflow.go", directoryApproval.Definitions[0].SourcePath)
	directoryUploadedOnly := readDexWebFlowTypeDefinitions(t, directoryWeb, uploadedOnlyFlowType)
	require.Equal(t, "uploaded", directoryUploadedOnly.Definitions[0].Source)
}

func readDexWebFlowTypeDefinitions(t *testing.T, web *dexWebV2Client, flowType string) dexWebFlowTypeDefinitions {
	t.Helper()
	var definitions dexWebFlowTypeDefinitions
	status, err := web.getJSON("/api/flow-definitions/by-type?flowType="+url.QueryEscape(flowType), &definitions)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, status, "definitions of %s", flowType)
	require.NotEmpty(t, definitions.Definitions, "definitions of %s", flowType)
	require.NotEmpty(t, definitions.DefinitionRevision)
	return definitions
}

// withGraphFields renames a graph's Flow type and source path, keeping every other field.
func withGraphFields(t *testing.T, graph []byte, flowType string, sourcePath string) []byte {
	t.Helper()
	var document map[string]any
	require.NoError(t, json.Unmarshal(graph, &document))
	document["flow"].(map[string]any)["name"] = flowType
	document["source"].(map[string]any)["path"] = sourcePath
	encoded, err := json.Marshal(document)
	require.NoError(t, err)
	return encoded
}
