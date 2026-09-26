// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package web

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/superdurable/dex/web/api"
)

const (
	flowDefinitionManifestName      = "active-manifest"
	flowDefinitionSourceInvalid     = "FLOW_DEFINITION_INVALID"
	flowDefinitionSourceUnavailable = "FLOW_DEFINITION_SOURCE_UNAVAILABLE"
)

// FlowDefinitionProvider loads one immutable, internally consistent definition snapshot.
type FlowDefinitionProvider interface {
	Load(context.Context) (*FlowDefinitionSnapshot, error)
}

// FlowDefinitionSnapshot is the validated catalog used throughout one HTTP request.
type FlowDefinitionSnapshot struct {
	Response           []byte
	V2Definitions      map[string]api.V2Definition
	DefinitionRevision string
	Source             string
	LoadedAt           time.Time
	DefinitionCount    int
}

type flowDefinitionSourceError struct {
	code string
	err  error
}

func (e *flowDefinitionSourceError) Error() string               { return e.err.Error() }
func (e *flowDefinitionSourceError) Unwrap() error               { return e.err }
func (e *flowDefinitionSourceError) DefinitionErrorCode() string { return e.code }

type flowDefinitionFile struct {
	path string
	data []byte
}

type flowDefinitionManifest struct {
	SchemaVersion   string `json:"schemaVersion"`
	ReleaseID       string `json:"releaseId"`
	BundlePrefix    string `json:"bundlePrefix"`
	BundleDigest    string `json:"bundleDigest"`
	DefinitionCount int    `json:"definitionCount"`
}

// DirectoryFlowDefinitionProvider reloads direct or manifest-backed definitions on every request.
type DirectoryFlowDefinitionProvider struct {
	directory    string
	readManifest func(string) ([]byte, error)
}

// NewDirectoryFlowDefinitionProvider creates a dynamic directory provider.
func NewDirectoryFlowDefinitionProvider(directory string) (*DirectoryFlowDefinitionProvider, error) {
	directory = strings.TrimSpace(directory)
	if directory == "" {
		return &DirectoryFlowDefinitionProvider{readManifest: os.ReadFile}, nil
	}
	absoluteDirectory, err := filepath.Abs(directory)
	if err != nil {
		return nil, fmt.Errorf("resolve Flow rendering directory: %w", err)
	}
	return &DirectoryFlowDefinitionProvider{directory: absoluteDirectory, readManifest: os.ReadFile}, nil
}

func (p *DirectoryFlowDefinitionProvider) Load(_ context.Context) (*FlowDefinitionSnapshot, error) {
	if p.directory == "" {
		return buildFlowDefinitionSnapshot(nil, "local", "", "")
	}
	manifestPath := filepath.Join(p.directory, flowDefinitionManifestName)
	manifestBytes, err := p.readManifest(manifestPath)
	if errors.Is(err, fs.ErrNotExist) {
		files, readErr := readDirectoryDefinitionFiles(p.directory)
		if readErr != nil {
			return nil, classifyDefinitionReadError(readErr)
		}
		return buildFlowDefinitionSnapshot(files, "local", p.directory, "")
	}
	if err != nil {
		return nil, unavailableDefinitionSource(fmt.Errorf("read Flow Definition manifest: %w", err))
	}
	for attempt := 0; attempt < 2; attempt++ {
		snapshot, loadErr := p.loadManifest(manifestBytes)
		if loadErr != nil {
			return nil, loadErr
		}
		currentManifest, readErr := p.readManifest(manifestPath)
		if readErr != nil {
			return nil, unavailableDefinitionSource(fmt.Errorf("verify Flow Definition manifest: %w", readErr))
		}
		if bytes.Equal(manifestBytes, currentManifest) {
			return snapshot, nil
		}
		manifestBytes = currentManifest
	}
	return nil, unavailableDefinitionSource(fmt.Errorf("Flow Definition manifest changed twice during one load"))
}

func (p *DirectoryFlowDefinitionProvider) loadManifest(data []byte) (*FlowDefinitionSnapshot, error) {
	manifest, relativePrefix, err := parseFlowDefinitionManifest(data, "")
	if err != nil {
		return nil, invalidDefinitionSource(err)
	}
	bundleDirectory := filepath.Join(p.directory, filepath.FromSlash(strings.TrimSuffix(relativePrefix, "/")))
	files, err := readDirectoryDefinitionFiles(bundleDirectory)
	if err != nil {
		return nil, classifyDefinitionReadError(fmt.Errorf("read Flow Definition release %s: %w", manifest.ReleaseID, err))
	}
	return buildManifestSnapshot(files, "local", p.directory, manifest)
}

func readDirectoryDefinitionFiles(directory string) ([]flowDefinitionFile, error) {
	info, err := os.Stat(directory)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("Flow rendering path is not a directory: %s", directory)
	}
	files := make([]flowDefinitionFile, 0)
	err = filepath.WalkDir(directory, func(filePath string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".json") {
			return nil
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return infoErr
		}
		if info.Size() > maxFlowDefinitionBytes {
			return invalidDefinitionSource(fmt.Errorf("Flow Definition Graph exceeds %d bytes: %s", maxFlowDefinitionBytes, filePath))
		}
		data, readErr := os.ReadFile(filePath)
		if readErr != nil {
			return readErr
		}
		relativePath, relativeErr := filepath.Rel(directory, filePath)
		if relativeErr != nil {
			return relativeErr
		}
		files = append(files, flowDefinitionFile{path: filepath.ToSlash(relativePath), data: data})
		return nil
	})
	return files, err
}

// FlowDefinitionObject is one object-store read result.
type FlowDefinitionObject struct {
	Data        []byte
	ETag        string
	NotModified bool
}

// FlowDefinitionObjectPage is one lexicographic object listing page.
type FlowDefinitionObjectPage struct {
	Keys              []string
	ContinuationToken string
}

// FlowDefinitionObjectStore is the narrow read-only object-store boundary used by Dex Web.
type FlowDefinitionObjectStore interface {
	Get(context.Context, string, string) (FlowDefinitionObject, error)
	List(context.Context, string, string) (FlowDefinitionObjectPage, error)
}

// S3FlowDefinitionProvider atomically follows an S3 active-manifest pointer.
type S3FlowDefinitionProvider struct {
	store  FlowDefinitionObjectStore
	prefix string
	mu     sync.Mutex
	etag   string
	cached *FlowDefinitionSnapshot
}

// NewS3FlowDefinitionProvider creates a manifest-backed S3 provider.
func NewS3FlowDefinitionProvider(store FlowDefinitionObjectStore, prefix string) (*S3FlowDefinitionProvider, error) {
	if store == nil {
		return nil, fmt.Errorf("S3 Flow Definition object store is required")
	}
	prefix = strings.Trim(strings.TrimSpace(prefix), "/")
	if prefix == "" || !safeObjectPath(prefix) {
		return nil, fmt.Errorf("S3 Flow Definition prefix must be a safe non-empty object prefix")
	}
	return &S3FlowDefinitionProvider{store: store, prefix: prefix}, nil
}

func (p *S3FlowDefinitionProvider) Load(ctx context.Context) (*FlowDefinitionSnapshot, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	manifestObject, err := p.store.Get(ctx, p.prefix+"/"+flowDefinitionManifestName, p.etag)
	if err != nil {
		return nil, unavailableDefinitionSource(fmt.Errorf("read S3 Flow Definition manifest: %w", err))
	}
	if manifestObject.NotModified {
		if p.cached == nil {
			return nil, unavailableDefinitionSource(fmt.Errorf("S3 manifest returned not modified without a cached snapshot"))
		}
		return p.cached, nil
	}
	manifest, bundlePrefix, err := parseFlowDefinitionManifest(manifestObject.Data, p.prefix)
	if err != nil {
		return nil, invalidDefinitionSource(err)
	}
	keys := make([]string, 0)
	token := ""
	for {
		page, listErr := p.store.List(ctx, bundlePrefix, token)
		if listErr != nil {
			return nil, unavailableDefinitionSource(fmt.Errorf("list S3 Flow Definition release %s: %w", manifest.ReleaseID, listErr))
		}
		for _, key := range page.Keys {
			if strings.HasSuffix(strings.ToLower(key), ".json") {
				keys = append(keys, key)
			}
		}
		if page.ContinuationToken == "" {
			break
		}
		token = page.ContinuationToken
	}
	sort.Strings(keys)
	files := make([]flowDefinitionFile, 0, len(keys))
	for _, key := range keys {
		if !strings.HasPrefix(key, bundlePrefix) || !safeObjectPath(key) {
			return nil, invalidDefinitionSource(fmt.Errorf("S3 Flow Definition key escapes the release prefix"))
		}
		object, getErr := p.store.Get(ctx, key, "")
		if getErr != nil {
			return nil, unavailableDefinitionSource(fmt.Errorf("read S3 Flow Definition object %q: %w", key, getErr))
		}
		if len(object.Data) > maxFlowDefinitionBytes {
			return nil, invalidDefinitionSource(fmt.Errorf("Flow Definition Graph exceeds %d bytes: %s", maxFlowDefinitionBytes, key))
		}
		files = append(files, flowDefinitionFile{path: strings.TrimPrefix(key, bundlePrefix), data: object.Data})
	}
	snapshot, err := buildManifestSnapshot(files, "blobstore", p.prefix, manifest)
	if err != nil {
		return nil, err
	}
	p.etag = manifestObject.ETag
	p.cached = snapshot
	return snapshot, nil
}

func parseFlowDefinitionManifest(data []byte, rootPrefix string) (flowDefinitionManifest, string, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var manifest flowDefinitionManifest
	if err := decoder.Decode(&manifest); err != nil {
		return manifest, "", fmt.Errorf("parse Flow Definition manifest: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return manifest, "", fmt.Errorf("parse Flow Definition manifest: trailing data")
	}
	if manifest.SchemaVersion != "1.0" {
		return manifest, "", fmt.Errorf("unsupported Flow Definition manifest schemaVersion %q", manifest.SchemaVersion)
	}
	if _, err := uuid.Parse(manifest.ReleaseID); err != nil {
		return manifest, "", fmt.Errorf("Flow Definition manifest releaseId must be a UUID")
	}
	if manifest.DefinitionCount < 0 {
		return manifest, "", fmt.Errorf("Flow Definition manifest definitionCount must be non-negative")
	}
	if !validSHA256Digest(manifest.BundleDigest) {
		return manifest, "", fmt.Errorf("Flow Definition manifest bundleDigest must be sha256:<64 lowercase hex>")
	}
	expectedRelative := "releases/" + manifest.ReleaseID + "/"
	rootPrefix = strings.Trim(rootPrefix, "/")
	expected := expectedRelative
	if rootPrefix != "" {
		expected = rootPrefix + "/" + expectedRelative
	}
	if manifest.BundlePrefix != expected || !safeObjectPath(strings.TrimSuffix(manifest.BundlePrefix, "/")) {
		return manifest, "", fmt.Errorf("Flow Definition manifest bundlePrefix must equal %q", expected)
	}
	return manifest, manifest.BundlePrefix, nil
}

func validSHA256Digest(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+sha256.Size*2 {
		return false
	}
	encoded := strings.TrimPrefix(value, "sha256:")
	_, err := hex.DecodeString(encoded)
	return err == nil && encoded == strings.ToLower(encoded)
}

func safeObjectPath(value string) bool {
	decoded, err := url.PathUnescape(value)
	return err == nil && decoded == value && value != "" && !strings.HasPrefix(value, "/") && path.Clean(value) == value &&
		value != "." && !strings.HasPrefix(value, "../") && !strings.Contains(value, "/../")
}

func buildManifestSnapshot(
	files []flowDefinitionFile,
	source string,
	location string,
	manifest flowDefinitionManifest,
) (*FlowDefinitionSnapshot, error) {
	digest := flowDefinitionDigest(files)
	if digest != manifest.BundleDigest {
		return nil, invalidDefinitionSource(fmt.Errorf(
			"Flow Definition release %s digest mismatch", manifest.ReleaseID,
		))
	}
	if len(files) != manifest.DefinitionCount {
		return nil, invalidDefinitionSource(fmt.Errorf(
			"Flow Definition release %s count mismatch", manifest.ReleaseID,
		))
	}
	return buildFlowDefinitionSnapshot(files, source, location, manifest.BundleDigest)
}

func buildFlowDefinitionSnapshot(
	files []flowDefinitionFile,
	source string,
	location string,
	revision string,
) (*FlowDefinitionSnapshot, error) {
	sort.Slice(files, func(left int, right int) bool { return files[left].path < files[right].path })
	if revision == "" {
		revision = flowDefinitionDigest(files)
	}
	catalog := flowDefinitionCatalog{
		Configured:         location != "",
		Source:             source,
		DefinitionRevision: revision,
		DefinitionCount:    len(files),
		Definitions:        make([]flowDefinition, 0, len(files)),
	}
	if source == "local" {
		catalog.Directory = location
	}
	v2Definitions := make(map[string]api.V2Definition)
	v2DefinitionFiles := make(map[string]string)
	for _, file := range files {
		if len(file.data) > maxFlowDefinitionBytes {
			return nil, invalidDefinitionSource(fmt.Errorf("Flow Definition Graph exceeds %d bytes: %s", maxFlowDefinitionBytes, file.path))
		}
		definition, err := flowDefinitionFromGraph(file.path, file.data)
		if err != nil {
			return nil, invalidDefinitionSource(err)
		}
		catalog.Definitions = append(catalog.Definitions, definition)
		if definition.SchemaVersion == "2.0" && definition.Valid {
			if existingFile, exists := v2DefinitionFiles[definition.FlowName]; exists {
				return nil, invalidDefinitionSource(fmt.Errorf(
					"multiple valid Flow Definition Graph 2.0 files define Flow type %q: %s and %s",
					definition.FlowName, existingFile, file.path,
				))
			}
			v2Definitions[definition.FlowName] = *definition.V2
			v2DefinitionFiles[definition.FlowName] = file.path
		}
	}
	response, err := json.Marshal(catalog)
	if err != nil {
		return nil, invalidDefinitionSource(fmt.Errorf("encode Flow Definition Graph catalog: %w", err))
	}
	return &FlowDefinitionSnapshot{
		Response:           response,
		V2Definitions:      v2Definitions,
		DefinitionRevision: revision,
		Source:             source,
		LoadedAt:           time.Now().UTC(),
		DefinitionCount:    len(files),
	}, nil
}

// The digest frames each sorted path and payload with unsigned 64-bit big-endian lengths.
func flowDefinitionDigest(files []flowDefinitionFile) string {
	ordered := append([]flowDefinitionFile(nil), files...)
	sort.Slice(ordered, func(left int, right int) bool { return ordered[left].path < ordered[right].path })
	hash := sha256.New()
	var length [8]byte
	for _, file := range ordered {
		binary.BigEndian.PutUint64(length[:], uint64(len(file.path)))
		_, _ = hash.Write(length[:])
		_, _ = hash.Write([]byte(file.path))
		binary.BigEndian.PutUint64(length[:], uint64(len(file.data)))
		_, _ = hash.Write(length[:])
		_, _ = hash.Write(file.data)
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
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
