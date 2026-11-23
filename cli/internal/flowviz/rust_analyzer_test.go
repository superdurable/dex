// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package flowviz

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"
)

func TestRustAnalyzerGolden(t *testing.T) {
	requireRustAnalyzerCargo(t)
	source := filepath.Join("testdata", "rust", "workflow.rs")
	graph, err := Analyze(t.Context(), source, AnalyzeOptions{FlowType: "Delivery"})
	require.NoError(t, err)
	require.True(t, graph.Valid, "%+v", graph.Diagnostics)
	require.Empty(t, graph.Diagnostics)
	graph.Source.Path = filepath.Base(source)
	actual, err := MarshalJSON(graph)
	require.NoError(t, err)
	golden := filepath.Join(filepath.Dir(source), "workflow.golden.json")
	if os.Getenv("UPDATE_SCRIPT_GOLDENS") == "1" {
		require.NoError(t, os.WriteFile(golden, actual, 0o644))
	}
	expected, err := os.ReadFile(golden)
	require.NoError(t, err)
	require.Equal(t, string(expected), string(actual))
}

func TestRustAnalyzerDynamicShapes(t *testing.T) {
	requireRustAnalyzerCargo(t)
	tests := []struct {
		source string
		codes  []string
	}{
		{"dynamic_target.rs", []string{"dynamic_step_target"}},
		{"hidden_handlers.rs", []string{"hidden_dex_decision", "hidden_dex_wait"}},
	}
	for _, test := range tests {
		t.Run(test.source, func(t *testing.T) {
			graph, err := Analyze(t.Context(), filepath.Join("testdata", "rust", "shapes", test.source), AnalyzeOptions{})
			require.NoError(t, err)
			require.False(t, graph.Valid)
			require.Equal(t, test.codes, diagnosticCodes(graph))
		})
	}
}

func TestRustAnalyzerParseFailure(t *testing.T) {
	requireRustAnalyzerCargo(t)
	graph, err := analyzeRust(t.Context(), "broken.rs", []byte("struct Example {"), "", "")
	require.NoError(t, err)
	require.False(t, graph.Valid)
	require.Equal(t, []string{"rust_parse_failed"}, diagnosticCodes(graph))
	require.Positive(t, graph.Diagnostics[0].Span.StartColumn)
}

func TestRustAnalyzerCacheBuildsOnceAndInvalidatesOnSourceChange(t *testing.T) {
	cache := t.TempDir()
	cargo := filepath.Join(cache, "cargo")
	require.NoError(t, os.WriteFile(cargo, []byte(fakeRustAnalyzerCargo), 0o700))
	crate := fstest.MapFS{
		"rust_analyzer/Cargo.toml":  {Data: []byte("manifest")},
		"rust_analyzer/Cargo.lock":  {Data: []byte("lock")},
		"rust_analyzer/src/main.rs": {Data: []byte("first version")},
	}
	var group sync.WaitGroup
	var binaries [2]string
	var buildErrors [2]error
	for index := range binaries {
		group.Go(func() {
			binaries[index], buildErrors[index] = resolveRustAnalyzerBinary(t.Context(), cargo, cache, crate)
		})
	}
	group.Wait()
	for _, err := range buildErrors {
		require.NoError(t, err)
	}
	require.Equal(t, binaries[0], binaries[1])
	reused, err := resolveRustAnalyzerBinary(t.Context(), cargo, cache, crate)
	require.NoError(t, err)
	require.Equal(t, binaries[0], reused)
	requireRustAnalyzerBuildCount(t, cache, 1)

	crate["rust_analyzer/src/main.rs"].Data = []byte("second version")
	updated, err := resolveRustAnalyzerBinary(t.Context(), cargo, cache, crate)
	require.NoError(t, err)
	require.NotEqual(t, filepath.Dir(reused), filepath.Dir(updated))
	requireRustAnalyzerBuildCount(t, cache, 2)
}

func TestRustAnalyzerBuildFailureIncludesLogTail(t *testing.T) {
	cache := t.TempDir()
	cargo := filepath.Join(cache, "cargo")
	require.NoError(t, os.WriteFile(cargo, []byte("#!/bin/sh\nif [ \"$1\" = --version ]; then echo cargo-test; exit 0; fi\necho build-sentinel >&2\nexit 1\n"), 0o700))
	_, err := resolveRustAnalyzerBinary(context.Background(), cargo, cache, rustAnalyzerCrate)
	require.ErrorContains(t, err, "build-sentinel")
	require.ErrorContains(t, err, "build.log")
}

func requireRustAnalyzerBuildCount(t *testing.T, directory string, expected int) {
	t.Helper()
	count, err := os.ReadFile(filepath.Join(directory, "build-count"))
	require.NoError(t, err)
	require.Equal(t, expected, strings.Count(string(count), "\n"))
}

func requireRustAnalyzerCargo(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("cargo"); err != nil {
		t.Skip("Rust visualization requires Cargo on PATH")
	}
}

const fakeRustAnalyzerCargo = `#!/bin/sh
set -eu
if [ "$1" = --version ]; then
    echo cargo-test
    exit 0
fi
test "$*" = "build --release --locked --target-dir $PWD/target"
echo build >> "$(dirname "$0")/build-count"
mkdir -p target/release
touch target/release/dex-flowviz-rust
chmod 700 target/release/dex-flowviz-rust
`
