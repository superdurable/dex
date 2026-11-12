// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { execFileSync, spawn, type ChildProcess } from "node:child_process";
import { chmodSync, existsSync, mkdtempSync, readFileSync, readdirSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import type { Readable } from "node:stream";
import { setTimeout as delay } from "node:timers/promises";
import { fileURLToPath } from "node:url";

import { TextCollector, type TestHome } from "./cli-test-support.js";

/** The scripted harness command's entry script. */
export const cliEntryPath = fileURLToPath(new URL("./scripted-harness-cli.js", import.meta.url));
const repositoryRoot = fileURLToPath(new URL("../../../../../../../", import.meta.url));
/** The contract host that plays `dexcli agents-tui` in these tests. */
export const contractHostPath = join(repositoryRoot, "agents", "cli", "tests", "terminal_ui_contract_host.py");
/** The contract's channel transcripts. */
export const transcriptDirectory = join(repositoryRoot, "agents", "conformance", "terminal_ui", "transcripts");
/** The contract's conformance cases. */
export const conformanceDirectory = join(repositoryRoot, "agents", "conformance", "terminal_ui");
/** What the fake terminal UI prints on the terminal right before and after it runs. */
export const terminalUiStartMarker = "<<fake terminal UI starts>>";
export const terminalUiEndMarker = "<<fake terminal UI ended>>";
const defaultWaitMs = 90_000;

// Node cannot open a pseudo-terminal, so Python's pty module relays the bytes and the exit code.
const pseudoTerminalRelayScript = [
  "import os, pty, sys",
  "code = os.waitstatus_to_exitcode(pty.spawn(sys.argv[1:]))",
  "sys.exit(code if code >= 0 else 128 - code)",
].join("\n");

/** One step of a channel transcript. */
export type TranscriptStep = Readonly<Record<string, unknown>>;

/** A channel transcript, as `agents/conformance/terminal_ui/transcripts` has them. */
export interface Transcript {
  readonly name: string;
  readonly description: string;
  readonly real_roles: readonly string[];
  readonly terminal_ui_environment: Readonly<Record<string, string>>;
  readonly steps: readonly TranscriptStep[];
  readonly tui_exit: number | "terminated";
}

/**
 * Reads one of the contract's transcripts.
 * @param name - Its name, without `.json`.
 */
export function readTranscript(name: string): Transcript {
  return JSON.parse(readFileSync(join(transcriptDirectory, `${name}.json`), "utf8")) as Transcript;
}

/**
 * Returns a transcript that starts like `chat_ready_then_exit`, the launch and `ready`, then has more steps.
 * @param name - The new transcript's name.
 * @param steps - The steps after `ready`.
 * @param tuiExit - The fake terminal UI's exit code.
 */
export function chatTranscript(name: string, steps: readonly TranscriptStep[], tuiExit: number | "terminated" = 0): Transcript {
  const base = readTranscript("chat_ready_then_exit");
  return { ...base, name, description: name, steps: [...base.steps, ...steps], tui_exit: tuiExit };
}

/**
 * Returns a transcript whose launch step does not compare `notices`, for a command that starts its own `dexcli dev`.
 * @param transcript - The transcript.
 */
export function ignoringLaunchNotices(transcript: Transcript): Transcript {
  const [launch, ...rest] = transcript.steps;
  const ignore = [...((launch?.ignore as readonly string[] | undefined) ?? []), "notices"];
  return { ...transcript, steps: [{ ...launch, ignore }, ...rest] };
}

/** Returns the first Python 3.11 or later on `PATH`, as an absolute path, which the launcher's `dexcli` needs. */
export function pythonExecutable(): string {
  return execFileSync("python3", ["-c", "import sys; assert sys.version_info >= (3, 11); print(sys.executable)"], {
    encoding: "utf8",
  }).trim();
}

/**
 * A fake `dexcli`: a shell script that answers `--describe` like a `dexcli` with contract 1, and plays the terminal
 * UI side of one transcript per launch, in order, with the contract host.
 */
export class FakeTerminalUi {
  /** The script; point `DEXCLI` at it. */
  public readonly executable: string;
  private readonly directory: string;

  /**
   * Writes the script.
   * @param transcripts - The transcripts of the launches, in order. The last one repeats.
   * @param options - Whether to print the markers around each launch, and `--describe` output to print instead.
   */
  public constructor(
    transcripts: readonly Transcript[],
    options: {
      readonly hasMarkers?: boolean;
      readonly describe?: string;
      readonly stepTimeoutSeconds?: number;
      /** Whether the script loses its execute permission after `--describe`, so starting the terminal UI fails. */
      readonly isUnrunnableAfterDescribe?: boolean;
      /** Transcript values to replace with real ones, such as a fixture path with a file this test wrote. */
      readonly aliases?: Readonly<Record<string, string>>;
      /** Whether the record keeps the notices of each launch document it received. */
      readonly isLaunchNoticeRecorded?: boolean;
    } = {},
  ) {
    this.directory = mkdtempSync(join(tmpdir(), "dex-agents-fake-tui-"));
    const python = pythonExecutable();
    const transcriptPaths = transcripts.map((transcript, index) => {
      const path = join(this.directory, `launch-${index + 1}.json`);
      writeFileSync(path, JSON.stringify(transcript));
      return path;
    });
    const describeArguments =
      options.describe === undefined ? "" : ` --describe-file ${quote(join(this.directory, "describe.json"))}`;
    if (options.describe !== undefined) {
      writeFileSync(join(this.directory, "describe.json"), options.describe);
    }
    const counter = join(this.directory, "launches");
    const cases = transcriptPaths
      .map((path, index) => `  ${index + 1}) transcript=${quote(path)} ;;`)
      .concat([`  *) transcript=${quote(transcriptPaths.at(-1) ?? "")} ;;`])
      .join("\n");
    const aliasArguments = Object.entries(options.aliases ?? {})
      .map(([fixture, actual]) => ` --alias ${quote(`${fixture}=${actual}`)}`)
      .join("");
    const noticeArgument = options.isLaunchNoticeRecorded === true ? " --record-launch-notices" : "";
    const host = `${quote(python)} ${quote(contractHostPath)} tui --step-timeout ${options.stepTimeoutSeconds ?? 30}${aliasArguments}${noticeArgument}`;
    const play =
      options.hasMarkers === true
        ? [
            `printf '%s' ${quote(terminalUiStartMarker)}`,
            `${host} --transcript "$transcript" --record "$record" "$@"`,
            "code=$?",
            `printf '%s' ${quote(terminalUiEndMarker)}`,
            'exit "$code"',
          ].join("\n")
        : `exec ${host} --transcript "$transcript" --record "$record" "$@"`;
    this.executable = join(this.directory, "dexcli");
    writeFileSync(
      this.executable,
      [
        "#!/bin/sh",
        // Every other dexcli command, such as dexcli dev, is the real one.
        `if [ "$1" != "agents-tui" ]; then exec ${quote(process.env.DEXCLI ?? "dexcli")} "$@"; fi`,
        `if [ "$2" = "--describe" ]; then echo describe >> ${quote(join(this.directory, "describes"))};${
          options.isUnrunnableAfterDescribe === true ? ' chmod a-x "$0";' : ""
        } exec ${host}${describeArguments} "$@"; fi`,
        `count=$(( $(cat ${quote(counter)} 2>/dev/null || echo 0) + 1 ))`,
        `echo "$count" > ${quote(counter)}`,
        `record=${quote(this.directory)}/record-$count.jsonl`,
        `case "$count" in\n${cases}\nesac`,
        play,
        "",
      ].join("\n"),
    );
    chmodSync(this.executable, 0o755);
  }

  /** How many times `--describe` ran. */
  public get describeCount(): number {
    const path = join(this.directory, "describes");
    return existsSync(path) ? readFileSync(path, "utf8").split("\n").filter((line) => line !== "").length : 0;
  }

  /** How many times the terminal UI was launched, not counting `--describe`. */
  public get launchCount(): number {
    return existsSync(join(this.directory, "launches"))
      ? Number(readFileSync(join(this.directory, "launches"), "utf8").trim())
      : 0;
  }

  /**
   * Returns the record of one launch: what the contract host saw, as JSON Lines events.
   * @param launch - The launch, from 1.
   */
  public record(launch = 1): Readonly<Record<string, unknown>>[] {
    const path = join(this.directory, `record-${launch}.jsonl`);
    if (!existsSync(path)) {
      return [];
    }
    return readFileSync(path, "utf8")
      .split("\n")
      .filter((line) => line !== "")
      .map((line) => JSON.parse(line) as Record<string, unknown>);
  }

  /**
   * Waits until one launch's record has an event that matches.
   * @param predicate - The match.
   * @param launch - The launch, from 1.
   */
  public async waitForRecord(
    predicate: (event: Readonly<Record<string, unknown>>) => boolean,
    launch = 1,
    timeoutMs = defaultWaitMs,
  ): Promise<Readonly<Record<string, unknown>>> {
    const deadline = Date.now() + timeoutMs;
    for (;;) {
      const found = this.record(launch).find(predicate);
      if (found !== undefined) {
        return found;
      }
      if (Date.now() >= deadline) {
        throw new Error(`no matching record event; record:\n${JSON.stringify(this.record(launch))}`);
      }
      await delay(50);
    }
  }
}

/** Returns whether a record event is a message the contract host received of one type. */
export function isReceived(type: string): (event: Readonly<Record<string, unknown>>) => boolean {
  return (event) => event.event === "received" && (event.message as { type?: string } | undefined)?.type === type;
}

/** The scripted harness command running in a pseudo-terminal, as at a person's terminal. */
export class PseudoTerminalCli {
  /** Everything written to the terminal. */
  public readonly terminal: TextCollector;
  private readonly exited: Promise<number | null>;

  private constructor(private readonly child: ChildProcess) {
    this.terminal = new TextCollector(child.stdout as Readable);
    this.exited = new Promise((resolve) => child.once("exit", (code) => resolve(code)));
  }

  /**
   * Starts the command in a pseudo-terminal.
   * @param home - The test home.
   * @param commandArguments - Arguments after the command name.
   * @param environment - Variables to add or replace.
   * @param nodeArguments - Node options before the entry script, such as `--import`.
   * @param entryPath - The entry script, such as a symlink to it as a global install has.
   */
  public static start(
    home: TestHome,
    commandArguments: readonly string[] = [],
    environment: NodeJS.ProcessEnv = {},
    nodeArguments: readonly string[] = [],
    entryPath: string = cliEntryPath,
  ): PseudoTerminalCli {
    const child = spawn(
      "python3",
      ["-c", pseudoTerminalRelayScript, process.execPath, ...nodeArguments, entryPath, ...commandArguments],
      {
        env: { ...home.environment(), TERM: "xterm-256color", LANG: "en_US.UTF-8", DEX_AGENTS_TERMINAL_UI: "1", ...environment },
        stdio: ["pipe", "pipe", "inherit"],
        detached: true,
      },
    );
    return new PseudoTerminalCli(child);
  }

  /**
   * Types text at the terminal.
   * @param text - The bytes.
   */
  public type(text: string): void {
    this.child.stdin?.write(text);
  }

  /**
   * Sends a signal to the relay's process group, which holds the relay only: the command leads its own session.
   * @param signal - The signal.
   */
  public signalRelay(signal: NodeJS.Signals): void {
    if (this.child.pid !== undefined) {
      process.kill(this.child.pid, signal);
    }
  }

  /**
   * Kills the relay; the command's terminal hangs up.
   * @returns A Promise that resolves after the relay exited.
   */
  public async kill(): Promise<void> {
    if (this.child.exitCode === null && this.child.pid !== undefined) {
      try {
        process.kill(-this.child.pid, "SIGKILL");
      } catch {
        // Already gone.
      }
    }
    await this.exited;
  }

  /**
   * Waits for the command to exit.
   * @param timeoutMs - How long to wait.
   * @returns The exit code.
   */
  public async waitForExit(timeoutMs = defaultWaitMs): Promise<number | null> {
    let timer: NodeJS.Timeout | undefined;
    const timeout = new Promise<"timeout">((resolve) => {
      timer = setTimeout(() => resolve("timeout"), timeoutMs);
    });
    try {
      const result = await Promise.race([this.exited, timeout]);
      if (result === "timeout") {
        throw new Error(`the command did not exit; terminal:\n${this.terminal.all}`);
      }
      return result;
    } finally {
      clearTimeout(timer);
    }
  }
}

/** The run record of a Worker host, as the tests read it. */
export interface RunRecordForTest {
  readonly path: string;
  readonly hostPid: number;
  readonly launcher: { readonly pid: number };
  readonly host: { readonly pid: number; readonly pgid: number };
  readonly tool_groups: readonly { readonly pid: number; readonly pgid: number }[];
}

/**
 * Returns the Worker host run records under a test home.
 * @param home - The test home.
 */
export function runRecords(home: TestHome): RunRecordForTest[] {
  const directory = join(home.directory, ".dex", "agents", "run");
  if (!existsSync(directory)) {
    return [];
  }
  return readdirSync(directory)
    .filter((name) => name.endsWith(".json"))
    .flatMap((name) => {
      try {
        const record = JSON.parse(readFileSync(join(directory, name), "utf8")) as Omit<RunRecordForTest, "path" | "hostPid">;
        return [{ ...record, path: join(directory, name), hostPid: record.host.pid }];
      } catch {
        return [];
      }
    });
}

/**
 * Waits until a test home has a Worker host run record that matches.
 * @param home - The test home.
 * @param predicate - The match.
 */
export async function waitForRunRecord(
  home: TestHome,
  predicate: (record: RunRecordForTest) => boolean = () => true,
  timeoutMs = defaultWaitMs,
): Promise<RunRecordForTest> {
  const deadline = Date.now() + timeoutMs;
  for (;;) {
    const found = runRecords(home).find(predicate);
    if (found !== undefined) {
      return found;
    }
    if (Date.now() >= deadline) {
      throw new Error(`no matching Worker host run record: ${JSON.stringify(runRecords(home))}`);
    }
    await delay(100);
  }
}

/**
 * Returns whether any process is in a process group.
 * @param pgid - The group.
 */
export function isProcessGroupAlive(pgid: number): boolean {
  try {
    process.kill(-pgid, 0);
    return true;
  } catch (error: unknown) {
    return (error as NodeJS.ErrnoException).code === "EPERM";
  }
}

/**
 * Returns whether a process exists.
 * @param pid - The process.
 */
export function isProcessAlive(pid: number): boolean {
  try {
    process.kill(pid, 0);
    return true;
  } catch (error: unknown) {
    return (error as NodeJS.ErrnoException).code === "EPERM";
  }
}

/**
 * Waits until a condition holds.
 * @param condition - The condition.
 * @param description - What it waits for, for the error.
 */
export async function waitUntil(condition: () => boolean, description: string, timeoutMs = defaultWaitMs): Promise<void> {
  const deadline = Date.now() + timeoutMs;
  while (!condition()) {
    if (Date.now() >= deadline) {
      throw new Error(`timed out waiting until ${description}`);
    }
    await delay(100);
  }
}

/**
 * Returns a fresh lowercase UUIDv7, as `dexcli` makes one idempotency key per user action.
 * @param timeMs - The key's time.
 */
export function newUuidV7(timeMs = Date.now()): string {
  const random = Buffer.from(Array.from({ length: 10 }, () => Math.floor(Math.random() * 256)));
  const hex = timeMs.toString(16).padStart(12, "0") + random.toString("hex");
  const variant = ((Number.parseInt(hex.slice(16, 17), 16) & 0x3) | 0x8).toString(16);
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-7${hex.slice(13, 16)}-${variant}${hex.slice(17, 20)}-${hex.slice(20, 32)}`;
}

function quote(text: string): string {
  return `'${text.replaceAll("'", "'\\''")}'`;
}
