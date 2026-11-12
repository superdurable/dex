// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// Runs Pi's grep and find once, so Pi downloads rg and fd into $PI_CODING_AGENT_DIR/bin when PATH lacks them.

import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

import { createFindTool, createGrepTool } from "@earendil-works/pi-coding-agent";

const directory = mkdtempSync(join(tmpdir(), "dex-agents-pi-search-tools-"));
try {
  writeFileSync(join(directory, "probe.txt"), "probe\n");
  await createGrepTool(directory).execute("download-rg", { pattern: "probe" });
  await createFindTool(directory).execute("download-fd", { pattern: "*.txt" });
} finally {
  rmSync(directory, { recursive: true, force: true });
}
