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
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// fakeDefinitionFlowService stores uploaded definitions the way a Dex Server does, in memory.
type fakeDefinitionFlowService struct {
	dexpb.FlowServiceClient
	mu sync.Mutex
	// pageSize forces small ListFlowDefinitions pages.
	pageSize int
	stored   map[string][]byte
	// isUnimplemented makes the definition RPCs answer as an older Dex Server.
	isUnimplemented bool
	// failure makes the definition RPCs fail with this error.
	failure error
	// oversized Flow types make any GetFlowDefinitions call that asks for one fail with ResourceExhausted.
	oversized     map[string]bool
	listCalls     int
	readFlowTypes [][]string
}

func newFakeDefinitionFlowService(definitions map[string]string) *fakeDefinitionFlowService {
	service := &fakeDefinitionFlowService{pageSize: 1000, stored: make(map[string][]byte), oversized: make(map[string]bool)}
	for flowType, content := range definitions {
		service.stored[flowType] = []byte(content)
	}
	return service
}

func (s *fakeDefinitionFlowService) ListFlowDefinitions(
	_ context.Context, request *dexpb.ListFlowDefinitionsRequest, _ ...grpc.CallOption,
) (*dexpb.ListFlowDefinitionsResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listCalls++
	if err := s.definitionRPCError(); err != nil {
		return nil, err
	}
	flowTypes := make([]string, 0, len(s.stored))
	for flowType := range s.stored {
		flowTypes = append(flowTypes, flowType)
	}
	sort.Strings(flowTypes)
	start := 0
	if request.GetNextPageToken() != "" {
		parsed, err := strconv.Atoi(request.GetNextPageToken())
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "bad page token")
		}
		start = parsed
	}
	end := min(start+min(s.pageSize, int(request.GetPageSize())), len(flowTypes))
	response := &dexpb.ListFlowDefinitionsResponse{FlowTypes: flowTypes[start:end]}
	if end < len(flowTypes) {
		response.NextPageToken = strconv.Itoa(end)
	}
	return response, nil
}

func (s *fakeDefinitionFlowService) GetFlowDefinitions(
	_ context.Context, request *dexpb.GetFlowDefinitionsRequest, _ ...grpc.CallOption,
) (*dexpb.GetFlowDefinitionsResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.readFlowTypes = append(s.readFlowTypes, slices.Clone(request.GetFlowTypes()))
	if err := s.definitionRPCError(); err != nil {
		return nil, err
	}
	for _, flowType := range request.GetFlowTypes() {
		if strings.Contains(flowType, " ") {
			return nil, status.Error(codes.InvalidArgument, "invalid Flow type")
		}
	}
	response := &dexpb.GetFlowDefinitionsResponse{}
	for _, flowType := range request.GetFlowTypes() {
		if s.oversized[flowType] {
			return nil, status.Error(codes.ResourceExhausted, "response too large")
		}
		content, found := s.stored[flowType]
		if !found {
			continue
		}
		response.Definitions = append(response.Definitions, &dexpb.FlowDefinition{
			Info: &dexpb.FlowDefinitionInfo{
				FlowType: flowType, Digest: sha256Hex(content),
				GeneratedAt: timestamppb.New(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)),
				UploadedAt:  timestamppb.New(time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)),
			},
			Content: content,
		})
	}
	return response, nil
}

func (s *fakeDefinitionFlowService) GetServerInfo(context.Context, *emptypb.Empty, ...grpc.CallOption) (*dexpb.ServerInfo, error) {
	return &dexpb.ServerInfo{ServerEngine: dexpb.ServerEngine_SERVER_ENGINE_ZD}, nil
}

func (s *fakeDefinitionFlowService) SearchFlows(context.Context, *dexpb.SearchFlowsRequest, ...grpc.CallOption) (*dexpb.SearchFlowsResponse, error) {
	return &dexpb.SearchFlowsResponse{}, nil
}

func (s *fakeDefinitionFlowService) definitionRPCError() error {
	if s.isUnimplemented {
		return status.Error(codes.Unimplemented, "not implemented")
	}
	return s.failure
}

func (s *fakeDefinitionFlowService) readRequests() [][]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.readFlowTypes)
}

func newTestFlowDefinitionSources(t *testing.T, directory string, service *fakeDefinitionFlowService) flowDefinitionSources {
	t.Helper()
	sources, err := newFlowDefinitionSources(directory, service)
	if err != nil {
		t.Fatal(err)
	}
	return sources
}

func definitionByID(t *testing.T, snapshot *FlowDefinitionSnapshot, id string) flowDefinition {
	t.Helper()
	for _, definition := range snapshot.definitions {
		if definition.ID == id {
			return definition
		}
	}
	t.Fatalf("no definition %q in %+v", id, snapshot.definitions)
	return flowDefinition{}
}

func TestServerFlowDefinitionsTreatUnimplementedAsNone(t *testing.T) {
	service := newFakeDefinitionFlowService(nil)
	service.isUnimplemented = true
	directory := t.TempDir()
	writeFlowDefinitionTestFile(t, directory, "refund.json", validFlowDefinitionV2("RefundFlow", true))
	sources := newTestFlowDefinitionSources(t, directory, service)

	snapshot, err := sources.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.definitions) != 1 || len(snapshot.V2Definitions) != 1 || strings.Contains(string(snapshot.metadataResponse), "sourceErrors") {
		t.Fatalf("snapshot = %s", snapshot.metadataResponse)
	}
	list, err := sources.listFlowTypes(context.Background())
	if err != nil || len(list.FlowTypes) != 1 || len(list.SourceErrors) != 0 || !list.Configured || !list.OperatorAPI {
		t.Fatalf("list = %+v, err = %v", list, err)
	}
	only, err := sources.loadFlowTypes(context.Background(), []string{"UploadedFlow"})
	if err != nil || len(only.definitions) != 0 {
		t.Fatalf("UploadedFlow = %+v, err = %v", only, err)
	}
}

func TestServerFlowDefinitionsReadEveryListPage(t *testing.T) {
	definitions := map[string]string{}
	for index := 0; index < 5; index++ {
		flowType := fmt.Sprintf("Flow%d", index)
		definitions[flowType] = validFlowDefinitionV2(flowType, true)
	}
	service := newFakeDefinitionFlowService(definitions)
	service.pageSize = 2
	sources := newTestFlowDefinitionSources(t, "", service)

	list, err := sources.listFlowTypes(context.Background())
	if err != nil || len(list.FlowTypes) != 5 || list.FlowTypes[4] != (flowDefinitionTypeName{FlowType: "Flow4", Source: "uploaded"}) {
		t.Fatalf("list = %+v, err = %v", list, err)
	}
	if service.listCalls != 3 {
		t.Fatalf("ListFlowDefinitions calls = %d", service.listCalls)
	}
	if len(service.readRequests()) != 0 {
		t.Fatalf("listing names read content: %v", service.readRequests())
	}
	snapshot, err := sources.Load(context.Background())
	if err != nil || len(snapshot.V2Definitions) != 5 {
		t.Fatalf("snapshot = %+v, err = %v", snapshot, err)
	}
	uploaded := definitionByID(t, snapshot, "uploaded:Flow3")
	if uploaded.Source != "uploaded" || uploaded.GeneratedAt == nil || uploaded.UploadedAt == nil || uploaded.Digest == "" {
		t.Fatalf("uploaded definition = %+v", uploaded)
	}
}

func TestDirectoryFlowDefinitionsWinOverUploadedOnes(t *testing.T) {
	directory := t.TempDir()
	writeFlowDefinitionTestFile(t, directory, "refund.json", validFlowDefinitionV2("RefundFlow", true))
	// An invalid file still claims its Flow type, so the uploaded copy never replaces it.
	writeFlowDefinitionTestFile(t, directory, "broken.json", `{"schemaVersion":"2.0","flow":{"name":"BrokenFlow"}}`)
	uploadedRefund := strings.Replace(validFlowDefinitionV2("RefundFlow", true), `"path":"flow.go"`, `"path":"uploaded.go"`, 1)
	service := newFakeDefinitionFlowService(map[string]string{
		"RefundFlow": uploadedRefund,
		"BrokenFlow": validFlowDefinitionV2("BrokenFlow", true),
		"OtherFlow":  validFlowDefinitionV2("OtherFlow", true),
	})
	sources := newTestFlowDefinitionSources(t, directory, service)

	snapshot, err := sources.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.definitions) != 3 {
		t.Fatalf("definitions = %+v", snapshot.definitions)
	}
	if refund := definitionByID(t, snapshot, "refund.json"); refund.Source != "directory" || refund.SourcePath != "flow.go" {
		t.Fatalf("RefundFlow = %+v", refund)
	}
	if broken := definitionByID(t, snapshot, "broken.json"); broken.FlowName != "BrokenFlow" || broken.Error == "" {
		t.Fatalf("BrokenFlow = %+v", broken)
	}
	if _, found := snapshot.V2Definitions["BrokenFlow"]; found {
		t.Fatal("the uploaded BrokenFlow replaced the invalid directory file")
	}
	if other := definitionByID(t, snapshot, "uploaded:OtherFlow"); other.Source != "uploaded" || other.Error != "" {
		t.Fatalf("OtherFlow = %+v", other)
	}
	for _, request := range service.readRequests() {
		if slices.Contains(request, "RefundFlow") || slices.Contains(request, "BrokenFlow") {
			t.Fatalf("read a Flow type the directory declares: %v", request)
		}
	}

	before := len(service.readRequests())
	refundOnly, err := sources.loadFlowTypes(context.Background(), []string{"RefundFlow"})
	if err != nil || len(refundOnly.definitions) != 1 || refundOnly.definitions[0].Source != "directory" {
		t.Fatalf("RefundFlow only = %+v, err = %v", refundOnly, err)
	}
	if len(service.readRequests()) != before {
		t.Fatal("a directory Flow type read the Dex Server")
	}
	list, err := sources.listFlowTypes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(list.FlowTypes, []flowDefinitionTypeName{
		{FlowType: "BrokenFlow", Source: "directory"}, {FlowType: "OtherFlow", Source: "uploaded"}, {FlowType: "RefundFlow", Source: "directory"},
	}) {
		t.Fatalf("list = %+v", list.FlowTypes)
	}
}

func TestUploadedFlowDefinitionsAreIsolatedAndLimited(t *testing.T) {
	manyNodes := make([]string, 0, maxFlowDefinitionNodes+1)
	for index := 0; index <= maxFlowDefinitionNodes; index++ {
		manyNodes = append(manyNodes, fmt.Sprintf(`{"id":"n%d","kind":"attribute","name":"A%d"}`, index, index))
	}
	manyEdges := make([]string, 0, maxFlowDefinitionEdges+1)
	for index := 0; index <= maxFlowDefinitionEdges; index++ {
		manyEdges = append(manyEdges, `{"kind":"control","from":"a","to":"b"}`)
	}
	service := newFakeDefinitionFlowService(map[string]string{
		"GoodFlow":      validFlowDefinitionV2("GoodFlow", true),
		"MalformedFlow": `{"schemaVersion":`,
		"RenamedFlow":   validFlowDefinitionV2("SomeoneElse", true),
		"LegacyFlow":    validFlowDefinitionV1("LegacyFlow"),
		"NodesFlow":     strings.Replace(validFlowDefinitionV2("NodesFlow", true), `"nodes":[]`, `"nodes":[`+strings.Join(manyNodes, ",")+`]`, 1),
		"EdgesFlow":     strings.Replace(validFlowDefinitionV2("EdgesFlow", true), `"edges":[]`, `"edges":[`+strings.Join(manyEdges, ",")+`]`, 1),
		"StringFlow":    strings.Replace(validFlowDefinitionV2("StringFlow", true), `"Note"`, `"`+strings.Repeat("x", maxFlowDefinitionStringCharacters+1)+`"`, 1),
		"DeepFlow":      strings.Replace(validFlowDefinitionV2("DeepFlow", true), `"diagnostics":[]`, `"diagnostics":[`+strings.Repeat("[", maxFlowDefinitionDepth)+strings.Repeat("]", maxFlowDefinitionDepth)+`]`, 1),
		"HugeFlow":      validFlowDefinitionV2("HugeFlow", true),
	})
	service.oversized["HugeFlow"] = true
	sources := newTestFlowDefinitionSources(t, "", service)

	snapshot, err := sources.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.V2Definitions) != 1 {
		t.Fatalf("v2 definitions = %v", snapshot.V2Definitions)
	}
	if _, found := snapshot.V2Definitions["GoodFlow"]; !found {
		t.Fatal("GoodFlow is missing")
	}
	wantErrors := map[string]string{
		"MalformedFlow": "parse Flow Definition Graph",
		"RenamedFlow":   `names Flow type "SomeoneElse"`,
		"LegacyFlow":    "uploaded definitions must use 2.0",
		"NodesFlow":     "nodes; the limit is",
		"EdgesFlow":     "edges; the limit is",
		"StringFlow":    "longer than",
		"DeepFlow":      "nests deeper than",
		"HugeFlow":      "too large to read",
	}
	for flowType, wantError := range wantErrors {
		definition := definitionByID(t, snapshot, "uploaded:"+flowType)
		if !strings.Contains(definition.Error, wantError) || definition.FlowName != flowType || definition.Graph != nil || definition.Valid {
			t.Errorf("%s = %+v, want error containing %q", flowType, definition, wantError)
		}
	}
	if !strings.Contains(string(snapshot.metadataResponse), `"error":`) || strings.Contains(string(snapshot.metadataResponse), `"graph"`) {
		t.Fatalf("metadata response = %s", snapshot.metadataResponse)
	}
}

func TestServerFlowDefinitionFailuresKeepDirectoryDefinitions(t *testing.T) {
	directory := t.TempDir()
	writeFlowDefinitionTestFile(t, directory, "refund.json", validFlowDefinitionV2("RefundFlow", true))
	service := newFakeDefinitionFlowService(map[string]string{"OtherFlow": validFlowDefinitionV2("OtherFlow", true)})
	service.failure = status.Error(codes.Unavailable, "server down")
	sources := newTestFlowDefinitionSources(t, directory, service)

	snapshot, err := sources.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, found := snapshot.V2Definitions["RefundFlow"]; !found || !strings.Contains(string(snapshot.metadataResponse), "server down") {
		t.Fatalf("snapshot = %s", snapshot.metadataResponse)
	}
	if _, err := sources.loadFlowTypes(context.Background(), []string{"RefundFlow"}); err != nil {
		t.Fatalf("a directory Flow type failed with the server: %v", err)
	}
	_, err = sources.loadFlowTypes(context.Background(), []string{"OtherFlow"})
	var coded interface{ DefinitionErrorCode() string }
	if !errors.As(err, &coded) || coded.DefinitionErrorCode() != flowDefinitionSourceUnavailable {
		t.Fatalf("OtherFlow error = %v", err)
	}
}

func TestFlowDefinitionRevisionsFollowTheirSource(t *testing.T) {
	directory := t.TempDir()
	service := newFakeDefinitionFlowService(map[string]string{"RefundFlow": validFlowDefinitionV2("RefundFlow", true)})
	sources := newTestFlowDefinitionSources(t, directory, service)
	revision := func() string {
		t.Helper()
		snapshot, err := sources.loadFlowTypes(context.Background(), []string{"RefundFlow"})
		if err != nil {
			t.Fatal(err)
		}
		if snapshot.flowTypeRevisions["RefundFlow"] == "" {
			t.Fatal("RefundFlow has an empty revision")
		}
		return snapshot.flowTypeRevisions["RefundFlow"]
	}
	uploaded := revision()
	if revision() != uploaded {
		t.Fatal("the revision changed without a change")
	}
	writeFlowDefinitionTestFile(t, directory, "refund.json", validFlowDefinitionV2("RefundFlow", true))
	fromDirectory := revision()
	if fromDirectory == uploaded {
		t.Fatal("moving the same content to the directory kept the revision")
	}
	writeFlowDefinitionTestFile(t, directory, "refund.json", withV2Action("RefundFlow", ""))
	if revision() == fromDirectory {
		t.Fatal("changed content kept the revision")
	}
}

func TestFlowDefinitionsIgnoreUnknownTopLevelFields(t *testing.T) {
	withGeneratedAt := strings.Replace(validFlowDefinitionV2("RefundFlow", true), `{"schemaVersion"`, `{"generatedAt":"2026-11-10T00:00:00Z","futureField":{"a":1},"schemaVersion"`, 1)
	directory := t.TempDir()
	writeFlowDefinitionTestFile(t, directory, "refund.json", withGeneratedAt)
	if _, err := loadFlowDefinitions(directory); err != nil {
		t.Fatalf("unknown top-level fields were rejected: %v", err)
	}
	unknownSourceField := strings.Replace(validFlowDefinitionV2("RefundFlow", true), `"path":"flow.go"`, `"path":"flow.go","extra":true`, 1)
	writeFlowDefinitionTestFile(t, directory, "refund.json", unknownSourceField)
	if _, err := loadFlowDefinitions(directory); err == nil || !strings.Contains(err.Error(), `unknown field "extra"`) {
		t.Fatalf("unknown source field error = %v", err)
	}
}

func newDefinitionTestServer(t *testing.T, directory string, service *fakeDefinitionFlowService) http.Handler {
	t.Helper()
	assets := fstest.MapFS{"dist/index.html": &fstest.MapFile{Data: []byte("<html><head></head><body></body></html>")}}
	server, err := NewServer(&Config{FlowRenderingDirectory: directory}, service, assets)
	if err != nil {
		t.Fatal(err)
	}
	return server.Handler()
}

func TestFlowTypeDefinitionRouteServesOneFlowTypeWithRevision(t *testing.T) {
	sessionGraph := agentSessionTestGraph("Agent/Session")
	service := newFakeDefinitionFlowService(map[string]string{
		"Agent/Session": sessionGraph,
		"OtherFlow":     validFlowDefinitionV2("OtherFlow", true),
	})
	handler := newDefinitionTestServer(t, "", service)

	request := httptest.NewRequest(http.MethodGet, "/api/flow-definitions/"+url.PathEscape("Agent/Session"), nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var body struct {
		FlowType           string `json:"flowType"`
		DefinitionRevision string `json:"definitionRevision"`
		Definitions        []struct {
			ID    string          `json:"id"`
			Graph json.RawMessage `json:"graph"`
		} `json:"definitions"`
		V2 *struct {
			DefinitionRevision string `json:"definitionRevision"`
			AgentSession       *struct {
				Language     string   `json:"language"`
				HasQuestions bool     `json:"hasQuestions"`
				RPCNames     []string `json:"rpcNames"`
			} `json:"agentSession"`
		} `json:"v2"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || body.FlowType != "Agent/Session" || len(body.Definitions) != 1 || len(body.Definitions[0].Graph) == 0 ||
		body.V2 == nil || body.V2.DefinitionRevision != body.DefinitionRevision || body.V2.AgentSession == nil ||
		body.V2.AgentSession.Language != "python" || !body.V2.AgentSession.HasQuestions ||
		!slices.Equal(body.V2.AgentSession.RPCNames, []string{"resolve_interrupt", "send_user_message"}) {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
	if response.Header().Get("ETag") != strconv.Quote(body.DefinitionRevision) {
		t.Fatalf("ETag = %q", response.Header().Get("ETag"))
	}
	if requests := service.readRequests(); len(requests) != 1 || !slices.Equal(requests[0], []string{"Agent/Session"}) {
		t.Fatalf("GetFlowDefinitions requests = %v", requests)
	}
	if service.listCalls != 0 {
		t.Fatal("reading one Flow type listed every definition")
	}

	notModified := httptest.NewRequest(http.MethodGet, "/api/flow-definitions/"+url.PathEscape("Agent/Session"), nil)
	notModified.Header.Set("If-None-Match", strconv.Quote(body.DefinitionRevision))
	notModifiedResponse := httptest.NewRecorder()
	handler.ServeHTTP(notModifiedResponse, notModified)
	if notModifiedResponse.Code != http.StatusNotModified || notModifiedResponse.Body.Len() != 0 {
		t.Fatalf("If-None-Match response = %d %s", notModifiedResponse.Code, notModifiedResponse.Body.String())
	}

	missingResponse := httptest.NewRecorder()
	handler.ServeHTTP(missingResponse, httptest.NewRequest(http.MethodGet, "/api/flow-definitions/MissingFlow", nil))
	if missingResponse.Code != http.StatusNotFound || !strings.Contains(missingResponse.Body.String(), "FLOW_DEFINITION_NOT_FOUND") {
		t.Fatalf("missing response = %d %s", missingResponse.Code, missingResponse.Body.String())
	}
}

func TestV2RoutesLoadOnlyTheirFlowTypeAndCheckItsRevision(t *testing.T) {
	service := newFakeDefinitionFlowService(map[string]string{
		"RefundFlow": validFlowDefinitionV2("RefundFlow", true),
		"OtherFlow":  validFlowDefinitionV2("OtherFlow", true),
	})
	handler := newDefinitionTestServer(t, "", service)
	revisionResponse := httptest.NewRecorder()
	handler.ServeHTTP(revisionResponse, httptest.NewRequest(http.MethodGet, "/api/flow-definitions/RefundFlow", nil))
	revision := strings.Trim(revisionResponse.Header().Get("ETag"), `"`)
	if revision == "" {
		t.Fatalf("RefundFlow response = %d %s", revisionResponse.Code, revisionResponse.Body.String())
	}

	search := func(header string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/api/v2/search", strings.NewReader(`{"flowType":"RefundFlow"}`))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-Dex-Flow-Definition-Revision", header)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	if response := search("sha256:stale"); response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "FLOW_DEFINITION_CHANGED") {
		t.Fatalf("stale search = %d %s", response.Code, response.Body.String())
	}
	if response := search("sha256:other," + revision); response.Code != http.StatusOK {
		t.Fatalf("current search = %d %s", response.Code, response.Body.String())
	}
	invalid := httptest.NewRequest(http.MethodPost, "/api/v2/search", strings.NewReader(`{"flowTypes":["RefundFlow","bad name"]}`))
	invalid.Header.Set("Content-Type", "application/json")
	invalid.Header.Set("X-Dex-Flow-Definition-Revision", revision)
	invalidResponse := httptest.NewRecorder()
	handler.ServeHTTP(invalidResponse, invalid)
	if invalidResponse.Code != http.StatusBadRequest || !strings.Contains(invalidResponse.Body.String(), `bad name\" has no valid`) {
		t.Fatalf("search with a Flow type the server rejects = %d %s", invalidResponse.Code, invalidResponse.Body.String())
	}
	for _, request := range service.readRequests() {
		if slices.Contains(request, "OtherFlow") {
			t.Fatalf("a RefundFlow route read OtherFlow: %v", service.readRequests())
		}
	}
	if service.listCalls != 0 {
		t.Fatal("a v2 route listed every definition")
	}
}

// agentSessionTestGraph declares the session Attributes, Start Step, question Step, and two RPCs.
func agentSessionTestGraph(flowType string) string {
	nodes := `[{"id":"a1","kind":"attribute","name":"AgentState"},` +
		`{"id":"a2","kind":"attribute","name":"AgentMessages","resource":{"map":true}},` +
		`{"id":"a3","kind":"attribute","name":"AgentToolCalls","resource":{"map":true}},` +
		`{"id":"a4","kind":"attribute","name":"PendingInterrupts"},` +
		`{"id":"s1","kind":"step","name":"StartAgentSession","start":true,"phase":"execute"},` +
		`{"id":"s2","kind":"step","name":"AwaitQuestionAnswer","phase":"wait_for+execute"},` +
		`{"id":"r1","kind":"rpc","name":"send_user_message"},` +
		`{"id":"r2","kind":"rpc","name":"resolve_interrupt"}]`
	graph := strings.Replace(validFlowDefinitionV2(flowType, true), `"nodes":[]`, `"nodes":`+nodes, 1)
	return strings.Replace(graph, `"language":"go"`, `"language":"python"`, 1)
}
