// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// The scripted test harness as a command, the way a harness package's bin calls the CLI core.

import { existsSync, writeSync } from "node:fs";

import { processAgentCliIo, runAgentCli, type AgentHarness } from "../../src/index.js";
import {
  fixedToolSetScriptedHarness,
  plainOnlyScriptedHarness,
  scriptedHarness,
  scriptedHarnessVariantVariable,
  scriptedHarnessWithoutImages,
  shellReplacingScriptedHarness,
} from "./scripted-harness.js";

const variants: Readonly<Record<string, AgentHarness>> = {
  "shell-replacing": shellReplacingScriptedHarness,
  "fixed-tool-set": fixedToolSetScriptedHarness,
  "without-images": scriptedHarnessWithoutImages,
  "plain-only": plainOnlyScriptedHarness,
};

// Writes to the terminal at import and later, as a framework's import side effects might.
if (process.env.DEX_AGENTS_CLI_TEST_IMPORT_NOISE === "1") {
  writeSync(2, "import noise right away\n");
  setInterval(() => {
    console.error("import noise later");
  }, 500).unref();
}

// Blocks the Worker host's main event loop for good once the file exists, as a stuck host would.
const spinFile = process.env.DEX_AGENTS_CLI_TEST_SPIN_FILE;
if (spinFile !== undefined && process.argv[2] === "__worker-host") {
  setInterval(() => {
    if (existsSync(spinFile)) {
      for (;;) {
        // Spin.
      }
    }
  }, 100).unref();
}
const harness = variants[process.env[scriptedHarnessVariantVariable] ?? ""] ?? scriptedHarness;
process.exit(await runAgentCli(harness, processAgentCliIo()));
