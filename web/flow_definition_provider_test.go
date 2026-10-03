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
	"testing"
)

const testReleaseID = "550e8400-e29b-41d4-a716-446655440000"
const secondTestReleaseID = "550e8400-e29b-41d4-a716-446655440001"
const thirdTestReleaseID = "550e8400-e29b-41d4-a716-446655440002"

func TestDirectoryFlowDefinitionProviderReloadsDirectDefinitions(t *testing.T) {
	directory := t.TempDir()
	writeFlowDefinitionTestFile(t, directory, "flow.json", validFlowDefinitionV2("FirstFlow", true))
	provider, err := NewDirectoryFlowDefinitionProvider(directory)
	if err != nil {
		t.Fatal(err)
	}
	first, err := provider.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	writeFlowDefinitionTestFile(t, directory, "flow.json", validFlowDefinitionV2("SecondFlow", true))
	second, err := provider.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if first.DefinitionRevision == second.DefinitionRevision {
		t.Fatal("definition revision did not change")
	}
	if _, found := second.V2Definitions["SecondFlow"]; !found {
		t.Fatalf("reloaded definitions = %+v", second.V2Definitions)
	}
}
