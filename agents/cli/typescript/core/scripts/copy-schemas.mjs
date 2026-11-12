// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// Copies the shared CLI configuration schemas into dist/schemas, so the published package carries them.

import { copyFileSync, mkdirSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const packageDirectory = dirname(dirname(fileURLToPath(import.meta.url)));
const sharedCliDirectory = join(packageDirectory, "..", "..");
const targetDirectory = join(packageDirectory, "dist", "schemas");
mkdirSync(targetDirectory, { recursive: true });
for (const name of [
  "config.schema.json",
  "session-settings.schema.json",
  "terminal-ui-launch.schema.json",
  "terminal-ui-channel.schema.json",
  "terminal-ui-describe.schema.json",
]) {
  copyFileSync(join(sharedCliDirectory, name), join(targetDirectory, name));
}
