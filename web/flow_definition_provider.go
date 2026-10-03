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

	"github.com/superdurable/dex/web/api"
)

const (
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

// DirectoryFlowDefinitionProvider reloads directory definitions on every request.
type DirectoryFlowDefinitionProvider struct {
	directory string
}

// NewDirectoryFlowDefinitionProvider creates a dynamic directory provider.
func NewDirectoryFlowDefinitionProvider(directory string) (*DirectoryFlowDefinitionProvider, error) {
	directory = strings.TrimSpace(directory)
	if directory == "" {
		return &DirectoryFlowDefinitionProvider{}, nil
	}
	absoluteDirectory, err := filepath.Abs(directory)
	if err != nil {
		return nil, fmt.Errorf("resolve Flow rendering directory: %w", err)
	}
	return &DirectoryFlowDefinitionProvider{directory: absoluteDirectory}, nil
}

func (p *DirectoryFlowDefinitionProvider) Load(_ context.Context) (*FlowDefinitionSnapshot, error) {
	if p.directory == "" {
		return buildFlowDefinitionSnapshot(nil, "local", "", "")
	}
	files, err := readDirectoryDefinitionFiles(p.directory)
	if err != nil {
		return nil, classifyDefinitionReadError(err)
	}
	return buildFlowDefinitionSnapshot(files, "local", p.directory, "")
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
