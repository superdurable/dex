// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package flowviz

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStartInputEnumValuesIgnoresFileLoadOrder(t *testing.T) {
	enumSourceFiles := map[string]string{
		"/workspace/enums/status_open.go": `package enums

type Status string

const (
	StatusPending Status = "pending"
	StatusActive  Status = "active"
)
`,
		"/workspace/enums/status_terminal.go": `package enums

const (
	StatusClosed   Status = "closed"
	StatusArchived Status = "archived"
)
`,
	}
	expectedValues := []StartInputEnumValue{
		{Name: "StatusPending", Value: "pending"},
		{Name: "StatusActive", Value: "active"},
		{Name: "StatusClosed", Value: "closed"},
		{Name: "StatusArchived", Value: "archived"},
	}
	for _, loadOrder := range [][]string{
		{"/workspace/enums/status_open.go", "/workspace/enums/status_terminal.go"},
		{"/workspace/enums/status_terminal.go", "/workspace/enums/status_open.go"},
	} {
		fileSet := token.NewFileSet()
		parsedFiles := make([]*ast.File, 0, len(loadOrder))
		for _, filename := range loadOrder {
			parsedFile, parseErr := parser.ParseFile(fileSet, filename, enumSourceFiles[filename], 0)
			require.NoError(t, parseErr)
			parsedFiles = append(parsedFiles, parsedFile)
		}
		checkedPackage, checkErr := (&types.Config{}).Check("example.com/enums", fileSet, parsedFiles, nil)
		require.NoError(t, checkErr)
		statusType, isNamed := checkedPackage.Scope().Lookup("Status").Type().(*types.Named)
		require.True(t, isNamed)

		require.Equal(t, expectedValues, startInputEnumValues(fileSet, statusType), "load order %v", loadOrder)
	}
}
