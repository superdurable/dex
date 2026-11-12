// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// Runs the command's bin at a pseudo-terminal with the terminal UI switched on and a fake dexcli: the contract
// host of agents/cli/tests playing the terminal UI of the shortest chat transcript.

import { execFileSync, spawn } from "node:child_process";
import { chmodSync, existsSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

const binPath = fileURLToPath(new URL("../../src/bin.js", import.meta.url));
const repositoryRoot = fileURLToPath(new URL("../../../../../../../", import.meta.url));
const contractHost = join(repositoryRoot, "agents", "cli", "tests", "terminal_ui_contract_host.py");
const transcriptPath = join(repositoryRoot, "agents", "conformance", "terminal_ui", "transcripts", "chat_ready_then_exit.json");
/** What the fake terminal UI prints right before and after it runs. */
const startMarker = "<<fake terminal UI starts>>";
const endMarker = "<<fake terminal UI ended>>";
const waitMs = 90_000;

// Node cannot open a pseudo-terminal, so Python's pty module relays the bytes and the exit code.
const relayScript = [
  "import os, pty, sys",
  "code = os.waitstatus_to_exitcode(pty.spawn(sys.argv[1:]))",
  "sys.exit(code if code >= 0 else 128 - code)",
].join("\n");

/** How a run at the terminal ended. */
export interface TerminalUiRun {
  readonly exitCode: number | null;
  /** Everything written to the terminal. */
  readonly terminal: string;
  /** What reached the terminal while the fake terminal UI ran, or `undefined` when it never ran. */
  readonly whileTerminalUiRan: string | undefined;
  /** How many times the command started the terminal UI. */
  readonly launchCount: number;
}

/**
 * Runs the bin with `DEX_AGENTS_TERMINAL_UI=1` at a pseudo-terminal until it exits.
 * @param environment - The command's environment.
 * @param typeOnPrompt - Text to type once the plain chat prompts, for a command that keeps the plain chat.
 * @returns How it ended.
 */
export async function runWithTerminalUi(environment: NodeJS.ProcessEnv, typeOnPrompt?: string): Promise<TerminalUiRun> {
  const directory = mkdtempSync(join(tmpdir(), "dex-agents-fake-tui-"));
  const transcript = JSON.parse(readFileSync(transcriptPath, "utf8")) as { steps: { ignore?: string[] }[] };
  // The harness decides methods and capabilities; this run checks the handoff itself.
  transcript.steps[0]?.ignore?.push("methods", "capabilities");
  writeFileSync(join(directory, "transcript.json"), JSON.stringify(transcript));
  const python = execFileSync("python3", ["-c", "import sys; print(sys.executable)"], { encoding: "utf8" }).trim();
  const host = `'${python}' '${contractHost}' tui`;
  const dexcli = join(directory, "dexcli");
  writeFileSync(
    dexcli,
    [
      "#!/bin/sh",
      `if [ "$2" = "--describe" ]; then exec ${host} "$@"; fi`,
      `echo launch >> '${join(directory, "launches")}'`,
      `printf '%s' '${startMarker}'`,
      `${host} --transcript '${join(directory, "transcript.json")}' "$@"`,
      "code=$?",
      `printf '%s' '${endMarker}'`,
      'exit "$code"',
      "",
    ].join("\n"),
  );
  chmodSync(dexcli, 0o755);
  const child = spawn("python3", ["-c", relayScript, process.execPath, binPath], {
    env: { ...environment, TERM: "xterm-256color", DEX_AGENTS_TERMINAL_UI: "1", DEXCLI: dexcli },
    stdio: ["pipe", "pipe", "inherit"],
    detached: true,
  });
  let terminal = "";
  let hasTyped = false;
  child.stdout.setEncoding("utf8");
  child.stdout.on("data", (chunk: string) => {
    terminal += chunk;
    if (typeOnPrompt !== undefined && !hasTyped && terminal.includes("Type a message")) {
      hasTyped = true;
      child.stdin.write(typeOnPrompt);
    }
  });
  const exitCode = await new Promise<number | null>((resolve, reject) => {
    const timer = setTimeout(() => {
      if (child.pid !== undefined) {
        process.kill(-child.pid, "SIGKILL");
      }
      reject(new Error(`the command did not exit; terminal:\n${terminal}`));
    }, waitMs);
    child.once("exit", (code) => {
      clearTimeout(timer);
      resolve(code);
    });
  });
  const start = terminal.indexOf(startMarker);
  const end = terminal.indexOf(endMarker);
  const launches = join(directory, "launches");
  return {
    exitCode,
    terminal,
    whileTerminalUiRan: start >= 0 && end > start ? terminal.slice(start + startMarker.length, end) : undefined,
    launchCount: existsSync(launches) ? readFileSync(launches, "utf8").split("\n").filter((line) => line !== "").length : 0,
  };
}
