// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// The scripted test harness as a command, the way a harness package's bin calls the CLI core.

import { existsSync, writeFileSync, writeSync } from "node:fs";
import { join } from "node:path";
import { setTimeout as delay } from "node:timers/promises";

import { Client } from "@superdurable/dex";

import { processAgentCliIo, runAgentCli, type AgentHarness } from "../../src/index.js";
import {
  fixedToolSetScriptedHarness,
  plainOnlyScriptedHarness,
  scriptedHarness,
  scriptedHarnessVariantVariable,
  scriptedHarnessWithoutImages,
  scriptedHarnessWithoutReasoningEffort,
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
  "without-reasoning-effort": scriptedHarnessWithoutReasoningEffort,
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
// Once the file exists, the launcher's JavaScript writes to stdout and stderr, directly and through console.
const launcherCanaryFile = process.env.DEX_AGENTS_CLI_TEST_LAUNCHER_CANARY_FILE;
if (launcherCanaryFile !== undefined && process.argv[2] !== "__worker-host") {
  const timer = setInterval(() => {
    if (!existsSync(launcherCanaryFile)) {
      return;
    }
    clearInterval(timer);
    process.stdout.write("launcher canary stdout\n");
    process.stderr.write("launcher canary stderr\n");
    console.log("launcher canary console.log");
    console.error("launcher canary console.error");
    writeFileSync(`${launcherCanaryFile}.written`, "");
  }, 100);
  timer.unref();
}

// Holds a lease RPC at a gate: a release before it leaves, an acquire after it answered.
const leaseGateDirectory = process.env.DEX_AGENTS_CLI_TEST_LEASE_GATE_DIR;
if (leaseGateDirectory !== undefined) {
  const invokeRPC = Client.prototype.invokeRPC;
  const passGate = async (name: string): Promise<void> => {
    writeFileSync(join(leaseGateDirectory, `${name}.reached`), "");
    while (!existsSync(join(leaseGateDirectory, `${name}.go`))) {
      await delay(20);
    }
  };
  Client.prototype.invokeRPC = async function gatedInvokeRPC(this: Client, ...callArguments: unknown[]): Promise<unknown> {
    const input = callArguments[2];
    const isLeaseInput = typeof input === "object" && input !== null && "holderId" in input;
    const isAcquire = isLeaseInput && "ttlSeconds" in input;
    if (isLeaseInput && !isAcquire) {
      await passGate("release");
    }
    const output: unknown = await (invokeRPC as (...forwarded: unknown[]) => Promise<unknown>).apply(this, callArguments);
    if (isAcquire) {
      await passGate("acquire");
    }
    return output;
  } as typeof invokeRPC;
}

const harness = variants[process.env[scriptedHarnessVariantVariable] ?? ""] ?? scriptedHarness;
process.exit(await runAgentCli(harness, processAgentCliIo()));
