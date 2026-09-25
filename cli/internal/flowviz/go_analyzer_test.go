// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package flowviz

import (
	"testing"

	"golang.org/x/tools/go/packages"
)

func TestGoAnalyzerLoadsImportsForConnectorModuleVersions(t *testing.T) {
	if goPackagesLoadMode&packages.NeedImports == 0 {
		t.Fatal("Go analyzer must load imports to index Connector module versions")
	}
}
