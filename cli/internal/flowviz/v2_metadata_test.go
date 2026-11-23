// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package flowviz

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestScriptV2ViewRPCFacts(t *testing.T) {
	const display = `"protocolName":"GetDexDisplay",
		"span":{"startLine":4,"startColumn":1,"endLine":4,"endColumn":100},
		"directives":[{"text":"dex:field attribute-key:state value-type:string editable:false description:State"}]`
	for _, test := range []struct {
		name       string
		viewRPCs   string
		code       string
		messages   []string
		spanLine   int
		fieldCount int
	}{
		{name: "unknown output keys", viewRPCs: `[{` + display + `}]`, fieldCount: 1},
		{name: "matching output keys", viewRPCs: `[{` + display + `,"outputKeys":["state"]}]`, fieldCount: 1},
		{
			name: "known empty output", viewRPCs: `[{` + display + `,"outputKeys":[]}]`,
			code: "v2_view_rpc_output", messages: []string{`GetDexDisplay omits declared field "state"`}, spanLine: 4, fieldCount: 1,
		},
		{
			name: "mismatched output keys", viewRPCs: `[{` + display + `,"outputKeys":["extra"]}]`, code: "v2_view_rpc_output",
			messages: []string{`GetDexDisplay omits declared field "state"`, `GetDexDisplay returns undeclared field "extra"`}, spanLine: 4, fieldCount: 1,
		},
		{
			name: "analyzer error", viewRPCs: `[{` + display + `,"error":"GetDexDisplay must be read-only"}]`,
			code: "v2_view_rpc", messages: []string{"GetDexDisplay must be read-only"}, spanLine: 4, fieldCount: 1,
		},
		{
			name: "unsupported name", viewRPCs: `[{"protocolName":"GetOtherView","span":{"startLine":6}}]`,
			code: "v2_view_rpc", messages: []string{`unexpected view RPC protocol name "GetOtherView"`}, spanLine: 6,
		},
		{
			name: "duplicate name", viewRPCs: `[{` + display + `},{"protocolName":"GetDexDisplay","span":{"startLine":8}}]`,
			code: "v2_view_rpc", messages: []string{`view RPC "GetDexDisplay" is declared more than once`}, spanLine: 8, fieldCount: 1,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			payload := `{"graph":{"valid":true},"attributes":[{"key":"state","valueType":"string"}],"viewRPCs":` + test.viewRPCs + `}`
			var analysis scriptAnalysis
			require.NoError(t, json.Unmarshal([]byte(payload), &analysis))
			graph := analysis.definition()
			require.Len(t, graph.V2.Display.Fields, test.fieldCount)
			require.Equal(t, len(test.messages) == 0, graph.Valid)
			require.Len(t, graph.Diagnostics, len(test.messages))
			for index, diagnostic := range graph.Diagnostics {
				require.Equal(t, "error", diagnostic.Severity)
				require.Equal(t, test.code, diagnostic.Code)
				require.Equal(t, test.messages[index], diagnostic.Message)
				require.NotNil(t, diagnostic.Span)
				require.Equal(t, test.spanLine, diagnostic.Span.StartLine)
			}
		})
	}
}

func TestScriptV2DuplicateAttributeFacts(t *testing.T) {
	const payload = `{
		"graph":{"valid":true},
		"attributes":[
			{"key":"state","valueType":"string","isIndexed":true,"indexKey":"state","indexType":"keyword",
			 "directives":[{"text":"dex:indexed-attribute attribute-key:state index-key:state index-type:keyword value-type:string description:First"}]},
			{"key":"state","valueType":"string","isIndexed":true,"indexKey":"state","indexType":"keyword",
			 "directives":[{"text":"dex:indexed-attribute attribute-key:state index-key:state index-type:keyword value-type:string description:Second"}],
			 "span":{"startLine":9}}
		]
	}`
	var analysis scriptAnalysis
	require.NoError(t, json.Unmarshal([]byte(payload), &analysis))
	graph := analysis.definition()
	require.False(t, graph.Valid)
	require.Equal(t, []IndexedAttribute{{
		AttributeKey: "state", IndexKey: "state", IndexType: "keyword", ValueType: "string", Description: "First",
	}}, graph.V2.IndexedAttributes)
	require.Equal(t, []Diagnostic{{
		Severity: "error", Code: "v2_indexed_attribute", Message: `Attribute "state" is declared more than once`, Span: &Span{StartLine: 9},
	}}, graph.Diagnostics)
}

func TestScriptV2MetadataMatchesGo(t *testing.T) {
	const indexedDirective = `dex:indexed-attribute attribute-key:state index-key:state index-type:keyword value-type:string description:"State"`
	const displayDirective = `dex:field attribute-key:%s value-type:%s editable:false description:"State" ui-slot:title`
	const enumInput = `{"kind":"string","enumValues":[{"name":"Open","value":"open"},{"name":"Closed","value":"closed"}]}`
	for _, test := range []struct {
		name        string
		key         string
		valueType   string
		goInput     string
		scriptInput string
		outputEntry string
		codes       []string
	}{
		{"indexed Attribute and ordered Start enum", "state", "string", "State", enumInput, `"state": nil`, nil},
		{"unknown Attribute", "missing", "string", "State", enumInput, "", []string{"v2_directive"}},
		{"value-type mismatch", "state", "bool", "State", enumInput, "", []string{"v2_directive"}},
		{"unsupported Start input", "state", "string", "chan string", `{"kind":"chan string"}`, `"state": nil`, []string{"v2_start_input"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			field := fmt.Sprintf(displayDirective, test.key, test.valueType)
			payload := fmt.Sprintf(`{
				"graph":{"valid":true,"source":{"language":"typescript"}},
				"attributes":[{"key":"state","valueType":"string","isIndexed":true,"indexKey":"state","indexType":"keyword",
					"directives":[{"text":%q}]}],
				"viewRPCs":[{"protocolName":"GetDexSummary"},{"protocolName":"GetDexDisplay",
					"directives":[{"text":%q,"span":{"startLine":4,"startColumn":1,"endLine":4,"endColumn":100}}]}],
				"startInput":{"stepType":"FinishStep","input":%s}
			}`, indexedDirective, field, test.scriptInput)
			var analysis scriptAnalysis
			require.NoError(t, json.Unmarshal([]byte(payload), &analysis))
			scriptGraph := analysis.definition()

			goSource := fmt.Sprintf(goV2MetadataSource, indexedDirective, field, test.outputEntry, test.goInput, test.goInput)
			goGraph := analyzeGoV2MetadataSource(t, goSource)
			require.Equal(t, []IndexedAttribute{{
				AttributeKey: "state", IndexKey: "state", IndexType: "keyword", ValueType: "string", Description: "State",
			}}, scriptGraph.V2.IndexedAttributes)
			require.Equal(t, goGraph.V2, scriptGraph.V2)
			if test.goInput == "State" {
				require.Equal(t, []StartInputEnumValue{{Name: "Open", Value: "open"}, {Name: "Closed", Value: "closed"}},
					scriptGraph.V2.Start.Input.EnumValues)
			} else {
				require.Nil(t, scriptGraph.V2.Start)
			}

			var goDiagnostics []Diagnostic
			for _, diagnostic := range goGraph.Diagnostics {
				if strings.HasPrefix(diagnostic.Code, "v2_") {
					goDiagnostics = append(goDiagnostics, diagnostic)
				}
			}
			require.Len(t, goDiagnostics, len(test.codes))
			require.Len(t, scriptGraph.Diagnostics, len(test.codes))
			for index, code := range test.codes {
				scriptDiagnostic := scriptGraph.Diagnostics[index]
				require.Equal(t, code, goDiagnostics[index].Code)
				require.Equal(t, code, scriptDiagnostic.Code)
				require.Equal(t, goDiagnostics[index].Severity, scriptDiagnostic.Severity)
				require.Equal(t, goDiagnostics[index].Message, scriptDiagnostic.Message)
				if code == "v2_directive" {
					require.NotNil(t, scriptDiagnostic.Span)
					require.Equal(t, 4, scriptDiagnostic.Span.StartLine)
				}
			}
		})
	}
}

func analyzeGoV2MetadataSource(t *testing.T, source string) *Graph {
	t.Helper()
	directory := t.TempDir()
	sdkDirectory, err := filepath.Abs(filepath.Join("..", "..", "..", "sdk-go"))
	require.NoError(t, err)
	module := "module flowvizmetadata\n\ngo 1.26.0\n\nrequire github.com/superdurable/dex/sdk-go v0.0.0\n\nreplace github.com/superdurable/dex/sdk-go => " + filepath.ToSlash(sdkDirectory) + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(directory, "go.mod"), []byte(module), 0o644))
	sourceFile := filepath.Join(directory, "workflow.go")
	require.NoError(t, os.WriteFile(sourceFile, []byte(source), 0o644))
	graph, err := Analyze(context.Background(), sourceFile, AnalyzeOptions{SchemaVersion: SchemaVersionV2})
	require.NoError(t, err)
	return graph
}

const goV2MetadataSource = `package flowvizmetadata

import "github.com/superdurable/dex/sdk-go/dex"

// %s
var state = dex.DefineAttribute[string]("state", dex.Indexed(dex.AttributeIndex{Type: dex.IndexKeyword}))

type State string

const (
	Open State = "open"
	Closed State = "closed"
)

type MetadataFlow struct { dex.FlowDefaults }

func (*MetadataFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(FinishStep{})}
}

func (flow *MetadataFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{dex.DefineRPC(flow.GetDexSummary, nil), dex.DefineRPC(flow.GetDexDisplay, nil)}
}

func (*MetadataFlow) GetDexSummary(dex.Context, dex.None) (*dex.RPCResult[map[string]any], error) {
	return &dex.RPCResult[map[string]any]{Output: map[string]any{}}, nil
}

// %s
func (*MetadataFlow) GetDexDisplay(dex.Context, dex.None) (*dex.RPCResult[map[string]any], error) {
	return &dex.RPCResult[map[string]any]{Output: map[string]any{%s}}, nil
}

// dex:group group-id:finish group-label:"Finish"
// dex:explanation text:"Finishes the flow."
type FinishStep struct { dex.StepDefaultsNoWaitFor[%s] }

func (FinishStep) Execute(dex.Context, %s) (*dex.StepDecision, error) {
	return dex.ForceComplete(nil), nil
}
`
