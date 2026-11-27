// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package flowviz

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

var (
	cargoSectionHeader         = regexp.MustCompile(`^\[([^\[\]]+)\]\s*(#.*)?$`)
	cargoInlineDependency      = regexp.MustCompile(`^([A-Za-z0-9_-]+)\s*=\s*\{(.*)\}`)
	cargoDexSDKPackage         = regexp.MustCompile(`(^|[\s,{])package\s*=\s*"dex-sdk"`)
	cargoDependencyTableHeader = regexp.MustCompile(`(^|\.)(dependencies|dev-dependencies|build-dependencies)\.([A-Za-z0-9_-]+)$`)
)

// rustDexSDKCrateAliases returns the crate names under which the Cargo manifests above sourcePath
// rename the dex-sdk package, such as dex for dex = { package = "dex-sdk" }.
// It reads manifests up to the first workspace root and returns nil when none renames dex-sdk.
func rustDexSDKCrateAliases(sourcePath string) []string {
	var aliases []string
	directory := filepath.Dir(sourcePath)
	for {
		manifest, err := os.ReadFile(filepath.Join(directory, "Cargo.toml"))
		if err == nil {
			isWorkspace := false
			aliases, isWorkspace = appendCargoDexSDKAliases(aliases, string(manifest))
			if isWorkspace {
				break
			}
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			break
		}
		directory = parent
	}
	slices.Sort(aliases)
	return slices.Compact(aliases)
}

// appendCargoDexSDKAliases appends the dex-sdk renames in one manifest and reports whether it is a workspace root.
func appendCargoDexSDKAliases(aliases []string, manifest string) ([]string, bool) {
	section := ""
	isWorkspace := false
	for _, line := range strings.Split(manifest, "\n") {
		line = strings.TrimSpace(line)
		if header := cargoSectionHeader.FindStringSubmatch(line); header != nil {
			section = strings.TrimSpace(header[1])
			isWorkspace = isWorkspace || section == "workspace"
			continue
		}
		if table := cargoDependencyTableHeader.FindStringSubmatch(section); table != nil {
			if cargoDexSDKPackage.MatchString(line) {
				aliases = append(aliases, strings.ReplaceAll(table[3], "-", "_"))
			}
			continue
		}
		if !strings.HasSuffix(section, "dependencies") {
			continue
		}
		if dependency := cargoInlineDependency.FindStringSubmatch(line); dependency != nil && cargoDexSDKPackage.MatchString(dependency[2]) {
			aliases = append(aliases, strings.ReplaceAll(dependency[1], "-", "_"))
		}
	}
	return aliases, isWorkspace
}
