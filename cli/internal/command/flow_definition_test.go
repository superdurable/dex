// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package command

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// fixedOutcomeFlowService answers every upload item with the outcome mapped from its Flow type.
type fixedOutcomeFlowService struct {
	dexpb.UnimplementedFlowServiceServer
	outcomes map[string]dexpb.FlowDefinitionUploadOutcome
}

func (service *fixedOutcomeFlowService) UploadFlowDefinitions(
	_ context.Context,
	request *dexpb.UploadFlowDefinitionsRequest,
) (*dexpb.UploadFlowDefinitionsResponse, error) {
	results := make([]*dexpb.FlowDefinitionUploadResult, 0, len(request.GetDefinitions()))
	for _, definition := range request.GetDefinitions() {
		results = append(results, &dexpb.FlowDefinitionUploadResult{
			FlowType:          definition.GetFlowType(),
			Outcome:           service.outcomes[definition.GetFlowType()],
			StoredDigest:      "stored-digest",
			StoredGeneratedAt: timestamppb.New(time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)),
		})
	}
	return &dexpb.UploadFlowDefinitionsResponse{Results: results}, nil
}

func TestFlowDefinitionUploadReportsKeptExistingAndUnknownOutcomes(t *testing.T) {
	service := &fixedOutcomeFlowService{outcomes: map[string]dexpb.FlowDefinitionUploadOutcome{
		"KeptFlow":    dexpb.FlowDefinitionUploadOutcome_FLOW_DEFINITION_UPLOAD_OUTCOME_KEPT_EXISTING,
		"UnknownFlow": dexpb.FlowDefinitionUploadOutcome(99),
	}}
	address := startGRPCServer(t, func(server *grpc.Server) { dexpb.RegisterFlowServiceServer(server, service) })
	directory := t.TempDir()
	keptContent := `{"schemaVersion":"2.0","generatedAt":"2026-01-02T03:04:05.678Z","flow":{"name":"KeptFlow"}}`
	require.NoError(t, os.WriteFile(filepath.Join(directory, "kept.json"), []byte(keptContent), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(directory, "unknown.json"),
		[]byte(`{"schemaVersion":"2.0","flow":{"name":"UnknownFlow"}}`), 0o600))

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	app := NewApp(bytes.NewReader(nil), stdout, stderr)
	require.NoError(t, app.Execute(context.Background(), []string{"flow-definition", "upload", directory, "--server", address}))

	var output struct {
		Definitions []map[string]string `json:"definitions"`
	}
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &output), stdout.String())
	require.Len(t, output.Definitions, 2)
	kept := output.Definitions[0]
	keptDigest := sha256.Sum256([]byte(keptContent))
	require.Equal(t, "KeptFlow", kept["flowType"])
	require.Equal(t, "KEPT_EXISTING", kept["outcome"])
	require.Equal(t, hex.EncodeToString(keptDigest[:]), kept["uploadedDigest"])
	require.Equal(t, "2026-01-02T03:04:05.678Z", kept["uploadedGeneratedAt"])
	require.Equal(t, "stored-digest", kept["storedDigest"])
	require.Equal(t, "2027-01-01T00:00:00Z", kept["storedGeneratedAt"])
	require.Equal(t, "UnknownFlow", output.Definitions[1]["flowType"])
	require.Contains(t, stderr.String(), "unknown upload outcome 99")
}
