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
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"github.com/superdurable/dex/web/embedded/internal/web/api"
)

const (
	flowDefinitionSourceInvalid     = "FLOW_DEFINITION_INVALID"
	flowDefinitionSourceUnavailable = "FLOW_DEFINITION_SOURCE_UNAVAILABLE"
)

// Where one definition came from, as the catalog reports it.
const (
	flowDefinitionOriginDirectory = "directory"
	flowDefinitionOriginUploaded  = "uploaded"
	flowDefinitionOriginVisualize = "visualize"
)

// FlowDefinitionProvider loads one immutable, internally consistent definition snapshot.
type FlowDefinitionProvider interface {
	Load(context.Context) (*FlowDefinitionSnapshot, error)
}

// flowDefinitionLookup adds the reads that do not load every definition; the routes use it.
type flowDefinitionLookup interface {
	FlowDefinitionProvider
	// loadFlowTypes loads only the definitions of flowTypes; a Flow type without one is left out.
	loadFlowTypes(ctx context.Context, flowTypes []string) (*FlowDefinitionSnapshot, error)
	// listFlowTypes names every Flow type that has a definition without reading uploaded content.
	listFlowTypes(ctx context.Context) (*flowDefinitionTypeList, error)
}

// FlowDefinitionSnapshot is the validated catalog used throughout one HTTP request.
type FlowDefinitionSnapshot struct {
	// Response is the catalog JSON with every graph; Connector setup reads it and it never reaches the browser.
	Response           []byte
	V2Definitions      map[string]api.V2Definition
	DefinitionRevision string
	Source             string
	LoadedAt           time.Time
	DefinitionCount    int
	// metadataResponse is the catalog JSON without graphs that GET /api/flow-definitions returns.
	metadataResponse []byte
	// flowTypeRevisions maps each Flow type in the snapshot to the revision of its own definitions.
	flowTypeRevisions map[string]string
	definitions       []flowDefinition
}

// flowDefinitionTypeList names the Flow types that have a definition, with where each one comes from.
type flowDefinitionTypeList struct {
	Configured   bool                     `json:"configured"`
	OperatorAPI  bool                     `json:"operatorAPI"`
	FlowTypes    []flowDefinitionTypeName `json:"flowTypes"`
	SourceErrors []string                 `json:"sourceErrors,omitempty"`
	// source is the catalog source name that readiness reports.
	source string
}

type flowDefinitionTypeName struct {
	FlowType string `json:"flowType"`
	Source   string `json:"source"`
}

type flowDefinitionSourceError struct {
	code string
	err  error
}

func (e *flowDefinitionSourceError) Error() string               { return e.err.Error() }
func (e *flowDefinitionSourceError) Unwrap() error               { return e.err }
func (e *flowDefinitionSourceError) DefinitionErrorCode() string { return e.code }

// flowDefinitionDocument is one definition document before validation.
type flowDefinitionDocument struct {
	origin string
	// path is the file path relative to the directory; empty for an uploaded document.
	path string
	// flowType is the Flow type the server stored the document under; empty for a file.
	flowType    string
	data        []byte
	generatedAt *time.Time
	uploadedAt  *time.Time
	// readErr is set when the document could not be read, so only it is invalid.
	readErr error
	// claimsNoFlowType is set for a file too large to read, so an upload of its Flow type is still shown.
	claimsNoFlowType bool
}

// DirectoryFlowDefinitionProvider reloads directory definitions on every request.
type DirectoryFlowDefinitionProvider struct {
	sources flowDefinitionSources
}

// NewDirectoryFlowDefinitionProvider creates a dynamic directory provider.
func NewDirectoryFlowDefinitionProvider(directory string) (*DirectoryFlowDefinitionProvider, error) {
	sources, err := newFlowDefinitionSources(directory, nil)
	if err != nil {
		return nil, err
	}
	return &DirectoryFlowDefinitionProvider{sources: sources}, nil
}

func (p *DirectoryFlowDefinitionProvider) Load(ctx context.Context) (*FlowDefinitionSnapshot, error) {
	return p.sources.Load(ctx)
}

func (p *DirectoryFlowDefinitionProvider) loadFlowTypes(ctx context.Context, flowTypes []string) (*FlowDefinitionSnapshot, error) {
	return p.sources.loadFlowTypes(ctx, flowTypes)
}

func (p *DirectoryFlowDefinitionProvider) listFlowTypes(ctx context.Context) (*flowDefinitionTypeList, error) {
	return p.sources.listFlowTypes(ctx)
}

// flowDefinitionSources merges a directory and the definitions uploaded to the Dex Server.
// A Flow type that any directory file declares comes only from the directory, even when that file is invalid.
type flowDefinitionSources struct {
	// directory is empty when no directory is configured.
	directory string
	// server is nil when Dex Web has no FlowService, as for a visualize server.
	server *serverFlowDefinitionSource
	// memory replaces the directory for a visualize server.
	memory []flowDefinitionDocument
	// memoryLocation describes where memory came from in the catalog.
	memoryLocation string
}

func newFlowDefinitionSources(directory string, client dexpb.FlowServiceClient) (flowDefinitionSources, error) {
	sources := flowDefinitionSources{}
	if client != nil {
		sources.server = newServerFlowDefinitionSource(client)
	}
	directory = strings.TrimSpace(directory)
	if directory == "" {
		return sources, nil
	}
	absoluteDirectory, err := filepath.Abs(directory)
	if err != nil {
		return flowDefinitionSources{}, fmt.Errorf("resolve Flow rendering directory: %w", err)
	}
	sources.directory = absoluteDirectory
	return sources, nil
}

func (s flowDefinitionSources) Load(ctx context.Context) (*FlowDefinitionSnapshot, error) {
	local, err := s.readLocalDefinitions()
	if err != nil {
		return nil, err
	}
	var uploaded []flowDefinition
	var sourceErrors []string
	if s.server != nil {
		flowTypes, listErr := s.server.listFlowTypes(ctx)
		if listErr != nil {
			sourceErrors = append(sourceErrors, listErr.Error())
		}
		documents, readErr := s.server.readFlowTypes(ctx, withoutLocalFlowTypes(flowTypes, local), true)
		if readErr != nil {
			sourceErrors = append(sourceErrors, readErr.Error())
		}
		uploaded = buildFlowDefinitions(documents)
	}
	return s.buildSnapshot(append(local, uploaded...), sourceErrors)
}

func (s flowDefinitionSources) loadFlowTypes(ctx context.Context, flowTypes []string) (*FlowDefinitionSnapshot, error) {
	local, err := s.readLocalDefinitions()
	if err != nil {
		return nil, err
	}
	requested := make(map[string]bool, len(flowTypes))
	for _, flowType := range flowTypes {
		requested[flowType] = true
	}
	selected := make([]flowDefinition, 0, len(flowTypes))
	for _, definition := range local {
		if requested[definition.FlowName] {
			selected = append(selected, definition)
		}
	}
	if s.server != nil {
		remaining := withoutLocalFlowTypes(uniqueFlowTypes(flowTypes), local)
		uploaded, readErr := s.server.readFlowTypes(ctx, remaining, false)
		if readErr != nil {
			return nil, unavailableDefinitionSource(readErr)
		}
		selected = append(selected, buildFlowDefinitions(uploaded)...)
	}
	return s.buildSnapshot(selected, nil)
}

func (s flowDefinitionSources) listFlowTypes(ctx context.Context) (*flowDefinitionTypeList, error) {
	local, err := s.readLocalDefinitions()
	if err != nil {
		return nil, err
	}
	list := &flowDefinitionTypeList{
		Configured: s.isConfigured(), OperatorAPI: s.server != nil, FlowTypes: []flowDefinitionTypeName{}, source: s.catalogSource(),
	}
	seen := make(map[string]bool)
	for _, definition := range local {
		if definition.FlowName != "" && !seen[definition.FlowName] {
			seen[definition.FlowName] = true
			list.FlowTypes = append(list.FlowTypes, flowDefinitionTypeName{FlowType: definition.FlowName, Source: definition.Source})
		}
	}
	if s.server != nil {
		flowTypes, listErr := s.server.listFlowTypes(ctx)
		if listErr != nil {
			list.SourceErrors = append(list.SourceErrors, listErr.Error())
		}
		for _, flowType := range flowTypes {
			if !seen[flowType] {
				seen[flowType] = true
				list.FlowTypes = append(list.FlowTypes, flowDefinitionTypeName{FlowType: flowType, Source: flowDefinitionOriginUploaded})
			}
		}
	}
	sort.Slice(list.FlowTypes, func(left int, right int) bool {
		return list.FlowTypes[left].FlowType < list.FlowTypes[right].FlowType
	})
	return list, nil
}

func (s flowDefinitionSources) isConfigured() bool {
	return s.directory != "" || s.server != nil || s.memory != nil
}

// readLocalDefinitions validates the directory or in-memory documents; only an unreadable directory fails.
func (s flowDefinitionSources) readLocalDefinitions() ([]flowDefinition, error) {
	if s.memory != nil {
		return buildFlowDefinitions(s.memory), nil
	}
	if s.directory == "" {
		return nil, nil
	}
	documents, err := readDirectoryDefinitionFiles(s.directory)
	if err != nil {
		return nil, classifyDefinitionReadError(err)
	}
	return buildFlowDefinitions(documents), nil
}

func (s flowDefinitionSources) buildSnapshot(definitions []flowDefinition, sourceErrors []string) (*FlowDefinitionSnapshot, error) {
	location := s.directory
	if s.memory != nil {
		location = s.memoryLocation
	}
	return assembleFlowDefinitionSnapshot(definitions, flowDefinitionCatalogHeader{
		configured: s.isConfigured(), source: s.catalogSource(), directory: location, sourceErrors: sourceErrors,
	})
}

// catalogSource names the configured sources: memory, local, server, or local+server.
func (s flowDefinitionSources) catalogSource() string {
	switch {
	case s.memory != nil:
		return "memory"
	case s.server != nil && s.directory != "":
		return "local+server"
	case s.server != nil:
		return "server"
	default:
		return "local"
	}
}

func readDirectoryDefinitionFiles(directory string) ([]flowDefinitionDocument, error) {
	info, err := os.Stat(directory)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("Flow rendering path is not a directory: %s", directory)
	}
	documents := make([]flowDefinitionDocument, 0)
	err = filepath.WalkDir(directory, func(filePath string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".json") {
			return nil
		}
		relativePath, relativeErr := filepath.Rel(directory, filePath)
		if relativeErr != nil {
			return relativeErr
		}
		document := flowDefinitionDocument{origin: flowDefinitionOriginDirectory, path: filepath.ToSlash(relativePath)}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return infoErr
		}
		if info.Size() > maxFlowDefinitionBytes {
			document.readErr = fmt.Errorf("Flow Definition Graph is too large: it exceeds %d bytes: %s", maxFlowDefinitionBytes, document.path)
			document.claimsNoFlowType = true
			documents = append(documents, document)
			return nil
		}
		data, readErr := os.ReadFile(filePath)
		if readErr != nil {
			return readErr
		}
		document.data = data
		documents = append(documents, document)
		return nil
	})
	sort.Slice(documents, func(left int, right int) bool { return documents[left].path < documents[right].path })
	return documents, err
}

// buildFlowDefinitions validates each document on its own, so a bad document marks only itself invalid.
func buildFlowDefinitions(documents []flowDefinitionDocument) []flowDefinition {
	definitions := make([]flowDefinition, 0, len(documents))
	for _, document := range documents {
		definitions = append(definitions, buildFlowDefinition(document))
	}
	markRepeatedV2FlowTypes(definitions)
	return definitions
}

func buildFlowDefinition(document flowDefinitionDocument) flowDefinition {
	name := document.path
	if document.origin == flowDefinitionOriginUploaded {
		name = "uploaded Flow type " + document.flowType
	}
	definition, err := validateFlowDefinitionDocument(name, document)
	if err != nil {
		definition = flowDefinition{FlowName: fallbackFlowName(document), Error: err.Error()}
	}
	definition.ID = flowDefinitionID(document)
	definition.File = document.path
	definition.Source = document.origin
	definition.GeneratedAt = document.generatedAt
	definition.UploadedAt = document.uploadedAt
	if document.data != nil {
		definition.Digest = sha256Hex(document.data)
	}
	return definition
}

func validateFlowDefinitionDocument(name string, document flowDefinitionDocument) (flowDefinition, error) {
	if document.readErr != nil {
		return flowDefinition{}, document.readErr
	}
	if len(document.data) > maxFlowDefinitionBytes {
		return flowDefinition{}, fmt.Errorf("Flow Definition Graph exceeds %d bytes: %s", maxFlowDefinitionBytes, name)
	}
	if err := checkFlowDefinitionDocumentLimits(document.data); err != nil {
		return flowDefinition{}, fmt.Errorf("Flow Definition Graph %s exceeds a limit: %w", name, err)
	}
	definition, err := flowDefinitionFromGraph(name, document.data)
	if err != nil {
		return flowDefinition{}, err
	}
	if document.origin == flowDefinitionOriginUploaded {
		if definition.SchemaVersion != "2.0" {
			return flowDefinition{}, fmt.Errorf("Flow Definition Graph %s has schemaVersion %q; uploaded definitions must use 2.0", name, definition.SchemaVersion)
		}
		if definition.FlowName != document.flowType {
			return flowDefinition{}, fmt.Errorf("Flow Definition Graph %s names Flow type %q", name, definition.FlowName)
		}
	}
	return definition, nil
}

// fallbackFlowName names an invalid document's Flow type so the directory still claims it. A file too large to
// read gets "", which claims no Flow type.
func fallbackFlowName(document flowDefinitionDocument) string {
	if document.flowType != "" {
		return document.flowType
	}
	if document.claimsNoFlowType {
		return ""
	}
	var header struct {
		Flow struct {
			Name string `json:"name"`
		} `json:"flow"`
	}
	if document.data != nil && json.Unmarshal(document.data, &header) == nil && strings.TrimSpace(header.Flow.Name) != "" {
		return strings.TrimSpace(header.Flow.Name)
	}
	return strings.TrimSuffix(document.path, filepath.Ext(document.path))
}

func flowDefinitionID(document flowDefinitionDocument) string {
	if document.origin == flowDefinitionOriginUploaded {
		return "uploaded:" + document.flowType
	}
	return document.path
}

// markRepeatedV2FlowTypes invalidates every valid 2.0 file of a Flow type that more than one file declares.
func markRepeatedV2FlowTypes(definitions []flowDefinition) {
	indexesByFlowType := make(map[string][]int)
	for index, definition := range definitions {
		if definition.Error == "" && definition.SchemaVersion == "2.0" && definition.Valid {
			indexesByFlowType[definition.FlowName] = append(indexesByFlowType[definition.FlowName], index)
		}
	}
	for flowType, indexes := range indexesByFlowType {
		if len(indexes) < 2 {
			continue
		}
		files := make([]string, 0, len(indexes))
		for _, index := range indexes {
			files = append(files, definitions[index].File)
		}
		message := fmt.Sprintf("multiple valid Flow Definition Graph 2.0 files define Flow type %q: %s", flowType, strings.Join(files, " and "))
		for _, index := range indexes {
			definitions[index] = flowDefinition{
				ID: definitions[index].ID, File: definitions[index].File, FlowName: flowType,
				Source: definitions[index].Source, Digest: definitions[index].Digest, Error: message,
			}
		}
	}
}

type flowDefinitionCatalogHeader struct {
	configured   bool
	source       string
	directory    string
	sourceErrors []string
}

func assembleFlowDefinitionSnapshot(definitions []flowDefinition, header flowDefinitionCatalogHeader) (*FlowDefinitionSnapshot, error) {
	flowTypeRevisions := flowDefinitionRevisions(definitions)
	revision := catalogRevision(flowTypeRevisions)
	catalog := flowDefinitionCatalog{
		Configured:         header.configured,
		Source:             header.source,
		DefinitionRevision: revision,
		DefinitionCount:    len(definitions),
		Definitions:        definitions,
		SourceErrors:       header.sourceErrors,
	}
	if header.source == "local" || header.source == "local+server" || header.source == "memory" {
		catalog.Directory = header.directory
	}
	v2Definitions := make(map[string]api.V2Definition)
	for _, definition := range definitions {
		if definition.Error == "" && definition.SchemaVersion == "2.0" && definition.Valid {
			v2Definitions[definition.FlowName] = *definition.V2
		}
	}
	response, err := json.Marshal(catalog)
	if err != nil {
		return nil, invalidDefinitionSource(fmt.Errorf("encode Flow Definition Graph catalog: %w", err))
	}
	metadataCatalog := catalog
	metadataCatalog.Definitions = make([]flowDefinition, 0, len(definitions))
	for _, definition := range definitions {
		definition.Graph = nil
		definition.V2 = nil
		metadataCatalog.Definitions = append(metadataCatalog.Definitions, definition)
	}
	metadataResponse, err := json.Marshal(metadataCatalog)
	if err != nil {
		return nil, invalidDefinitionSource(fmt.Errorf("encode Flow Definition Graph catalog metadata: %w", err))
	}
	return &FlowDefinitionSnapshot{
		Response:           response,
		V2Definitions:      v2Definitions,
		DefinitionRevision: revision,
		Source:             header.source,
		LoadedAt:           time.Now().UTC(),
		DefinitionCount:    len(definitions),
		metadataResponse:   metadataResponse,
		flowTypeRevisions:  flowTypeRevisions,
		definitions:        definitions,
	}, nil
}

// flowDefinitionRevisions digests the source, ID, and content digest of each Flow type's definitions.
// The result is never empty, so a moved or replaced definition always changes it.
func flowDefinitionRevisions(definitions []flowDefinition) map[string]string {
	hashes := make(map[string][]string)
	for _, definition := range definitions {
		hashes[definition.FlowName] = append(hashes[definition.FlowName], definition.Source, definition.ID, definition.Digest, definition.Error)
	}
	revisions := make(map[string]string, len(hashes))
	for flowType, parts := range hashes {
		revisions[flowType] = framedDigest(parts)
	}
	return revisions
}

func catalogRevision(flowTypeRevisions map[string]string) string {
	flowTypes := make([]string, 0, len(flowTypeRevisions))
	for flowType := range flowTypeRevisions {
		flowTypes = append(flowTypes, flowType)
	}
	sort.Strings(flowTypes)
	parts := make([]string, 0, 2*len(flowTypes))
	for _, flowType := range flowTypes {
		parts = append(parts, flowType, flowTypeRevisions[flowType])
	}
	return framedDigest(parts)
}

// framedDigest frames each part with its unsigned 64-bit big-endian length before hashing.
func framedDigest(parts []string) string {
	hash := sha256.New()
	var length [8]byte
	for _, part := range parts {
		binary.BigEndian.PutUint64(length[:], uint64(len(part)))
		// hash.Hash.Write never returns an error.
		_, _ = hash.Write(length[:])
		_, _ = hash.Write([]byte(part))
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

func sha256Hex(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func withoutLocalFlowTypes(flowTypes []string, local []flowDefinition) []string {
	claimed := make(map[string]bool, len(local))
	for _, definition := range local {
		if definition.FlowName != "" {
			claimed[definition.FlowName] = true
		}
	}
	remaining := make([]string, 0, len(flowTypes))
	for _, flowType := range flowTypes {
		if !claimed[flowType] {
			remaining = append(remaining, flowType)
		}
	}
	return remaining
}

func uniqueFlowTypes(flowTypes []string) []string {
	seen := make(map[string]bool, len(flowTypes))
	unique := make([]string, 0, len(flowTypes))
	for _, flowType := range flowTypes {
		if flowType != "" && !seen[flowType] {
			seen[flowType] = true
			unique = append(unique, flowType)
		}
	}
	return unique
}

func invalidDefinitionSource(err error) error {
	return &flowDefinitionSourceError{code: flowDefinitionSourceInvalid, err: err}
}

func unavailableDefinitionSource(err error) error {
	return &flowDefinitionSourceError{code: flowDefinitionSourceUnavailable, err: err}
}

func classifyDefinitionReadError(err error) error {
	var coded interface{ DefinitionErrorCode() string }
	if errors.As(err, &coded) {
		return err
	}
	return unavailableDefinitionSource(err)
}
