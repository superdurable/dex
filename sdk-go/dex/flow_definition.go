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
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

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
	// The SDK truncates it to whole milliseconds before upload, as every Dex SDK does.
	// The server rejects a time more than 24 hours in its future.
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
// Each path names a file or a directory. A missing or unreadable path is an error.
//
// A named file must be a valid FDG 2.x document whatever its extension: a UTF-8 JSON
// object whose schemaVersion starts with "2.", whose flow.name is a non-empty string,
// and whose generatedAt, when present, is a valid RFC 3339 time. Otherwise it is an error.
//
// A directory is read recursively, and only files whose extension is ".json" in any
// letter case are considered. A considered file that is not an FDG 2.x document, such
// as package.json or an FDG 1.x file, is skipped and logged at info level with the reason.
// An FDG 2.x file with an invalid generatedAt is an error.
//
// flow.name becomes the Flow type. GeneratedAt comes from the top-level generatedAt
// when present, otherwise from the file modification time, truncated to whole
// milliseconds. generatedAt must use the form 2026-11-01T00:00:00Z: a "T" or "t"
// separator, optional fractional seconds, and "Z", "z", or a ±HH:MM offset.
// An absolute source.path is rewritten to a path relative to the working directory when
// it lies under it, otherwise to its base name. A relative source.path stays as written.
// Only that string changes; the other bytes stay as written.
//
// The same file reached through two paths is read once. Two files with the same
// Flow type are an error. Documents are returned sorted by Flow type.
// WorkerOptions.FlowDefinitionPaths and dexcli flow-definition upload use the same rules.
// logger receives the info logs of skipped files; nil uses slog.Default.
//
//	documents, err := dex.ReadFlowDefinitionFiles([]string{"flow-definitions"}, nil)
//	if err != nil {
//		return err
//	}
//	results, err := client.UploadFlowDefinitions(ctx, documents, false)
//
// It returns an error that names the path for a missing or unreadable path, an invalid
// named file, an invalid generatedAt, or a duplicate Flow type.
//
// Since 1.45.0.
func ReadFlowDefinitionFiles(paths []string, logger Logger) ([]FlowDefinitionDocument, error) {
	logger = resolveLogger(logger, nil)
	candidates, err := collectFlowDefinitionFiles(paths)
	if err != nil {
		return nil, err
	}
	workingDirectory := currentWorkingDirectory()
	documents := make([]FlowDefinitionDocument, 0, len(candidates))
	sourceFileByFlowType := make(map[string]string, len(candidates))
	for _, candidate := range candidates {
		document, isFlowDefinition, err := readFlowDefinitionFile(candidate, workingDirectory)
		if err != nil {
			return nil, err
		}
		if !isFlowDefinition {
			logger.Info("dex: skip a file that is not a Flow Definition Graph 2.x document",
				"path", candidate.path, "reason", document.skipReason)
			continue
		}
		if previous, exists := sourceFileByFlowType[document.FlowType]; exists {
			return nil, fmt.Errorf(
				"dex: Flow definition files %q and %q both describe Flow type %q",
				previous, candidate.path, document.FlowType,
			)
		}
		sourceFileByFlowType[document.FlowType] = candidate.path
		documents = append(documents, document.FlowDefinitionDocument)
	}
	sort.Slice(documents, func(i, j int) bool { return documents[i].FlowType < documents[j].FlowType })
	return documents, nil
}

type flowDefinitionFile struct {
	path string
	// isFromDirectory marks a file found by walking a directory; such files may be skipped.
	isFromDirectory bool
}

// collectFlowDefinitionFiles expands directories and returns unique absolute file paths in walk order.
func collectFlowDefinitionFiles(paths []string) ([]flowDefinitionFile, error) {
	files := make([]flowDefinitionFile, 0, len(paths))
	isSeen := make(map[string]bool, len(paths))
	addFile := func(file flowDefinitionFile) {
		if !isSeen[file.path] {
			isSeen[file.path] = true
			files = append(files, file)
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
			addFile(flowDefinitionFile{path: absolutePath})
			continue
		}
		err = filepath.WalkDir(absolutePath, func(walkedPath string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.Type().IsRegular() && strings.EqualFold(filepath.Ext(entry.Name()), ".json") {
				addFile(flowDefinitionFile{path: walkedPath, isFromDirectory: true})
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("dex: read Flow definition directory %q: %w", path, err)
		}
	}
	return files, nil
}

type readFlowDefinition struct {
	FlowDefinitionDocument
	skipReason string
}

// readFlowDefinitionFile reports false, with a skip reason, for a directory file that is not an FDG 2.x document.
func readFlowDefinitionFile(file flowDefinitionFile, workingDirectory string) (readFlowDefinition, bool, error) {
	content, err := os.ReadFile(file.path)
	if err != nil {
		return readFlowDefinition{}, false, fmt.Errorf("dex: read Flow definition file %q: %w", file.path, err)
	}
	header, err := parseFlowDefinitionHeader(content)
	if err != nil {
		var notFlowDefinition *notFlowDefinitionError
		if file.isFromDirectory && errors.As(err, &notFlowDefinition) {
			return readFlowDefinition{skipReason: notFlowDefinition.reason}, false, nil
		}
		return readFlowDefinition{}, false, fmt.Errorf("dex: Flow definition file %q: %w", file.path, err)
	}
	generatedAt := header.generatedAt
	if generatedAt.IsZero() {
		info, err := os.Stat(file.path)
		if err != nil {
			return readFlowDefinition{}, false, fmt.Errorf("dex: read Flow definition file %q: %w", file.path, err)
		}
		generatedAt = info.ModTime()
	}
	return readFlowDefinition{FlowDefinitionDocument: FlowDefinitionDocument{
		FlowType:    header.flowType,
		Content:     rewriteFlowDefinitionSourcePath(content, workingDirectory),
		GeneratedAt: generatedAt.Truncate(time.Millisecond),
	}}, true, nil
}

type flowDefinitionHeader struct {
	flowType    string
	generatedAt time.Time
}

// notFlowDefinitionError marks content that is not an FDG 2.x document, as opposed to an invalid one.
type notFlowDefinitionError struct {
	reason string
}

func (err *notFlowDefinitionError) Error() string { return err.reason }

var strictRFC3339Pattern = regexp.MustCompile(
	`^[0-9]{4}-[0-9]{2}-[0-9]{2}[Tt]([0-9]{2}):([0-9]{2}):([0-9]{2})(\.[0-9]+)?([Zz]|[+-]([0-9]{2}):([0-9]{2}))$`,
)

// parseFlowDefinitionHeader returns *notFlowDefinitionError when content is not an FDG 2.x document.
func parseFlowDefinitionHeader(content []byte) (flowDefinitionHeader, error) {
	if !utf8.Valid(content) {
		return flowDefinitionHeader{}, &notFlowDefinitionError{reason: "content is not valid UTF-8"}
	}
	trimmed := bytes.TrimLeft(content, " \t\r\n")
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return flowDefinitionHeader{}, &notFlowDefinitionError{reason: "content is not a JSON object"}
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(content, &fields); err != nil {
		return flowDefinitionHeader{}, &notFlowDefinitionError{reason: "content is not a JSON object: " + err.Error()}
	}
	var schemaVersion string
	if json.Unmarshal(fields["schemaVersion"], &schemaVersion) != nil ||
		!strings.HasPrefix(schemaVersion, flowDefinitionSchemaVersionPrefix) {
		return flowDefinitionHeader{}, &notFlowDefinitionError{
			reason: `schemaVersion must start with "2."; regenerate with dexcli visualize --schema-version 2.0`,
		}
	}
	var flow struct {
		Name json.RawMessage `json:"name"`
	}
	var flowName string
	if json.Unmarshal(fields["flow"], &flow) != nil || json.Unmarshal(flow.Name, &flowName) != nil || flowName == "" {
		return flowDefinitionHeader{}, &notFlowDefinitionError{reason: "flow.name must be a non-empty string"}
	}
	header := flowDefinitionHeader{flowType: flowName}
	if rawGeneratedAt, isPresent := fields["generatedAt"]; isPresent {
		var generatedAtText string
		if err := json.Unmarshal(rawGeneratedAt, &generatedAtText); err != nil {
			return flowDefinitionHeader{}, errors.New("generatedAt must be an RFC 3339 time string")
		}
		generatedAt, err := parseStrictRFC3339(generatedAtText)
		if err != nil {
			return flowDefinitionHeader{}, err
		}
		header.generatedAt = generatedAt
	}
	return header, nil
}

func isTwoDigitFieldAtMost(field string, maximum int) bool {
	return int(field[0]-'0')*10+int(field[1]-'0') <= maximum
}

// parseStrictRFC3339 accepts only the RFC 3339 forms that every Dex SDK accepts.
func parseStrictRFC3339(text string) (time.Time, error) {
	match := strictRFC3339Pattern.FindStringSubmatch(text)
	if match == nil || !isTwoDigitFieldAtMost(match[1], 23) || !isTwoDigitFieldAtMost(match[2], 59) ||
		!isTwoDigitFieldAtMost(match[3], 59) {
		return time.Time{}, fmt.Errorf("generatedAt %q must be an RFC 3339 time such as 2026-11-01T00:00:00Z", text)
	}
	if match[6] != "" && (!isTwoDigitFieldAtMost(match[6], 23) || !isTwoDigitFieldAtMost(match[7], 59)) {
		return time.Time{}, fmt.Errorf("generatedAt %q has an offset out of range", text)
	}
	canonical := strings.ToUpper(text)
	parsed, err := time.Parse(time.RFC3339Nano, canonical)
	if err != nil {
		return time.Time{}, fmt.Errorf("generatedAt %q must be an RFC 3339 time: %w", text, err)
	}
	return parsed, nil
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
			GeneratedAt: timestamppb.New(document.GeneratedAt.Truncate(time.Millisecond)),
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
	if !isFound {
		return content
	}
	replacement, isRewritten := relativeFlowDefinitionSourcePath(location.value, workingDirectory)
	if !isRewritten {
		return content
	}
	encoded := encodeMinimalJSONString(replacement)
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

// findFlowDefinitionSourcePath returns the byte range of the top-level source.path string value.
// It reports false unless the top level has exactly one "source" object with exactly one string "path".
func findFlowDefinitionSourcePath(content []byte) (flowDefinitionSourcePathLocation, bool) {
	decoder := json.NewDecoder(bytes.NewReader(content))
	if !isNextJSONDelimiter(decoder, '{') {
		return flowDefinitionSourcePathLocation{}, false
	}
	var location flowDefinitionSourcePathLocation
	hasSource := false
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
		if hasSource {
			return flowDefinitionSourcePathLocation{}, false
		}
		hasSource = true
		sourceLocation, isFound := findSourceObjectPath(decoder, content)
		if !isFound {
			return flowDefinitionSourcePathLocation{}, false
		}
		location = sourceLocation
	}
	return location, hasSource
}

// findSourceObjectPath reads the source object and reports false unless it has exactly one string "path".
func findSourceObjectPath(decoder *json.Decoder, content []byte) (flowDefinitionSourcePathLocation, bool) {
	if !isNextJSONDelimiter(decoder, '{') {
		return flowDefinitionSourcePathLocation{}, false
	}
	var location flowDefinitionSourcePathLocation
	hasPath := false
	for decoder.More() {
		key, isKey := nextJSONObjectKey(decoder)
		if !isKey {
			return flowDefinitionSourcePathLocation{}, false
		}
		if key != "path" {
			if !skipJSONValue(decoder) {
				return flowDefinitionSourcePathLocation{}, false
			}
			continue
		}
		if hasPath {
			return flowDefinitionSourcePathLocation{}, false
		}
		hasPath = true
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
		location = flowDefinitionSourcePathLocation{start: valueSearchStart + quoteOffset, end: end, value: value}
	}
	if !isNextJSONDelimiter(decoder, '}') {
		return flowDefinitionSourcePathLocation{}, false
	}
	return location, hasPath
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

var windowsDrivePathPattern = regexp.MustCompile(`^[A-Za-z]:[\\/]`)

// relativeFlowDefinitionSourcePath applies the cross-SDK rewrite rule; it reports false for a relative path.
// An empty workingDirectory means the working directory is unavailable, which yields the base name.
func relativeFlowDefinitionSourcePath(sourcePath string, workingDirectory string) (string, bool) {
	if !isCrossPlatformAbsolutePath(sourcePath) {
		return "", false
	}
	sourceRoot, sourceSegments := splitLexicalPath(sourcePath)
	if workingDirectory != "" {
		directoryRoot, directorySegments := splitLexicalPath(workingDirectory)
		if isLexicalPathUnder(sourceRoot, sourceSegments, directoryRoot, directorySegments) {
			return strings.Join(sourceSegments[len(directorySegments):], "/"), true
		}
	}
	if len(sourceSegments) == 0 {
		return "", true
	}
	return sourceSegments[len(sourceSegments)-1], true
}

func isCrossPlatformAbsolutePath(path string) bool {
	return strings.HasPrefix(path, "/") || strings.HasPrefix(path, `\\`) || windowsDrivePathPattern.MatchString(path)
}

// splitLexicalPath returns the root ("/", "//" for UNC, a drive such as "C:", or empty) and normalized segments.
func splitLexicalPath(path string) (string, []string) {
	root := ""
	remainder := path
	switch {
	case strings.HasPrefix(path, `\\`):
		root, remainder = "//", path[2:]
	case windowsDrivePathPattern.MatchString(path):
		root, remainder = path[:2], path[3:]
	case strings.HasPrefix(path, "/"):
		root, remainder = "/", path[1:]
	}
	segments := []string{}
	for _, segment := range strings.FieldsFunc(remainder, func(character rune) bool {
		return character == '/' || character == '\\'
	}) {
		switch segment {
		case ".":
		case "..":
			if len(segments) > 0 {
				segments = segments[:len(segments)-1]
			}
		default:
			segments = append(segments, segment)
		}
	}
	return root, segments
}

// isLexicalPathUnder reports whether the directory is a proper segment-wise prefix of the path.
func isLexicalPathUnder(pathRoot string, pathSegments []string, directoryRoot string, directorySegments []string) bool {
	if !strings.EqualFold(pathRoot, directoryRoot) || len(pathSegments) <= len(directorySegments) {
		return false
	}
	for index, segment := range directorySegments {
		if pathSegments[index] != segment {
			return false
		}
	}
	return true
}

// encodeMinimalJSONString escapes only the quote, the backslash, and control characters, as every Dex SDK does.
func encodeMinimalJSONString(value string) []byte {
	encoded := make([]byte, 0, len(value)+2)
	encoded = append(encoded, '"')
	for index := 0; index < len(value); index++ {
		character := value[index]
		switch character {
		case '"':
			encoded = append(encoded, '\\', '"')
		case '\\':
			encoded = append(encoded, '\\', '\\')
		case '\b':
			encoded = append(encoded, '\\', 'b')
		case '\f':
			encoded = append(encoded, '\\', 'f')
		case '\n':
			encoded = append(encoded, '\\', 'n')
		case '\r':
			encoded = append(encoded, '\\', 'r')
		case '\t':
			encoded = append(encoded, '\\', 't')
		default:
			if character < 0x20 {
				encoded = append(encoded, fmt.Sprintf(`\u%04x`, character)...)
			} else {
				encoded = append(encoded, character)
			}
		}
	}
	return append(encoded, '"')
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
