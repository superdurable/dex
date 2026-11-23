// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//go:build integration

package command

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/cli/internal/flowviz"
)

func TestVisualizeV2MetadataParity(t *testing.T) {
	root := prepareMetadataParityTest(t)
	expected, err := visualizeMetadataParitySource(t, filepath.Join(root, "workflow.go"))
	require.NoError(t, err)
	require.Empty(t, expected.Diagnostics)
	require.Len(t, expected.V2.IndexedAttributes, 4)
	require.Len(t, expected.V2.Summary.Fields, 7)
	require.Len(t, expected.V2.Display.Fields, 1)
	require.NotNil(t, expected.V2.Start)

	for _, extension := range []string{"py", "ts"} {
		t.Run(extension, func(t *testing.T) {
			actual, err := visualizeMetadataParitySource(t, filepath.Join(root, "workflow."+extension))
			require.NoError(t, err)
			require.Empty(t, actual.Diagnostics)
			require.Equal(t, expected.V2.IndexedAttributes, actual.V2.IndexedAttributes)
			require.Equal(t, expected.V2.Summary, actual.V2.Summary)
			require.Equal(t, expected.V2.Display, actual.V2.Display)
			require.Equal(t, expected.V2.Start, actual.V2.Start)
			if extension == "ts" {
				contents, err := os.ReadFile(filepath.Join(root, "workflow.ts"))
				require.NoError(t, err)
				modified := strings.Replace(string(contents), `indexKey: ""`, "indexKey: readIndexKey()", 1)
				require.NotEqual(t, string(contents), modified)
				source := filepath.Join(t.TempDir(), "workflow.ts")
				require.NoError(t, os.WriteFile(source, []byte(modified), 0o644))
				actual, err := visualizeMetadataParitySource(t, source)
				require.NoError(t, err)
				require.Empty(t, actual.Diagnostics)
				require.Equal(t, expected.V2.IndexedAttributes, actual.V2.IndexedAttributes)
			}
		})
	}
}

func TestVisualizeV2ScriptMetadataDiagnostics(t *testing.T) {
	root := prepareMetadataParityTest(t)
	for _, extension := range []string{"py", "ts"} {
		t.Run(extension, func(t *testing.T) {
			contents, err := os.ReadFile(filepath.Join(root, "workflow."+extension))
			require.NoError(t, err)
			inputAnnotation := "step_input: MetadataInput"
			displayKey := `"state": value`
			unknownKeys := `**{"state": value}, "extra": value`
			displaySignature := "def get_dex_display(self, context: Context"
			summaryRegistration := `name="GetDexSummary"`
			unresolvedLocks := `name="GetDexSummary", lock_attributes=read_locks()`
			displayReturn := "return RPCResult(display_output(STATE.get(context)))"
			attributeWrite := "STATE.set(context, \"new\")\n        " + displayReturn
			channelWrite := "EVENTS.publish(context, \"new\")\n        " + displayReturn
			if extension == "ts" {
				inputAnnotation = "_input: MetadataInput"
				displayKey = "state: state.get"
				unknownKeys = "...{state: state.get(context)}, extra: state.get"
				displaySignature = "public display(context: Context"
				summaryRegistration = `name: "GetDexSummary"`
				unresolvedLocks = `name: "GetDexSummary", lockAttributes: readLocks()`
				displayReturn = "return {output: displayOutput(context)};"
				attributeWrite = "state.set(context, \"new\");\n    " + displayReturn
				channelWrite = "events.publish(context, \"new\");\n    " + displayReturn
			}
			type diagnosticTest struct {
				name         string
				original     string
				replacement  string
				code         string
				severity     string
				message      string
				missingStart bool
			}
			tests := []diagnosticTest{
				{"unknown Attribute", "dex:field attribute-key:label", "dex:field attribute-key:missing", "v2_directive", "error", `field Attribute "missing" must be declared in this file`, false},
				{"value-type mismatch", "attribute-key:label value-type:string", "attribute-key:label value-type:bool", "v2_directive", "error", `value-type "bool" does not match Attribute "label" type "string"`, false},
				{"unresolved Start input", inputAnnotation, strings.Replace(inputAnnotation, "MetadataInput", "ExternalInput", 1), "start_input_unresolved", "warning", "ExternalInput", true},
				{"output mismatch in helper", displayKey, strings.Replace(displayKey, "state", "extra", 1), "v2_view_rpc_output", "error", `GetDexDisplay returns undeclared field "extra"`, false},
				{"unknown output keys", displayKey, unknownKeys, "v2_view_rpc_output", "error", "GetDexDisplay must return its declared fields from a map[string]any literal", false},
				{"view input", displaySignature, displaySignature + ", input: string", "v2_rpc_signature", "error", "RPC GetDexDisplay input must be dex.None", false},
				{"view Attribute write", displayReturn, attributeWrite, "v2_view_rpc_side_effect", "error", "GetDexDisplay must be read-only", false},
				{"view Channel write", displayReturn, channelWrite, "v2_view_rpc_side_effect", "error", "GetDexDisplay must be read-only", false},
				{"view unresolved locks", summaryRegistration, unresolvedLocks, "rpc_lock_unresolved", "error", "GetDexSummary", false},
				{"missing index directive", "dex:indexed-attribute attribute-key:state", "attribute-key:state", "v2_indexed_attribute", "error", `indexed Attribute "state" must declare exactly one dex:indexed-attribute directive`, false},
			}
			if extension == "py" {
				tests = append(tests, diagnosticTest{"dynamic RPC name", summaryRegistration, "name=read_rpc_name()", "dynamic_rpc_declaration", "error", "RPC name must be a string literal or a module-level string constant", false})
			}
			for _, test := range tests {
				t.Run(test.name, func(t *testing.T) {
					modified := strings.Replace(string(contents), test.original, test.replacement, 1)
					require.NotEqual(t, string(contents), modified)
					source := filepath.Join(t.TempDir(), "workflow."+extension)
					require.NoError(t, os.WriteFile(source, []byte(modified), 0o644))
					graph, executeErr := visualizeMetadataParitySource(t, source)
					if test.severity == "error" {
						require.Error(t, executeErr)
						require.False(t, graph.Valid)
					} else {
						require.NoError(t, executeErr)
						require.True(t, graph.Valid)
					}
					require.Equal(t, test.missingStart, graph.V2.Start == nil)
					require.Condition(t, func() bool {
						for _, diagnostic := range graph.Diagnostics {
							if diagnostic.Code == test.code && diagnostic.Severity == test.severity && strings.Contains(diagnostic.Message, test.message) {
								return diagnostic.Span != nil && diagnostic.Span.StartLine > 0 && diagnostic.Span.StartColumn > 0
							}
						}
						return false
					}, "%+v", graph.Diagnostics)
				})
			}
		})
	}
}

func prepareMetadataParityTest(t *testing.T) string {
	t.Helper()
	root := visualizerRepositoryRoot(t)
	t.Setenv("NODE_PATH", filepath.Join(root, "web", "node_modules"))
	return filepath.Join(root, "cli", "internal", "command", "testfixtures", "v2-metadata-parity")
}

func visualizeMetadataParitySource(t *testing.T, source string) (flowviz.Graph, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	app := NewApp(strings.NewReader(""), &stdout, &stderr)
	err := app.Execute(context.Background(), []string{"visualize", source, "--json"})
	var graph flowviz.Graph
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &graph), "%v %s", err, stderr.String())
	require.NotNil(t, graph.V2)
	return graph, err
}
