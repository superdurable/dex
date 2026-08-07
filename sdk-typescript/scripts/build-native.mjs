#!/usr/bin/env node
// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import { copyFileSync, mkdirSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";

const scriptDirectory = dirname(fileURLToPath(import.meta.url));
const packageRoot = join(scriptDirectory, "..");
const repositoryRoot = join(packageRoot, "..");
const rustRoot = join(repositoryRoot, "sdk-rust");

const platform = nativePlatform();
const libraryName = nativeLibraryName();
const stagedName = "dex_blob_cache_node.node";
const cargoTarget = process.env.DEX_BLOB_CACHE_CARGO_TARGET ?? "release";
const builtLibrary = join(rustRoot, "target", cargoTarget, libraryName);
const destinationDirectory = join(packageRoot, "native", platform);
const destination = join(destinationDirectory, stagedName);

const cargoArguments = ["build", "-p", "dex-blob-cache-node", "--locked"];
if (cargoTarget === "release") {
  cargoArguments.push("--release");
}

const build = spawnSync("cargo", cargoArguments, {
  cwd: rustRoot,
  stdio: "inherit",
  env: process.env,
});
if (build.status !== 0) {
  process.exit(build.status === null ? 1 : build.status);
}

mkdirSync(destinationDirectory, { recursive: true });
copyFileSync(builtLibrary, destination);
console.log(`staged ${destination}`);

function nativePlatform() {
  return `${operatingSystem()}-${architecture()}`;
}

function operatingSystem() {
  switch (process.platform) {
    case "darwin":
      return "macos";
    case "linux":
      return "linux";
    case "win32":
      return "windows";
    default:
      throw new Error(`unsupported operating system: ${process.platform}`);
  }
}

function architecture() {
  switch (process.arch) {
    case "x64":
      return "x86_64";
    case "arm64":
      return "aarch64";
    default:
      throw new Error(`unsupported architecture: ${process.arch}`);
  }
}

function nativeLibraryName() {
  switch (process.platform) {
    case "darwin":
      return "libdex_blob_cache_node.dylib";
    case "win32":
      return "dex_blob_cache_node.dll";
    default:
      return "libdex_blob_cache_node.so";
  }
}
