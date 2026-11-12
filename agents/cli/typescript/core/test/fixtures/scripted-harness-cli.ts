// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// The scripted test harness as a command, the way a harness package's bin calls the CLI core.

import { spawnSync } from "node:child_process";
import { existsSync, writeFileSync, writeSync } from "node:fs";

import { processAgentCliIo, runAgentCli, type AgentHarness } from "../../src/index.js";
import {
  fixedToolSetScriptedHarness,
  plainOnlyScriptedHarness,
  scriptedHarness,
  scriptedHarnessVariantVariable,
  scriptedHarnessWithoutImages,
  shellReplacingScriptedHarness,
  summaryForcedOffScriptedHarness,
  summaryForcedOnScriptedHarness,
  summaryMisreportedOffScriptedHarness,
  summaryMisreportedOnScriptedHarness,
} from "./scripted-harness.js";

const variants: Readonly<Record<string, AgentHarness>> = {
  "shell-replacing": shellReplacingScriptedHarness,
  "fixed-tool-set": fixedToolSetScriptedHarness,
  "without-images": scriptedHarnessWithoutImages,
  "plain-only": plainOnlyScriptedHarness,
  "summary-on": summaryForcedOnScriptedHarness,
  "summary-off": summaryForcedOffScriptedHarness,
  "misreported-on": summaryMisreportedOnScriptedHarness,
  "misreported-off": summaryMisreportedOffScriptedHarness,
};

// Writes to the terminal at import and later, as a framework's import side effects might.
if (process.env.DEX_AGENTS_CLI_TEST_IMPORT_NOISE === "1") {
  writeSync(2, "import noise right away\n");
  setInterval(() => {
    console.error("import noise later");
  }, 500).unref();
}

// Blocks the Worker host's main event loop while the file exists, as a slow host would.
const pauseFile = process.env.DEX_AGENTS_CLI_TEST_PAUSE_FILE;
if (pauseFile !== undefined && process.argv[2] === "__worker-host") {
  setInterval(() => {
    while (existsSync(pauseFile)) {
      // Busy until the test removes the file.
    }
  }, 50).unref();
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
// Once the file exists, the launcher writes to fds 1 and 2 directly, and so does a child with inherited stdio.
const launcherCanaryFile = process.env.DEX_AGENTS_CLI_TEST_LAUNCHER_CANARY_FILE;
if (launcherCanaryFile !== undefined && process.argv[2] !== "__worker-host") {
  const timer = setInterval(() => {
    if (!existsSync(launcherCanaryFile)) {
      return;
    }
    clearInterval(timer);
    writeSync(1, "launcher canary fd 1\n");
    writeSync(2, "launcher canary fd 2\n");
    spawnSync("/bin/sh", ["-c", "echo launcher canary child fd 1; echo launcher canary child fd 2 >&2"], { stdio: "inherit" });
    writeFileSync(`${launcherCanaryFile}.written`, "");
  }, 100);
  timer.unref();
}

const harness = variants[process.env[scriptedHarnessVariantVariable] ?? ""] ?? scriptedHarness;
process.exit(await runAgentCli(harness, processAgentCliIo()));
