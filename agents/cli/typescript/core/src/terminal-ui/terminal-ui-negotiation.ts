// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { spawn } from "node:child_process";

import { maxChannelLineBytes } from "./terminal-ui-channel.js";
import { terminalUiSchemaValidator } from "./terminal-ui-schemas.js";

/** The terminal UI contract versions this CLI core speaks. */
export const launcherContractVersions: readonly number[] = Object.freeze([1]);
/** How long `dexcli agents-tui --describe` may take. */
const describeTimeoutMs = 5_000;
/** The first `dexcli` release whose `agents-tui` speaks contract 1. */
export const terminalUiMinimumDexcliVersion = "1.46.0";
const newerDexcliHint = `The terminal UI needs dexcli ${terminalUiMinimumDexcliVersion} or later; point DEXCLI at one`;
/** The capability a chat needs: each session runs on its own task queue, so the launcher must open a switch. */
const launcherSessionSwitchCapability = "launcher_session_switch";
/** The first `dexcli` release that hands session switches back to the launcher. */
const launcherSessionSwitchMinimumDexcliVersion = "1.47.2";

/** A mode of the terminal UI. */
export type TerminalUiMode = "chat" | "sessions" | "connectors";

/** The `--describe` output of a `dexcli`, after it passed its schema. */
export interface TerminalUiDescription {
  readonly dexcli_version: string;
  readonly contracts: Readonly<
    Record<string, { readonly modes: Readonly<Record<string, unknown>>; readonly capabilities?: readonly string[] }>
  >;
}

/** The contract to hand off with, or the plain reason of the contract. */
export type ContractChoice =
  | { readonly contract: number }
  | {
      readonly plainReason: "describe_failed" | "no_common_contract" | "mode_missing" | "capability_missing";
      readonly detail: string;
    };

/**
 * Returns the choice for a chat, which needs `launcher_session_switch`: a `dexcli` without it switches sessions in
 * place, onto a session task queue that no Worker of this command pulls.
 * @param description - A `--describe` document that passed its schema.
 * @param choice - The contract {@link chooseContract} chose.
 */
export function requireChatCapabilities(description: TerminalUiDescription, choice: ContractChoice): ContractChoice {
  if (!("contract" in choice)) {
    return choice;
  }
  const capabilities = description.contracts[String(choice.contract)]?.capabilities ?? [];
  if (capabilities.includes(launcherSessionSwitchCapability)) {
    return choice;
  }
  return {
    plainReason: "capability_missing",
    detail:
      `dexcli ${description.dexcli_version} cannot hand session switches back to this command. ` +
      `The terminal UI needs dexcli ${launcherSessionSwitchMinimumDexcliVersion} or later; point DEXCLI at one`,
  };
}

/**
 * Returns the highest contract both sides support whose modes include `mode`.
 * @param launcherContracts - The launcher's versions.
 * @param description - A `--describe` document that passed its schema.
 * @param mode - The mode the subcommand needs.
 */
export function chooseContract(
  launcherContracts: readonly number[],
  description: TerminalUiDescription,
  mode: TerminalUiMode,
): ContractChoice {
  const described = new Set(Object.keys(description.contracts).map(Number));
  const common = launcherContracts.filter((version) => described.has(version)).sort((left, right) => right - left);
  if (described.size === 0) {
    return { plainReason: "no_common_contract", detail: `dexcli ${description.dexcli_version} offers no terminal UI contract` };
  }
  if (common.length === 0) {
    const isCommandOlder = Math.min(...described) > Math.max(...launcherContracts);
    return {
      plainReason: "no_common_contract",
      detail: `dexcli ${description.dexcli_version} speaks no terminal UI contract this command knows. ${
        isCommandOlder ? "Upgrade this command to use it" : newerDexcliHint
      }`,
    };
  }
  for (const version of common) {
    if (mode in (description.contracts[String(version)]?.modes ?? {})) {
      return { contract: version };
    }
  }
  return { plainReason: "mode_missing", detail: `dexcli ${description.dexcli_version} has no ${mode} terminal UI` };
}

/**
 * Runs `dexcli agents-tui --describe` without a terminal, network, or environment, and chooses the contract.
 * @param dexcli - The absolute `dexcli` path.
 * @param mode - The mode the subcommand needs.
 * @returns The contract, or why the command uses the plain chat.
 */
export async function negotiateTerminalUi(dexcli: string, mode: TerminalUiMode): Promise<ContractChoice> {
  const output = await runDescribe(dexcli);
  if (typeof output !== "object") {
    return { plainReason: "describe_failed", detail: `${dexcli} has no terminal UI (${output}). ${newerDexcliHint}` };
  }
  const choice = chooseContract(launcherContractVersions, output, mode);
  return mode === "chat" ? requireChatCapabilities(output, choice) : choice;
}

/**
 * Returns the parsed `--describe` document, or why it failed.
 * @param dexcli - The absolute `dexcli` path.
 */
async function runDescribe(dexcli: string): Promise<TerminalUiDescription | string> {
  const child = spawn(dexcli, ["agents-tui", "--describe"], { stdio: ["ignore", "pipe", "ignore"], env: {} });
  const chunks: Buffer[] = [];
  let byteCount = 0;
  let isTooLong = false;
  child.stdout.on("data", (chunk: Buffer) => {
    byteCount += chunk.length;
    if (byteCount > maxChannelLineBytes + 1) {
      isTooLong = true;
      child.kill("SIGKILL");
      return;
    }
    chunks.push(chunk);
  });
  const exit = new Promise<string | number>((resolve) => {
    child.once("error", (error) => resolve(`it cannot run: ${error.message}`));
    child.once("close", (code, signal) => resolve(code ?? `agents-tui --describe ended by ${signal ?? "a signal"}`));
  });
  let timer: NodeJS.Timeout | undefined;
  const timeout = new Promise<string>((resolve) => {
    timer = setTimeout(() => resolve("timeout"), describeTimeoutMs);
  });
  const status = await Promise.race([exit, timeout]);
  clearTimeout(timer);
  if (status === "timeout") {
    child.kill("SIGKILL");
    return `agents-tui --describe did not answer within ${describeTimeoutMs / 1000} seconds`;
  }
  if (isTooLong) {
    return "agents-tui --describe printed more than 1 MiB";
  }
  if (status !== 0) {
    return typeof status === "string" ? status : `agents-tui --describe exited with code ${status}`;
  }
  return parseDescription(Buffer.concat(chunks));
}

/**
 * Parses `--describe` stdout: one UTF-8 JSON object of at most 1 MiB, then a newline, that passes its schema.
 * @param stdout - The bytes.
 * @returns The document, or why it is refused.
 */
export function parseDescription(stdout: Buffer): TerminalUiDescription | string {
  const body = stdout.at(-1) === 0x0a ? stdout.subarray(0, -1) : stdout;
  if (body.length > maxChannelLineBytes) {
    return "agents-tui --describe printed more than 1 MiB";
  }
  let value: unknown;
  try {
    value = JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(body));
  } catch {
    return "agents-tui --describe did not print one JSON object";
  }
  const validate = terminalUiSchemaValidator("describe");
  if (!validate(value)) {
    return "agents-tui --describe printed a document this command does not understand";
  }
  return value as TerminalUiDescription;
}
