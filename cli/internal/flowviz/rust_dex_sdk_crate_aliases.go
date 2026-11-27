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
	"slices"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

const rustDexSDKPackage = "dex-sdk"

// rustDexSDKCrateAliases returns the crate names under which the package that owns sourcePath
// depends on dex-sdk through a rename, such as dex for dex = { package = "dex-sdk" }.
// It reads the nearest Cargo.toml with a [package] table, and the workspace root only for a
// dependency with workspace = true. It returns nil when no owning package exists or none renames dex-sdk.
func rustDexSDKCrateAliases(sourcePath string) []string {
	packageDirectory, packageManifest := rustOwningPackageManifest(filepath.Dir(sourcePath))
	if packageManifest == nil {
		return nil
	}
	var workspaceDependencies map[string]any
	var aliases []string
	for name, dependency := range rustNormalDependencies(packageManifest) {
		table, isTable := dependency.(map[string]any)
		if !isTable {
			continue
		}
		if inherits, _ := table["workspace"].(bool); inherits {
			if workspaceDependencies == nil {
				workspaceDependencies = rustWorkspaceDependencies(packageDirectory)
			}
			inherited, isInheritedTable := workspaceDependencies[name].(map[string]any)
			if !isInheritedTable {
				continue
			}
			table = inherited
		}
		if packageName, _ := table["package"].(string); packageName == rustDexSDKPackage && name != rustDexSDKPackage {
			aliases = append(aliases, strings.ReplaceAll(name, "-", "_"))
		}
	}
	slices.Sort(aliases)
	return slices.Compact(aliases)
}

// rustOwningPackageManifest returns the directory and parsed manifest of the nearest Cargo.toml with a [package] table.
// It stops at the first manifest without one, such as a virtual workspace root, and returns a nil manifest.
func rustOwningPackageManifest(directory string) (string, map[string]any) {
	for {
		manifest, found := rustReadManifest(directory)
		if found {
			if _, hasPackage := manifest["package"].(map[string]any); hasPackage {
				return directory, manifest
			}
			return "", nil
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			return "", nil
		}
		directory = parent
	}
}

// rustNormalDependencies returns [dependencies] and every [target.<cfg>.dependencies] table, which build the package's code.
func rustNormalDependencies(manifest map[string]any) map[string]any {
	dependencies := make(map[string]any)
	if table, isTable := manifest["dependencies"].(map[string]any); isTable {
		for name, dependency := range table {
			dependencies[name] = dependency
		}
	}
	targets, _ := manifest["target"].(map[string]any)
	for _, target := range targets {
		targetTable, _ := target.(map[string]any)
		if table, isTable := targetTable["dependencies"].(map[string]any); isTable {
			for name, dependency := range table {
				dependencies[name] = dependency
			}
		}
	}
	return dependencies
}

// rustWorkspaceDependencies returns [workspace.dependencies] of the nearest manifest with a [workspace] table at or above directory.
func rustWorkspaceDependencies(directory string) map[string]any {
	for {
		if manifest, found := rustReadManifest(directory); found {
			if workspace, isWorkspace := manifest["workspace"].(map[string]any); isWorkspace {
				dependencies, _ := workspace["dependencies"].(map[string]any)
				return dependencies
			}
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			return nil
		}
		directory = parent
	}
}

// rustReadManifest parses directory/Cargo.toml; it reports false when the file is missing or is not valid TOML.
func rustReadManifest(directory string) (map[string]any, bool) {
	contents, err := os.ReadFile(filepath.Join(directory, "Cargo.toml"))
	if err != nil {
		return nil, false
	}
	var manifest map[string]any
	if err := toml.Unmarshal(contents, &manifest); err != nil {
		return nil, false
	}
	return manifest, true
}
