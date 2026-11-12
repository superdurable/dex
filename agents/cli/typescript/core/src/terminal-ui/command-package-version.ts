// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { readFileSync, realpathSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

/**
 * Returns the version of the package whose bin runs this process: the nearest `package.json` above the entry
 * script, through symlinks such as a global install's. Without one, the CLI core's own version.
 */
export function commandPackageVersion(): string {
  const entryScript = process.argv[1];
  if (entryScript !== undefined) {
    try {
      const version = nearestPackageVersion(dirname(realpathSync(entryScript)));
      if (version !== undefined) {
        return version;
      }
    } catch {
      // An entry script that is gone leaves the CLI core's version.
    }
  }
  return nearestPackageVersion(dirname(fileURLToPath(import.meta.url))) ?? "0.0.0";
}

function nearestPackageVersion(start: string): string | undefined {
  for (let directory = start; ; directory = dirname(directory)) {
    try {
      const manifest = JSON.parse(readFileSync(join(directory, "package.json"), "utf8")) as { version?: unknown };
      if (typeof manifest.version === "string" && manifest.version !== "") {
        return manifest.version;
      }
    } catch {
      // No readable package.json here; look one directory up.
    }
    if (dirname(directory) === directory) {
      return undefined;
    }
  }
}
