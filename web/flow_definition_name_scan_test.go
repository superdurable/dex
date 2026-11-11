// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package web

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestScanTopLevelFlowNameSkipsValuesWithoutKeepingThem(t *testing.T) {
	deep := strings.Repeat("[", 100000) + strings.Repeat("]", 100000)
	tricky := `"a \" } ] { [ \\" , ""`
	tests := map[string]struct {
		document  string
		wantName  string
		wantError string
	}{
		"flow after a large string": {document: `{"nodes":"` + strings.Repeat("x", 20<<20) + `","flow":{"name":"Big"}}`, wantName: "Big"},
		"flow after deep nesting":   {document: `{"nodes":` + deep + `,"flow":{"name":"Deep"}}`, wantName: "Deep"},
		"strings with brackets":     {document: `{"edges":[{"label":` + tricky + `}],"flow":{"name":"Tricky","span":{"startLine":1}}}`, wantName: "Tricky"},
		"scalar values":             {document: `{"valid":true,"count":-1.5e3,"none":null,"flow":{"name":" Spaced "}}`, wantName: "Spaced"},
		"escaped key":               {document: `{"flow":{"name":"Escaped"}}`, wantName: "Escaped"},
		"last flow wins":            {document: `{"flow":{"name":"First"},"x":1,"flow":{"name":"Second"}}`, wantName: "Second"},
		"long keys":                 {document: `{"` + strings.Repeat("k", 200) + `":1,"flow":{"name":"Long"}}`, wantName: "Long"},
		"truncated after flow":      {document: `{"flow":{"name":"Kept"},"nodes":["unfinished`, wantName: "Kept"},
		"truncated before flow":     {document: `{"nodes":["unfinished`, wantError: "no flow object within the first"},
		"no flow":                   {document: `{"nodes":[]}`, wantError: "has no flow object"},
		"not an object":             {document: `["flow"]`, wantError: "not a JSON object"},
		"oversized flow object":     {document: `{"flow":{"name":"N","pad":"` + strings.Repeat("p", maxFlowObjectBytes) + `"}}`, wantError: "larger than"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			flowName, err := scanTopLevelFlowName(strings.NewReader(test.document))
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("name = %q, err = %v, want error %q", flowName, err, test.wantError)
				}
				return
			}
			if err != nil || flowName != test.wantName {
				t.Fatalf("name = %q, err = %v, want %q", flowName, err, test.wantName)
			}
		})
	}
}

func TestOversizedFlowNamesAreCachedByPathSizeAndTime(t *testing.T) {
	cache := &flowNameScanCache{results: make(map[flowNameScanKey]flowNameScanResult)}
	path := filepath.Join(t.TempDir(), "huge.json")
	write := func(name string, modified time.Time) os.FileInfo {
		t.Helper()
		if err := os.WriteFile(path, []byte(`{"flow":{"name":"`+name+`"}}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, modified, modified); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		return info
	}
	modified := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	budget := &flowNameScanBudget{remainingBytes: maxFlowNameScanBytesPerLoad}
	if name, err := cache.scan(path, write("AlphaFlow", modified), budget); err != nil || name != "AlphaFlow" {
		t.Fatalf("first scan = %q, %v", name, err)
	}
	// The same size and time means the same file to the cache, so no budget is needed to answer again.
	if name, err := cache.scan(path, write("OtherFlow", modified), &flowNameScanBudget{}); err != nil || name != "AlphaFlow" {
		t.Fatalf("cached scan = %q, %v", name, err)
	}
	changed := write("OtherFlow", modified.Add(time.Second))
	if _, err := cache.scan(path, changed, &flowNameScanBudget{remainingBytes: 1}); !errors.Is(err, errFlowNameScanBudgetExhausted) {
		t.Fatalf("scan beyond the budget = %v", err)
	}
	if name, err := cache.scan(path, changed, budget); err != nil || name != "OtherFlow" {
		t.Fatalf("rescan = %q, %v", name, err)
	}
}
