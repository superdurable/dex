// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package web

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestFlowDefinitionsRejectMalformedGraphElements(t *testing.T) {
	stepNode := `{"id":"step:a","kind":"step","name":"A"}`
	tests := map[string]struct {
		nodes, edges, diagnostics string
	}{
		"null node":                 {nodes: `[null]`},
		"string node":               {nodes: `["step"]`},
		"node without kind":         {nodes: `[{"id":"a","name":"A"}]`},
		"numeric node name":         {nodes: `[{"id":"a","kind":"step","name":7}]`},
		"null metadata":             {nodes: `[{"id":"a","kind":"step","name":"A","metadata":null}]`},
		"string resource":           {nodes: `[{"id":"a","kind":"attribute","name":"A","resource":"x"}]`},
		"numeric resource map":      {nodes: `[{"id":"a","kind":"attribute","name":"A","resource":{"map":1}}]`},
		"null wait condition":       {nodes: `[{"id":"a","kind":"step","name":"A","wait":{"conditions":[null]}}]`},
		"numeric checked Channel":   {nodes: `[{"id":"a","kind":"step","name":"A","decision":{"checkedChannels":[1]}}]`},
		"null cancellation":         {nodes: `[{"id":"a","kind":"step","name":"A","decision":{"cancellations":[null]}}]`},
		"null edge":                 {nodes: `[` + stepNode + `]`, edges: `[null]`},
		"numeric edge target":       {nodes: `[` + stepNode + `]`, edges: `[{"id":"e","kind":"control","from":"step:a","to":1}]`},
		"null diagnostic":           {diagnostics: `[null]`},
		"diagnostic without code":   {diagnostics: `[{"severity":"error","message":"m"}]`},
		"object diagnostic message": {diagnostics: `[{"severity":"error","code":"c","message":{}}]`},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			graph := validFlowDefinitionV2("HostileFlow", true)
			for field, value := range map[string]string{"nodes": test.nodes, "edges": test.edges, "diagnostics": test.diagnostics} {
				if value != "" {
					graph = strings.Replace(graph, `"`+field+`":[]`, `"`+field+`":`+value, 1)
				}
			}
			definitions := buildFlowDefinitions([]flowDefinitionDocument{{origin: flowDefinitionOriginUploaded, flowType: "HostileFlow", data: []byte(graph)}})
			if !strings.Contains(definitions[0].Error, "is malformed") || definitions[0].Graph != nil {
				t.Fatalf("definition = %+v", definitions[0])
			}
		})
	}
}

func TestFlowDefinitionsBoundGroupsAndV2Lists(t *testing.T) {
	manyGroups := make([]string, 0, maxFlowDefinitionGroups+1)
	for index := 0; index <= maxFlowDefinitionGroups; index++ {
		manyGroups = append(manyGroups, fmt.Sprintf(`{"id":"g%d","label":"G","stepIds":["step:%d"]}`, index, index))
	}
	manySteps := make([]string, 0, maxFlowDefinitionGroupedSteps+1)
	for index := 0; index <= maxFlowDefinitionGroupedSteps; index++ {
		manySteps = append(manySteps, strconv.Quote(fmt.Sprintf("missing:%d", index)))
	}
	repeat := func(item string, count int) string {
		items := make([]string, 0, count)
		for index := 0; index < count; index++ {
			items = append(items, fmt.Sprintf(item, index))
		}
		return strings.Join(items, ",")
	}
	base := validFlowDefinitionV2("BigFlow", true)
	tests := map[string]struct {
		graph     string
		wantError string
	}{
		"groups": {
			graph:     strings.Replace(base, `"groups":[{"id":"control","label":"Control","stepIds":["step:control"]}]`, `"groups":[`+strings.Join(manyGroups, ",")+`]`, 1),
			wantError: "groups; the limit is",
		},
		"grouped Steps": {
			graph:     strings.Replace(base, `"stepIds":["step:control"]`, `"stepIds":[`+strings.Join(manySteps, ",")+`]`, 1),
			wantError: "groups name more than",
		},
		"Actions": {
			graph: strings.Replace(base, `"actions":[]`, `"actions":[`+repeat(
				`{"rpcName":"Act%d","label":"Act","requiredPermission":"a.b","input":{"kind":"none"}}`, maxFlowDefinitionV2Items+1)+`]`, 1),
			wantError: "Actions; the limit is",
		},
		"Display fields": {
			graph: strings.Replace(base, `{"attributeKey":"operator-note","valueType":"string","editable":true,"description":"Note"}`, repeat(
				`{"attributeKey":"note-%d","valueType":"string","editable":false,"description":"Note"}`, maxFlowDefinitionV2Items+1), 1),
			wantError: "Display fields; the limit is",
		},
		"Action input fields": {
			graph: strings.Replace(base, `"actions":[]`, `"actions":[{"rpcName":"Act","label":"Act","requiredPermission":"a.b",`+
				`"input":{"kind":"object","fields":[`+repeat(`{"fieldName":"f%d","valueType":"string","source":"user","required":false,"description":"F"}`, maxFlowDefinitionV2Items+1)+`]}}]`, 1),
			wantError: "input fields in one Action; the limit is",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			definitions := buildFlowDefinitions([]flowDefinitionDocument{{origin: flowDefinitionOriginUploaded, flowType: "BigFlow", data: []byte(test.graph)}})
			if !strings.Contains(definitions[0].Error, test.wantError) {
				t.Fatalf("error = %q, want %q", definitions[0].Error, test.wantError)
			}
		})
	}
}

// A directory file too large to read is invalid and claims no Flow type, so an upload of that Flow type shows.
func TestOversizedDirectoryFileIsInvalidAndClaimsNoFlowType(t *testing.T) {
	directory := t.TempDir()
	padding := `{"id":"pad","kind":"attribute","name":"` + strings.Repeat("p", 1000) + `"},`
	nodes := strings.Repeat(padding, (maxFlowDefinitionBytes/len(padding))+10)
	oversized := strings.Replace(validFlowDefinitionV2("RefundFlow", true), `"nodes":[]`, `"nodes":[`+strings.TrimSuffix(nodes, ",")+`]`, 1)
	if err := os.WriteFile(filepath.Join(directory, "RefundFlow.json"), []byte(oversized), 0o600); err != nil {
		t.Fatal(err)
	}
	service := newFakeDefinitionFlowService(map[string]string{"RefundFlow": validFlowDefinitionV2("RefundFlow", true)})
	sources := newTestFlowDefinitionSources(t, directory, service)

	snapshot, err := sources.loadFlowTypes(context.Background(), []string{"RefundFlow"})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.definitions) != 1 || snapshot.definitions[0].Source != "uploaded" || snapshot.definitions[0].Error != "" {
		t.Fatalf("RefundFlow definitions = %+v", snapshot.definitions)
	}
	if _, found := snapshot.V2Definitions["RefundFlow"]; !found {
		t.Fatal("the uploaded RefundFlow is not served")
	}
	all, err := sources.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	oversizedEntry := definitionByID(t, all, "RefundFlow.json")
	if oversizedEntry.FlowName != "" || !strings.Contains(oversizedEntry.Error, "too large") || oversizedEntry.Graph != nil {
		t.Fatalf("oversized entry = %+v", oversizedEntry)
	}
	list, err := sources.listFlowTypes(context.Background())
	if err != nil || len(list.FlowTypes) != 1 || list.FlowTypes[0] != (flowDefinitionTypeName{FlowType: "RefundFlow", Source: "uploaded"}) {
		t.Fatalf("list = %+v, err = %v", list, err)
	}
}

func TestRepeatedPageTokenIsASourceError(t *testing.T) {
	service := newFakeDefinitionFlowService(map[string]string{
		"Flow0": validFlowDefinitionV2("Flow0", true), "Flow1": validFlowDefinitionV2("Flow1", true), "Flow2": validFlowDefinitionV2("Flow2", true),
	})
	service.pageSize = 1
	service.isRepeatingPageToken = true
	sources := newTestFlowDefinitionSources(t, "", service)

	list, err := sources.listFlowTypes(context.Background())
	if err != nil || len(list.SourceErrors) != 1 || !strings.Contains(list.SourceErrors[0], "repeated page token") {
		t.Fatalf("list = %+v, err = %v", list, err)
	}
	snapshot, err := sources.Load(context.Background())
	if err != nil || !strings.Contains(string(snapshot.metadataResponse), "repeated page token") {
		t.Fatalf("snapshot = %s, err = %v", snapshot.metadataResponse, err)
	}
}

func TestFlowTypeDefinitionRouteAcceptsDotNamesAndHTTPValidators(t *testing.T) {
	service := newFakeDefinitionFlowService(map[string]string{
		".":  strings.Replace(validFlowDefinitionV2("RefundFlow", true), `"name":"RefundFlow"`, `"name":"."`, 1),
		"..": strings.Replace(validFlowDefinitionV2("RefundFlow", true), `"name":"RefundFlow"`, `"name":".."`, 1),
	})
	handler := newDefinitionTestServer(t, "", service)
	get := func(query string, ifNoneMatch string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, "/api/flow-definitions/by-type?"+query, nil)
		if ifNoneMatch != "" {
			request.Header.Set("If-None-Match", ifNoneMatch)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	for _, flowType := range []string{".", ".."} {
		response := get("flowType="+url.QueryEscape(flowType), "")
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"flowType":"`+flowType+`"`) {
			t.Fatalf("%q = %d %s", flowType, response.Code, response.Body.String())
		}
		etag := response.Header().Get("ETag")
		for _, validator := range []string{"W/" + etag, "*", `"other", ` + etag} {
			if cached := get("flowType="+url.QueryEscape(flowType), validator); cached.Code != http.StatusNotModified {
				t.Fatalf("If-None-Match %s = %d", validator, cached.Code)
			}
		}
		if changed := get("flowType="+url.QueryEscape(flowType), `W/"other"`); changed.Code != http.StatusOK {
			t.Fatalf("a different validator = %d", changed.Code)
		}
	}
	for _, query := range []string{"", "flowType=a&flowType=b", "flowType="} {
		if response := get(query, ""); response.Code != http.StatusBadRequest {
			t.Fatalf("query %q = %d %s", query, response.Code, response.Body.String())
		}
	}
}
