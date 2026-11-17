// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package embedded

import (
	"runtime/debug"
	"testing"
)

func TestDexWebModuleVersionOf(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		modules []*debug.Module
		want    string
	}{
		{"published embedded module", []*debug.Module{
			{Path: "google.golang.org/grpc", Version: "v1.70.0"},
			{Path: "github.com/superdurable/dex/web/embedded", Version: "v1.45.11"},
		}, "dex-web 1.45.11"},
		{"web module", []*debug.Module{{Path: "github.com/superdurable/dex/web", Version: "v1.45.11"}}, "dex-web 1.45.11"},
		{"replaced by a released version", []*debug.Module{{
			Path: "github.com/superdurable/dex/web/embedded", Version: "v1.45.10",
			Replace: &debug.Module{Path: "github.com/superdurable/dex/web/embedded", Version: "v1.45.11"},
		}}, "dex-web 1.45.11"},
		{"local replace", []*debug.Module{{
			Path: "github.com/superdurable/dex/web", Version: "v0.0.0",
			Replace: &debug.Module{Path: "../web"},
		}}, ""},
		{"devel build", []*debug.Module{{Path: "github.com/superdurable/dex/web", Version: "(devel)"}}, ""},
		{"absent", []*debug.Module{{Path: "google.golang.org/grpc", Version: "v1.70.0"}}, ""},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := dexWebModuleVersionOf(testCase.modules); got != testCase.want {
				t.Fatalf("Dex Web version = %q, want %q", got, testCase.want)
			}
		})
	}
}
