// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

package index

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/gen/dexpb"
)

func TestLoadSchemaSupportsAllIndexTypes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flow-index.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`definitionVersion: 1
attributes:
  - {name: keyword, type: keyword}
  - {name: text, type: text}
  - {name: keywords, type: keyword_array}
  - {name: integer, type: int}
  - {name: double, type: double}
  - {name: boolean, type: bool}
  - {name: datetime, type: datetime}
  - name: embedding
    type: vector
    vectorDimensions: 3
    vectorMetric: cosine
`), 0o600))

	request, err := loadSchema(path)
	require.NoError(t, err)
	require.Equal(t, int32(1), request.GetDefinitionVersion())
	require.Equal(t, []dexpb.IndexType{
		dexpb.IndexType_INDEX_TYPE_KEYWORD,
		dexpb.IndexType_INDEX_TYPE_TEXT,
		dexpb.IndexType_INDEX_TYPE_KEYWORD_ARRAY,
		dexpb.IndexType_INDEX_TYPE_INT,
		dexpb.IndexType_INDEX_TYPE_DOUBLE,
		dexpb.IndexType_INDEX_TYPE_BOOL,
		dexpb.IndexType_INDEX_TYPE_DATETIME,
		dexpb.IndexType_INDEX_TYPE_VECTOR,
	}, schemaTypes(request.GetAttributes()))
	require.Equal(t, int32(3), request.GetAttributes()[7].GetVectorDimensions())
	require.Equal(t,
		dexpb.VectorDistanceMetric_VECTOR_DISTANCE_METRIC_COSINE,
		request.GetAttributes()[7].GetVectorDistanceMetric(),
	)
}

func TestLoadSchemaRejectsUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flow-index.yaml")
	require.NoError(t, os.WriteFile(path, []byte("unknown: true\n"), 0o600))

	_, err := loadSchema(path)
	require.ErrorContains(t, err, "field unknown not found")
}

func schemaTypes(fields []*dexpb.FlowIndexField) []dexpb.IndexType {
	types := make([]dexpb.IndexType, 0, len(fields))
	for _, field := range fields {
		types = append(types, field.GetType())
	}
	return types
}
