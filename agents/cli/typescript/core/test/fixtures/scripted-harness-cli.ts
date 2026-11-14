// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// The scripted test harness as a command, the way a harness package's bin calls the CLI core.

import { appendFileSync, existsSync, readFileSync, rmSync, writeFileSync, writeSync } from "node:fs";
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

// Lease faults: fail-acquire fails every acquire before it is sent; lose-next-acquire loses one answer and, with
// the text then-fail-lease-reads or then-hang-lease-reads, creates fail-lease-read or hang-lease-read, which fail
// or hold every read of the lease while they exist; slow-next-acquire delays one answer by its milliseconds;
// fail-cancel fails every cancel_turn. Each acquire answer appends a line to acquire-answers, and each held lease
// read appends one to held-lease-reads.
const leaseFaultDirectory = process.env.DEX_AGENTS_CLI_TEST_LEASE_FAULT_DIR;
if (leaseFaultDirectory !== undefined) {
  const getAttributes = Client.prototype.getAttributes;
  Client.prototype.getAttributes = async function faultyGetAttributes(this: Client, ...callArguments: unknown[]): Promise<unknown> {
    const reads = callArguments.slice(1) as readonly { readonly name?: string }[];
    const isLeaseRead = reads.some((read) => read.name === "SessionTerminalLease");
    if (isLeaseRead && existsSync(join(leaseFaultDirectory, "fail-lease-read"))) {
      throw new Error("injected lease read failure");
    }
    if (isLeaseRead && existsSync(join(leaseFaultDirectory, "hang-lease-read"))) {
      appendFileSync(join(leaseFaultDirectory, "held-lease-reads"), `${new Date().toISOString()}\n`);
    }
    while (isLeaseRead && existsSync(join(leaseFaultDirectory, "hang-lease-read"))) {
      await delay(50);
    }
    return (getAttributes as (...forwarded: unknown[]) => Promise<unknown>).apply(this, callArguments);
  } as typeof getAttributes;
  const invokeRPC = Client.prototype.invokeRPC;
  Client.prototype.invokeRPC = async function faultyInvokeRPC(this: Client, ...callArguments: unknown[]): Promise<unknown> {
    const input = callArguments[2];
    const isAcquire = typeof input === "object" && input !== null && "holderId" in input && "ttlSeconds" in input;
    if (isAcquire && existsSync(join(leaseFaultDirectory, "fail-acquire"))) {
      throw new Error("injected acquire failure");
    }
    const isCancel = typeof input === "object" && input !== null && Object.keys(input).join() === "turnId";
    if (isCancel && existsSync(join(leaseFaultDirectory, "fail-cancel"))) {
      throw new Error("injected cancel_turn failure");
    }
    const output: unknown = await (invokeRPC as (...forwarded: unknown[]) => Promise<unknown>).apply(this, callArguments);
    const lostAnswer = join(leaseFaultDirectory, "lose-next-acquire");
    if (isAcquire && existsSync(lostAnswer)) {
      const afterwards = readFileSync(lostAnswer, "utf8").trim();
      rmSync(lostAnswer);
      if (afterwards === "then-fail-lease-reads") {
        writeFileSync(join(leaseFaultDirectory, "fail-lease-read"), "");
      } else if (afterwards === "then-hang-lease-reads") {
        writeFileSync(join(leaseFaultDirectory, "hang-lease-read"), "");
      }
      throw new Error("no answer within 5000 ms");
    }
    const slowAnswer = join(leaseFaultDirectory, "slow-next-acquire");
    if (isAcquire && existsSync(slowAnswer)) {
      const delayMs = Number(readFileSync(slowAnswer, "utf8").trim());
      rmSync(slowAnswer);
      await delay(delayMs);
    }
    if (isAcquire) {
      appendFileSync(join(leaseFaultDirectory, "acquire-answers"), `${new Date().toISOString()}\n`);
    }
    return output;
  } as typeof invokeRPC;
}

const harness = variants[process.env[scriptedHarnessVariantVariable] ?? ""] ?? scriptedHarness;
process.exit(await runAgentCli(harness, processAgentCliIo()));
