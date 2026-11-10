// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package connectorsmcp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestCompiledConnectorsMatchAgentManifests guards module bumps: manifests must match compiled operations.
func TestCompiledConnectorsMatchAgentManifests(t *testing.T) {
	directory := t.TempDir()
	fixtures := allConnectorFixtures("")
	writeConnectionsFile(t, directory, fixtures)
	project, err := loadLocalProject(context.Background(), filepath.Join(directory, connectionsFileName))
	require.NoError(t, err)
	catalogs, err := loadCompiledConnectorCatalogs()
	require.NoError(t, err)
	require.Len(t, catalogs, len(fixtures))
	for index, catalog := range catalogs {
		fixture := fixtures[index]
		t.Run(catalog.connector.connectorID, func(t *testing.T) {
			require.Equal(t, fixture.ConnectorID, catalog.connector.connectorID)
			opened, err := catalog.connector.openConnection(project, fixture.ConnectionName)
			require.NoError(t, err)
			require.Equal(t, fixture.Provider, opened.reference.Provider)
			require.Equal(t, fixture.ConnectionName, opened.reference.Name)
			runners := make(map[string]operationRunner)
			for _, runner := range opened.runners {
				require.Equal(t, catalog.connector.connectorID, runner.operationRef().ConnectorID)
				runners[runner.operationRef().OperationID] = runner
			}
			require.NotEmpty(t, catalog.operations)
			for operationID, operation := range catalog.operations {
				runner, found := runners[operationID]
				require.True(t, found, "manifest operation %s has no compiled operation", operationID)
				require.NoError(t, checkManifestMatchesOperation(operation, runner))
				requireSchemaMatchesInputType(t, operation.manifest.InputSchema, runner.inputType())
				require.LessOrEqual(t, len(toolNameOf(catalog.connector.connectorID, operationID)), maximumToolNameLength)
			}
		})
	}
}

func TestToolNameMatchesAgentCoreConvention(t *testing.T) {
	require.Equal(t, "slack_post_thread_reply", toolNameOf("slack", "postThreadReply"))
	require.Equal(t, "hacker-news-daily_list_story_i_ds", toolNameOf("hacker-news-daily", "listStoryIDs"))
	require.Equal(t, "reddit_get_post", toolNameOf("reddit", "getPost"))
}

// requireSchemaMatchesInputType checks property names, required versus omitempty, and JSON types.
func requireSchemaMatchesInputType(t *testing.T, rawSchema json.RawMessage, inputType reflect.Type) {
	t.Helper()
	var schema struct {
		Properties map[string]struct {
			Type any `json:"type"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	require.NoError(t, json.Unmarshal(rawSchema, &schema))
	require.Equal(t, reflect.Struct, inputType.Kind())
	fieldNames := make(map[string]bool)
	for index := range inputType.NumField() {
		field := inputType.Field(index)
		if !field.IsExported() {
			continue
		}
		name, options, _ := strings.Cut(field.Tag.Get("json"), ",")
		if name == "-" {
			continue
		}
		if name == "" {
			name = field.Name
		}
		fieldNames[name] = true
		property, found := schema.Properties[name]
		require.True(t, found, "%s.%s has no schema property", inputType.Name(), name)
		isOmitEmpty := slices.Contains(strings.Split(options, ","), "omitempty")
		require.Equal(t, !isOmitEmpty, slices.Contains(schema.Required, name), "%s.%s required", inputType.Name(), name)
		require.Contains(t, schemaTypes(property.Type), jsonTypeOf(field.Type), "%s.%s type", inputType.Name(), name)
	}
	for name := range schema.Properties {
		require.True(t, fieldNames[name], "schema property %s is not a field of %s", name, inputType.Name())
	}
}

func schemaTypes(value any) []string {
	switch typed := value.(type) {
	case string:
		return []string{typed}
	case []any:
		var types []string
		for _, item := range typed {
			if name, isString := item.(string); isString {
				types = append(types, name)
			}
		}
		return types
	default:
		return nil
	}
}

func jsonTypeOf(fieldType reflect.Type) string {
	if fieldType == reflect.TypeFor[time.Time]() || fieldType == reflect.TypeFor[*time.Time]() {
		return "string"
	}
	switch fieldType.Kind() {
	case reflect.Pointer:
		return jsonTypeOf(fieldType.Elem())
	case reflect.String:
		return "string"
	case reflect.Bool:
		return "boolean"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "integer"
	case reflect.Float32, reflect.Float64:
		return "number"
	case reflect.Slice, reflect.Array:
		return "array"
	default:
		return "object"
	}
}
