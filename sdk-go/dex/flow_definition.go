// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package dex

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	// maxFlowDefinitionUploadBatchItems matches the server's per-request item limit.
	maxFlowDefinitionUploadBatchItems = 256
	// maxFlowDefinitionUploadBatchBytes keeps each request below the default 32 MiB gRPC message limit.
	maxFlowDefinitionUploadBatchBytes = 16 << 20
	flowDefinitionSchemaVersionPrefix = "2."
)

// FlowDefinitionDocument is one Flow Definition Graph (FDG 2.0) JSON document to store on the Dex server.
//
// Dex Web reads stored documents to draw a Flow's graph and offer its Actions, so the
// application does not need to ship the JSON files to Dex Web. Produce the JSON with
// dexcli visualize --schema-version 2.0 --json, or build it in memory.
//
// Pass documents to a Worker with WorkerOptions.FlowDefinitions, or upload them
// from a deployment script with Client.UploadFlowDefinitions. ReadFlowDefinitionFiles
// returns documents read from files.
//
// The server keeps one document per Flow type. A document with a later GeneratedAt
// replaces a different stored document; an older or equal one leaves the stored
// document in place. Use the same GeneratedAt for every process of one release, such as
// the build time, so that rolling deployments do not flip between versions.
//
//	documents := []dex.FlowDefinitionDocument{{
//		FlowType:    "OrderFlow",
//		Content:     orderFlowDefinitionJSON,
//		GeneratedAt: releaseTime,
//	}}
//	worker, err := dex.NewWorker(registry, cache, dex.WorkerOptions{FlowDefinitions: documents})
//
// Since 1.45.0.
type FlowDefinitionDocument struct {
	// FlowType is the Flow type that the document describes.
	// It must be non-empty and equal the document's flow.name.
	FlowType string
	// Content is the FDG JSON document. It must be a JSON object whose schemaVersion starts with "2.".
	// The server stores these bytes unchanged, except that the SDK rewrites an absolute
	// source.path to a path relative to the working directory, or to its base name.
	Content []byte
	// GeneratedAt is when the document was generated. It must not be the zero time.
	// The server keeps microsecond precision and rejects a time more than 24 hours in its future.
	GeneratedAt time.Time
}

// FlowDefinitionUploadOutcome reports what the server did with one uploaded Flow definition.
//
// Since 1.45.0.
type FlowDefinitionUploadOutcome uint8

const (
	// FlowDefinitionUploadOutcomeUnknown is an outcome this SDK does not recognize.
	FlowDefinitionUploadOutcomeUnknown FlowDefinitionUploadOutcome = iota
	// FlowDefinitionUploadCreated means no document was stored for the Flow type and the uploaded one now is.
	FlowDefinitionUploadCreated
	// FlowDefinitionUploadReplaced means the uploaded document replaced a different, older one.
	FlowDefinitionUploadReplaced
	// FlowDefinitionUploadUnchanged means the stored document has the same digest, so nothing was written.
	FlowDefinitionUploadUnchanged
	// FlowDefinitionUploadKeptExisting means the stored document differs and has the same or a later
	// generated time, so it stays. Upload with force to replace it anyway.
	FlowDefinitionUploadKeptExisting
)

// String returns the outcome name, such as "CREATED" or "KEPT_EXISTING", or "UNKNOWN".
func (outcome FlowDefinitionUploadOutcome) String() string {
	switch outcome {
	case FlowDefinitionUploadCreated:
		return "CREATED"
	case FlowDefinitionUploadReplaced:
		return "REPLACED"
	case FlowDefinitionUploadUnchanged:
		return "UNCHANGED"
	case FlowDefinitionUploadKeptExisting:
		return "KEPT_EXISTING"
	default:
		return "UNKNOWN"
	}
}

// FlowDefinitionUploadResult is the outcome for one uploaded Flow type and the document stored afterwards.
//
// Client.UploadFlowDefinitions returns one result for each uploaded document, in input order.
//
// Since 1.45.0.
type FlowDefinitionUploadResult struct {
	// FlowType is the Flow type of the uploaded document.
	FlowType string
	// Outcome is what the server did with the uploaded document.
	Outcome FlowDefinitionUploadOutcome
	// StoredDigest is the lowercase hexadecimal SHA-256 digest of the document stored after the call.
	// It differs from the uploaded document's digest only for FlowDefinitionUploadKeptExisting.
	StoredDigest string
	// StoredGeneratedAt is the generated time of the document stored after the call, at microsecond precision.
	StoredGeneratedAt time.Time
}

// ReadFlowDefinitionFiles reads Flow Definition Graph (FDG 2.0) JSON documents from files and directories.
//
// Each path names a file or a directory. A directory is read recursively and only its
// files ending in ".json" are read; a named file is read whatever its extension.
// Every file read must be an FDG JSON object whose schemaVersion starts with "2."
// and whose flow.name is a non-empty string. flow.name becomes the Flow type.
// GeneratedAt comes from the top-level generatedAt (RFC 3339) when present, otherwise
// from the file modification time. An absolute source.path is rewritten to a path
// relative to the working directory when the file lies under it, otherwise to its base name.
// Only that string changes; the other bytes stay as written.
//
// The same file reached through two paths is read once. Two files with the same
// Flow type are an error. Documents are returned sorted by Flow type.
// WorkerOptions.FlowDefinitionPaths and dexcli flow-definition upload use the same rules.
//
//	documents, err := dex.ReadFlowDefinitionFiles([]string{"flow-definitions"})
//	if err != nil {
//		return err
//	}
//	results, err := client.UploadFlowDefinitions(ctx, documents, false)
//
// It returns an error that names the path for a missing or unreadable path, invalid JSON,
// an unsupported schemaVersion, a missing flow.name, an invalid generatedAt, or a duplicate Flow type.
//
// Since 1.45.0.
func ReadFlowDefinitionFiles(paths []string) ([]FlowDefinitionDocument, error) {
	filePaths, err := collectFlowDefinitionFilePaths(paths)
	if err != nil {
		return nil, err
	}
	workingDirectory := currentWorkingDirectory()
	documents := make([]FlowDefinitionDocument, 0, len(filePaths))
	sourceFileByFlowType := make(map[string]string, len(filePaths))
	for _, filePath := range filePaths {
		document, err := readFlowDefinitionFile(filePath, workingDirectory)
		if err != nil {
			return nil, err
		}
		if previous, exists := sourceFileByFlowType[document.FlowType]; exists {
			return nil, fmt.Errorf(
				"dex: Flow definition files %q and %q both describe Flow type %q",
				previous, filePath, document.FlowType,
			)
		}
		sourceFileByFlowType[document.FlowType] = filePath
		documents = append(documents, document)
	}
	sort.Slice(documents, func(i, j int) bool { return documents[i].FlowType < documents[j].FlowType })
	return documents, nil
}

// collectFlowDefinitionFilePaths expands directories and returns unique absolute file paths in walk order.
func collectFlowDefinitionFilePaths(paths []string) ([]string, error) {
	filePaths := make([]string, 0, len(paths))
	isSeen := make(map[string]bool, len(paths))
	addFilePath := func(filePath string) {
		if !isSeen[filePath] {
			isSeen[filePath] = true
			filePaths = append(filePaths, filePath)
		}
	}
	for _, path := range paths {
		if strings.TrimSpace(path) == "" {
			return nil, errors.New("dex: Flow definition path must not be empty")
		}
		absolutePath, err := filepath.Abs(path)
		if err != nil {
			return nil, fmt.Errorf("dex: resolve Flow definition path %q: %w", path, err)
		}
		info, err := os.Stat(absolutePath)
		if err != nil {
			return nil, fmt.Errorf("dex: read Flow definition path %q: %w", path, err)
		}
		if !info.IsDir() {
			addFilePath(absolutePath)
			continue
		}
		err = filepath.WalkDir(absolutePath, func(walkedPath string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.Type().IsRegular() && strings.HasSuffix(entry.Name(), ".json") {
				addFilePath(walkedPath)
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("dex: read Flow definition directory %q: %w", path, err)
		}
	}
	return filePaths, nil
}

func readFlowDefinitionFile(filePath string, workingDirectory string) (FlowDefinitionDocument, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return FlowDefinitionDocument{}, fmt.Errorf("dex: read Flow definition file %q: %w", filePath, err)
	}
	header, err := parseFlowDefinitionHeader(content)
	if err != nil {
		return FlowDefinitionDocument{}, fmt.Errorf("dex: Flow definition file %q: %w", filePath, err)
	}
	generatedAt := header.generatedAt
	if generatedAt.IsZero() {
		info, err := os.Stat(filePath)
		if err != nil {
			return FlowDefinitionDocument{}, fmt.Errorf("dex: read Flow definition file %q: %w", filePath, err)
		}
		generatedAt = info.ModTime()
	}
	return FlowDefinitionDocument{
		FlowType:    header.flowType,
		Content:     rewriteFlowDefinitionSourcePath(content, workingDirectory),
		GeneratedAt: generatedAt,
	}, nil
}

type flowDefinitionHeader struct {
	flowType    string
	generatedAt time.Time
}

func parseFlowDefinitionHeader(content []byte) (flowDefinitionHeader, error) {
	var document struct {
		SchemaVersion *string `json:"schemaVersion"`
		Flow          *struct {
			Name *string `json:"name"`
		} `json:"flow"`
		GeneratedAt *string `json:"generatedAt"`
	}
	if err := json.Unmarshal(content, &document); err != nil {
		return flowDefinitionHeader{}, fmt.Errorf("parse JSON object: %w", err)
	}
	if document.SchemaVersion == nil || !strings.HasPrefix(*document.SchemaVersion, flowDefinitionSchemaVersionPrefix) {
		return flowDefinitionHeader{}, errors.New(`schemaVersion must start with "2."; regenerate with dexcli visualize --schema-version 2.0`)
	}
	if document.Flow == nil || document.Flow.Name == nil || *document.Flow.Name == "" {
		return flowDefinitionHeader{}, errors.New("flow.name must be a non-empty string")
	}
	header := flowDefinitionHeader{flowType: *document.Flow.Name}
	if document.GeneratedAt != nil {
		generatedAt, err := time.Parse(time.RFC3339Nano, *document.GeneratedAt)
		if err != nil {
			return flowDefinitionHeader{}, fmt.Errorf("generatedAt must be an RFC 3339 time: %w", err)
		}
		header.generatedAt = generatedAt
	}
	return header, nil
}

// mergeFlowDefinitionDocuments validates in-memory documents and rejects a Flow type given twice.
func mergeFlowDefinitionDocuments(fromPaths []FlowDefinitionDocument, inMemory []FlowDefinitionDocument) ([]FlowDefinitionDocument, error) {
	if err := validateFlowDefinitionDocuments(inMemory); err != nil {
		return nil, err
	}
	merged := make([]FlowDefinitionDocument, 0, len(fromPaths)+len(inMemory))
	isFromPaths := make(map[string]bool, len(fromPaths))
	for _, document := range fromPaths {
		isFromPaths[document.FlowType] = true
		merged = append(merged, document)
	}
	for _, document := range inMemory {
		if isFromPaths[document.FlowType] {
			return nil, fmt.Errorf(
				"dex: Flow type %q appears in both WorkerOptions.FlowDefinitionPaths and WorkerOptions.FlowDefinitions",
				document.FlowType,
			)
		}
		merged = append(merged, document)
	}
	return merged, nil
}

// validateFlowDefinitionDocuments checks locally what would make the server reject the whole request.
func validateFlowDefinitionDocuments(documents []FlowDefinitionDocument) error {
	isSeen := make(map[string]bool, len(documents))
	for index, document := range documents {
		if document.FlowType == "" {
			return fmt.Errorf("dex: Flow definition %d has an empty FlowType", index)
		}
		if isSeen[document.FlowType] {
			return fmt.Errorf("dex: Flow type %q has more than one Flow definition", document.FlowType)
		}
		isSeen[document.FlowType] = true
		if document.GeneratedAt.IsZero() {
			return fmt.Errorf("dex: Flow definition for Flow type %q has a zero GeneratedAt", document.FlowType)
		}
		header, err := parseFlowDefinitionHeader(document.Content)
		if err != nil {
			return fmt.Errorf("dex: Flow definition for Flow type %q: %w", document.FlowType, err)
		}
		if header.flowType != document.FlowType {
			return fmt.Errorf(
				"dex: Flow definition for Flow type %q has flow.name %q",
				document.FlowType, header.flowType,
			)
		}
	}
	return nil
}

// uploadFlowDefinitionDocuments sends documents in batches and stops at the first failed batch.
// It returns the results of the batches the server applied, in input order, and the first error.
func uploadFlowDefinitionDocuments(
	ctx context.Context,
	service dexpb.FlowServiceClient,
	documents []FlowDefinitionDocument,
	uploader string,
	isForced bool,
) ([]*dexpb.FlowDefinitionUploadResult, error) {
	workingDirectory := currentWorkingDirectory()
	results := make([]*dexpb.FlowDefinitionUploadResult, 0, len(documents))
	for _, batch := range splitFlowDefinitionUploads(documents, workingDirectory) {
		response, err := service.UploadFlowDefinitions(ctx, &dexpb.UploadFlowDefinitionsRequest{
			Definitions: batch,
			Uploader:    uploader,
			Force:       isForced,
		})
		if err != nil {
			return results, err
		}
		results = append(results, response.GetResults()...)
	}
	return results, nil
}

func splitFlowDefinitionUploads(documents []FlowDefinitionDocument, workingDirectory string) [][]*dexpb.FlowDefinitionUpload {
	var batches [][]*dexpb.FlowDefinitionUpload
	var batch []*dexpb.FlowDefinitionUpload
	batchBytes := 0
	for _, document := range documents {
		content := rewriteFlowDefinitionSourcePath(document.Content, workingDirectory)
		isBatchFull := len(batch) == maxFlowDefinitionUploadBatchItems ||
			(len(batch) > 0 && batchBytes+len(content) > maxFlowDefinitionUploadBatchBytes)
		if isBatchFull {
			batches = append(batches, batch)
			batch = nil
			batchBytes = 0
		}
		batch = append(batch, &dexpb.FlowDefinitionUpload{
			FlowType:    document.FlowType,
			Content:     content,
			GeneratedAt: timestamppb.New(document.GeneratedAt),
		})
		batchBytes += len(content)
	}
	if len(batch) > 0 {
		batches = append(batches, batch)
	}
	return batches
}

// rewriteFlowDefinitionSourcePath replaces an absolute source.path string and keeps every other byte.
func rewriteFlowDefinitionSourcePath(content []byte, workingDirectory string) []byte {
	location, isFound := findFlowDefinitionSourcePath(content)
	if !isFound || !filepath.IsAbs(location.value) {
		return content
	}
	encoded, err := encodeJSONStringWithoutHTMLEscape(relativeFlowDefinitionSourcePath(location.value, workingDirectory))
	if err != nil {
		// Encoding a Go string cannot fail; keep the original bytes if it somehow does.
		return content
	}
	rewritten := make([]byte, 0, len(content)-(location.end-location.start)+len(encoded))
	rewritten = append(rewritten, content[:location.start]...)
	rewritten = append(rewritten, encoded...)
	rewritten = append(rewritten, content[location.end:]...)
	return rewritten
}

type flowDefinitionSourcePathLocation struct {
	start int
	end   int
	value string
}

// findFlowDefinitionSourcePath returns the byte range of the first top-level source.path string value.
func findFlowDefinitionSourcePath(content []byte) (flowDefinitionSourcePathLocation, bool) {
	decoder := json.NewDecoder(bytes.NewReader(content))
	if !isNextJSONDelimiter(decoder, '{') {
		return flowDefinitionSourcePathLocation{}, false
	}
	for decoder.More() {
		key, isKey := nextJSONObjectKey(decoder)
		if !isKey {
			return flowDefinitionSourcePathLocation{}, false
		}
		if key != "source" {
			if !skipJSONValue(decoder) {
				return flowDefinitionSourcePathLocation{}, false
			}
			continue
		}
		if !isNextJSONDelimiter(decoder, '{') {
			return flowDefinitionSourcePathLocation{}, false
		}
		for decoder.More() {
			sourceKey, isSourceKey := nextJSONObjectKey(decoder)
			if !isSourceKey {
				return flowDefinitionSourcePathLocation{}, false
			}
			if sourceKey != "path" {
				if !skipJSONValue(decoder) {
					return flowDefinitionSourcePathLocation{}, false
				}
				continue
			}
			valueSearchStart := int(decoder.InputOffset())
			token, err := decoder.Token()
			if err != nil {
				return flowDefinitionSourcePathLocation{}, false
			}
			value, isString := token.(string)
			if !isString {
				return flowDefinitionSourcePathLocation{}, false
			}
			end := int(decoder.InputOffset())
			quoteOffset := bytes.IndexByte(content[valueSearchStart:end], '"')
			if quoteOffset < 0 {
				return flowDefinitionSourcePathLocation{}, false
			}
			return flowDefinitionSourcePathLocation{start: valueSearchStart + quoteOffset, end: end, value: value}, true
		}
		return flowDefinitionSourcePathLocation{}, false
	}
	return flowDefinitionSourcePathLocation{}, false
}

func isNextJSONDelimiter(decoder *json.Decoder, delimiter json.Delim) bool {
	token, err := decoder.Token()
	if err != nil {
		return false
	}
	actual, isDelimiter := token.(json.Delim)
	return isDelimiter && actual == delimiter
}

func nextJSONObjectKey(decoder *json.Decoder) (string, bool) {
	token, err := decoder.Token()
	if err != nil {
		return "", false
	}
	key, isString := token.(string)
	return key, isString
}

func skipJSONValue(decoder *json.Decoder) bool {
	var skipped json.RawMessage
	return decoder.Decode(&skipped) == nil
}

func relativeFlowDefinitionSourcePath(absolutePath string, workingDirectory string) string {
	if workingDirectory != "" {
		relativePath, err := filepath.Rel(workingDirectory, absolutePath)
		isUnderWorkingDirectory := err == nil && relativePath != ".." &&
			!strings.HasPrefix(relativePath, ".."+string(filepath.Separator))
		if isUnderWorkingDirectory {
			return filepath.ToSlash(relativePath)
		}
	}
	return filepath.Base(absolutePath)
}

func encodeJSONStringWithoutHTMLEscape(value string) ([]byte, error) {
	buffer := &bytes.Buffer{}
	encoder := json.NewEncoder(buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buffer.Bytes(), []byte("\n")), nil
}

// currentWorkingDirectory returns the working directory, or empty when it is unavailable.
// An empty result makes source.path rewriting fall back to base names.
func currentWorkingDirectory() string {
	workingDirectory, err := os.Getwd()
	if err != nil {
		return ""
	}
	return workingDirectory
}

// goSDKFlowDefinitionUploader identifies this SDK in the server's stored Flow definition metadata.
func goSDKFlowDefinitionUploader(sdkVersion string) string {
	return "sdk-go/" + sdkVersion
}

func flowDefinitionContentDigest(content []byte) string {
	digest := sha256.Sum256(content)
	return hex.EncodeToString(digest[:])
}

func mapFlowDefinitionUploadOutcome(outcome dexpb.FlowDefinitionUploadOutcome) FlowDefinitionUploadOutcome {
	switch outcome {
	case dexpb.FlowDefinitionUploadOutcome_FLOW_DEFINITION_UPLOAD_OUTCOME_CREATED:
		return FlowDefinitionUploadCreated
	case dexpb.FlowDefinitionUploadOutcome_FLOW_DEFINITION_UPLOAD_OUTCOME_REPLACED:
		return FlowDefinitionUploadReplaced
	case dexpb.FlowDefinitionUploadOutcome_FLOW_DEFINITION_UPLOAD_OUTCOME_UNCHANGED:
		return FlowDefinitionUploadUnchanged
	case dexpb.FlowDefinitionUploadOutcome_FLOW_DEFINITION_UPLOAD_OUTCOME_KEPT_EXISTING:
		return FlowDefinitionUploadKeptExisting
	default:
		return FlowDefinitionUploadOutcomeUnknown
	}
}

func mapFlowDefinitionUploadResults(results []*dexpb.FlowDefinitionUploadResult) []FlowDefinitionUploadResult {
	mapped := make([]FlowDefinitionUploadResult, 0, len(results))
	for _, result := range results {
		mapped = append(mapped, FlowDefinitionUploadResult{
			FlowType:          result.GetFlowType(),
			Outcome:           mapFlowDefinitionUploadOutcome(result.GetOutcome()),
			StoredDigest:      result.GetStoredDigest(),
			StoredGeneratedAt: result.GetStoredGeneratedAt().AsTime(),
		})
	}
	return mapped
}
