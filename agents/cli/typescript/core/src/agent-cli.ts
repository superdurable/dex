// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { spawn } from "node:child_process";
import { createHash, randomBytes } from "node:crypto";
import { appendFileSync, mkdirSync, mkdtempSync, realpathSync, rmSync } from "node:fs";
import { constants, hostname, tmpdir, userInfo } from "node:os";
import { join, resolve } from "node:path";
import type { Readable, Writable } from "node:stream";
import { format, parseArgs } from "node:util";

import { Client, FlowNotFoundError, Registry, Worker, openBlobCache, type BlobCache } from "@superdurable/dex";
import {
  LocalDirectoryMediaStore,
  listAgentSessions,
  sessionFlowDefinitionDocument,
  sortAgentSessionsByLastActivity,
  startAgentSession,
  type AgentFrameworkRunner,
  type AgentSessionFlow,
  type DeferredToolOutcome,
  type MediaStore,
  type ToolSource,
  type TurnSessionSettings,
} from "@superdurable/dex-agents";

import type { AgentHarness, HarnessContext, LocalToolGroup } from "./agent-harness.js";
import { localToolGroups, requireValidHarnessDeclarations } from "./agent-harness.js";
import {
  ConfigError,
  canonicalOperationName,
  isCoveredByConnectorEntry,
  isSessionSummaryIndexed,
  type EffectiveConfig,
  type MediaConfig,
} from "./cli-config.js";
import { readCliEnvironment, removeDexSecretVariables, type CliEnvironment } from "./cli-environment.js";
import {
  DexConnectionSettingsError,
  createDexConnectionOptions,
  DescribedDexFailure,
  describeDexRefusalWithoutDetails,
  describeFailureWithoutDexDetails,
  describeRefusedDexCredential,
  describeRefusedTlsOptions,
  resolveDexConnectionSettings,
  type DexConnectionOptions,
  type DexConnectionSettings,
} from "./dex-connection.js";
import {
  askInitQuestions,
  defaultConfigPath,
  defaultInitAnswers,
  describeConfigError,
  expandConfigPath,
  inMemoryConfig,
  isConfigPresent,
  readConfigFile,
  readFileInstructions,
  writeConfigFile,
  type InitAnswers,
  type LoadedConfig,
} from "./config-file.js";
import {
  ConnectorsMcpToolSource,
  DisabledConnectorsToolSource,
  connectorsAccessDeniedSentence,
  connectorsUnavailableLine,
  isConnectorsMcpAccessDenied,
  probeConnectorsMcp,
  type ConnectorToolIdentity,
  type ConnectorsMcpUnavailability,
} from "./connector-tool-source.js";
import { DexcliVariableError, resolveDexcliExecutable } from "./dexcli-binary.js";
import {
  StartedDexcliDevProcess,
  describeLostDexcliDev,
  resolveDexServerForCommand,
  type DexServerConnection,
} from "./dex-server.js";
import { LocalToolSource } from "./local-tools/local-tool-source.js";
import { waitForQueuedOutput } from "./queued-output.js";
import {
  SessionEnvironmentFence,
  SessionTakeover,
  SessionWorkerStartError,
  isHarnessSessionFlowType,
  otherHarnessSessionError,
} from "./session-environment-fence.js";
import { collectSessionsForList } from "./session-list-pages.js";
import {
  SessionLeaseRenewal,
  acquireSessionLease,
  defaultSessionLeaseTtlSeconds,
  describeLeaseHolder,
  isAnimatedWaitingOutput,
  leaseLostLine,
  newSessionLeaseHolder,
  waitForSessionLease,
  type SessionLeaseHolder,
} from "./session-terminal-lease.js";
import { commandPackageVersion } from "./terminal-ui/command-package-version.js";
import { TerminalUiChatLauncher, type TerminalUiChatOutcome } from "./terminal-ui/terminal-ui-chat-launcher.js";
import { negotiateTerminalUi } from "./terminal-ui/terminal-ui-negotiation.js";
import { isTerminalUiSelected } from "./terminal-ui/terminal-ui-selection.js";
import { cleanUpLeftoverWorkerHosts } from "./worker-host/leftover-worker-hosts.js";
import type { ToolProcessGroupRegistry } from "./worker-host/tool-process-groups.js";
import { runWorkerHost } from "./worker-host/worker-host.js";
import { workerHostSubcommand } from "./worker-host/worker-host-protocol.js";
import { TerminalChat } from "./terminal-chat.js";
import { TerminalInput, lineWaitAborted } from "./terminal-input.js";
import { TurnConfigResolver } from "./turn-config-resolver.js";
import { describeUnavailableDexServer } from "./unavailable-dex-server.js";

const subcommands = ["run", "init", "connectors", "sessions"] as const;
type Subcommand = (typeof subcommands)[number];
const blobCacheBytes = 256 * 1024 * 1024;

/**
 * The process a command runs in: its arguments, streams, environment, and whether a person is at a terminal.
 *
 * @since 0.5.0
 */
export interface AgentCliIo {
  /** The arguments after the command name. */
  readonly argv: readonly string[];
  /** Input. */
  readonly stdin: Readable;
  /** Output of the chat. */
  readonly stdout: Writable;
  /** Progress, warnings, and errors. */
  readonly stderr: Writable;
  /**
   * Whether a person is at a terminal. Only then does a run without a configuration file ask the `init`
   * questions, and does the command open Dex Web in a browser.
   */
  readonly isInteractive: boolean;
  /** Environment variables; see {@link CliEnvironment}. */
  readonly environment: NodeJS.ProcessEnv;
}

/**
 * Returns the {@link AgentCliIo} of the current process.
 * @returns The process's arguments, streams, and environment; interactive when stdin and stdout are terminals.
 * @since 0.5.0
 */
export function processAgentCliIo(): AgentCliIo {
  return {
    argv: process.argv.slice(2),
    stdin: process.stdin,
    stdout: process.stdout,
    stderr: process.stderr,
    isInteractive: process.stdin.isTTY === true && process.stdout.isTTY === true,
    environment: process.env,
  };
}

const sessionSummaryAttributeNames: ReadonlySet<string> = new Set([
  "AgentSessionTitle",
  "AgentSessionLastActivityAt",
  "AgentSessionTurnCount",
]);

/** Refuses a harness Flow whose declared indexed session summary differs from the file's `sessions.summary`. */
function requireSessionSummarySetting(
  flow: AgentSessionFlow<AgentFrameworkRunner<DeferredToolOutcome>>,
  isExpected: boolean,
): void {
  // The persistence schema decides what the Worker registers; the option must agree with it.
  const declaredCount = (flow.getPersistenceSchema?.()?.attributes ?? []).filter(
    (definition) => sessionSummaryAttributeNames.has(definition.name) && definition.index !== undefined,
  ).length;
  const isOptionOn = flow.options.summaryAttributesIndexed === true;
  if (declaredCount === (isExpected ? sessionSummaryAttributeNames.size : 0) && isOptionOn === isExpected) {
    return;
  }
  throw new Error(
    `sessions.summary says the session Flow must ${isExpected ? "keep" : "leave out"} the indexed session summary, ` +
      `but the harness built it declaring ${declaredCount} of its ${sessionSummaryAttributeNames.size} indexed ` +
      `Attributes with summaryAttributesIndexed ${String(isOptionOn)}; the harness must pass ` +
      "HarnessContext.isSessionSummaryIndexed",
  );
}

/**
 * Runs one Dex Agents CLI command for a harness: `run` (the default), `init`, `connectors`, or `sessions`.
 *
 * `run` loads `~/.dex/agents/<name>.yaml` or `--config`. Without the file, it asks the `init` questions at a
 * terminal and writes the file, writes the defaults with `--yes`, and otherwise runs on the defaults without a
 * file. It then reuses or starts the Dex server, connects the connectors MCP server, starts a Worker in this
 * process, and chats in the terminal with a new session or the `--resume` one. Exiting keeps the session.
 *
 * A `dexcli dev` that the command started for `dex.server: auto` stops when the command exits: on `/exit`, the end
 * of input, the Ctrl-C that exits, a failure, SIGTERM, or SIGHUP. On a signal, the command stops it and exits the
 * process with 128 plus the signal number. A `dexcli dev` the command reused keeps running. To share one across
 * sessions, run `dexcli dev` yourself first. When a reused `dexcli dev` stops during the command, the command fails
 * with a short error that says so.
 *
 * Changed in 0.6.0: a started `dexcli dev` stops when the command exits instead of running on.
 *
 * A harness command's bin calls it and exits with its result, because open connections can keep Node running. When
 * it returns or throws, its output on `io.stdout` and `io.stderr` has reached the operating system, so exiting at once
 * loses no line, even when a slow reader such as a pipe has not read them yet. It waits at most 2 seconds for that
 * output, so a reader that never reads cannot hang the command. A handled SIGTERM, SIGHUP, or SIGINT waits the same
 * way before the process exits; a second signal exits at once.
 *
 * Changed in 0.6.0: it waits for its output before it returns. Before, `process.exit` could cut off the end of a long
 * `sessions` list written to a pipe.
 *
 * @example
 * ```ts
 * #!/usr/bin/env node
 * import { runAgentCli } from "@superdurable/dex-agents-cli-core";
 * import { claudeSdkHarness } from "./claude-sdk-harness.js";
 *
 * process.exit(await runAgentCli(claudeSdkHarness));
 * ```
 * @param harness - The command's harness.
 * @param io - The process; defaults to {@link processAgentCliIo}.
 * @returns The exit code: 0 on success, 1 for a failure or for input that ended during the `init` questions, 2 for
 * invalid arguments or configuration, and 130 for Ctrl-C during the `init` questions. A cancelled `init` writes no
 * file.
 * @throws {Error} Since 0.6.2, before anything else, when the harness's `supportedReasoningEfforts` or
 * `suggestedModels` has a malformed or repeated entry: a programming error of the harness.
 * @since 0.5.0
 */
export async function runAgentCli(harness: AgentHarness, io: AgentCliIo = processAgentCliIo()): Promise<number> {
  try {
    return await runAgentCliCommand(harness, io);
  } finally {
    // Callers exit the process at once, which drops writes still queued for a slow pipe.
    await waitForQueuedOutput([io.stdout, io.stderr]);
  }
}

async function runAgentCliCommand(harness: AgentHarness, io: AgentCliIo): Promise<number> {
  requireValidHarnessDeclarations(harness);
  if (io.argv[0] === workerHostSubcommand) {
    return runWorkerHost(harness, io, (log) => AgentCliCommand.forWorkerHost(harness, io, log));
  }
  const log = (line: string): void => {
    io.stderr.write(`${line}\n`);
  };
  let parsed: ParsedArguments;
  try {
    parsed = parseCommandArguments(io.argv);
  } catch (error: unknown) {
    log(`${describe(error)}\n${usage(harness)}`);
    return 2;
  }
  if (parsed.isHelpRequested) {
    io.stdout.write(`${usage(harness)}\n`);
    return 0;
  }
  const command = new AgentCliCommand(harness, io, parsed, log);
  ignoreWriteErrorsOfClosedTerminal(io.stdout);
  ignoreWriteErrorsOfClosedTerminal(io.stderr);
  let exitCode: number;
  try {
    exitCode = await command.run();
  } catch (error: unknown) {
    if (error instanceof InitCancelledError) {
      exitCode = error.exitCode;
    } else {
      log(`dex-agents-${harness.name}: ${(await command.describeLostDexServer()) ?? describe(error)}`);
      exitCode = command.exitCodeOf(error);
    }
  } finally {
    await command.close();
  }
  // A chat that ended while a signal's cleanup ran exits as the signal does.
  return command.signalExitCode ?? exitCode;
}

interface ParsedArguments {
  readonly subcommand: Subcommand;
  readonly configPath: string | undefined;
  readonly isYes: boolean;
  readonly resumeSessionId: string | undefined;
  readonly model: string | undefined;
  readonly isPlainRequested: boolean;
  readonly isHelpRequested: boolean;
}

/** A command-line or configuration error: the command exits with 2. */
export class UsageError extends Error {}

/** The Worker half of a chat: the session Flow, its Worker and Client, and the session. */
export interface OpenedSessionWorker {
  /** The Client, whose Registry contains {@link flow}. */
  readonly client: Client;
  /** The session Flow. */
  readonly flow: AgentSessionFlow<AgentFrameworkRunner<DeferredToolOutcome>>;
  /**
   * The session's Flow type, which the Worker polls: this execution environment's, or another environment's for a
   * resumed session that started there.
   */
  readonly flowType: string;
  /** This process's lease holder, which names this execution environment. */
  readonly leaseHolder: SessionLeaseHolder;
  /** Takes, completes, and leaves the session's lease; it starts a deferred Worker. */
  readonly takeover: SessionTakeover;
  /** The session. */
  readonly sessionId: string;
  /** Whether the session was resumed rather than started. */
  readonly isResumed: boolean;
  /** The values each new session records as its `startEnvironment`. */
  readonly startEnvironment: Record<string, string>;
  /** The store that `/image` writes to. */
  readonly mediaStore: MediaStore;
  /** The connectors MCP source and its URL, or `undefined` when connectors are off. */
  readonly connectors: { readonly source: ConnectorsMcpToolSource; readonly url: string } | undefined;
  /** The workspace's real path. */
  readonly workspace: string;
}

/** The lease the plain chat holds, with what releases it. */
interface HeldSessionLease {
  readonly takeover: SessionTakeover;
  readonly sessionId: string;
  readonly renewal: SessionLeaseRenewal;
}

/** The sessions whose lease call got no answer; leaving them by holder ID is harmless. */
interface UnansweredSessionLeases {
  readonly takeover: SessionTakeover;
  readonly sessionIds: Set<string>;
}

/** Ctrl-C or the end of input during the `init` questions, after which nothing was written. */
class InitCancelledError extends Error {
  public constructor(public readonly exitCode: number) {
    super("the init questions were cancelled");
  }
}

/** The shell's exit code for a command that Ctrl-C ended. */
const interruptedExitCode = 130;

/** One command run, owning everything it opened. */
export class AgentCliCommand {
  private readonly environment: CliEnvironment;
  private input: TerminalInput | undefined;
  private isInitInterrupted = false;
  private linesTypedAhead: readonly string[] = [];
  private worker: Worker | undefined;
  private client: Client | undefined;
  private blobCache: BlobCache | undefined;
  private blobCacheDirectory: string | undefined;
  private localTools: LocalToolSource | undefined;
  private mediaStore: ClosableMediaStore | undefined;
  private sessionFlow: AgentSessionFlow<AgentFrameworkRunner<DeferredToolOutcome>> | undefined;
  private readonly startedDexcliDev: StartedDexcliDevProcess;
  private server: DexServerConnection | undefined;
  /** The lines printed while this command started a `dexcli dev`, which the terminal UI shows again. */
  private startedDexcliDevLines: readonly string[] = [];
  private libraryConsole: LibraryConsoleRedirection | undefined;
  /** The plain chat's held session lease, which {@link close} releases before the Worker stops. */
  private heldSessionLease: HeldSessionLease | undefined;
  private unansweredSessionLeases: UnansweredSessionLeases | undefined;
  private closing: Promise<void> | undefined;
  private terminationSignal: "SIGTERM" | "SIGHUP" | undefined;
  private readonly terminationListeners = new Map<NodeJS.Signals, () => void>();

  public constructor(
    private readonly harness: AgentHarness,
    private readonly io: AgentCliIo,
    private readonly parsed: ParsedArguments,
    private readonly log: (line: string) => void,
  ) {
    this.environment = readCliEnvironment(io.environment);
    // Tools, browsers, and dexcli dev inherit process.env; the credential stays in this.environment.
    removeDexSecretVariables(process.env);
    this.startedDexcliDev = new StartedDexcliDevProcess(this.environment, log);
  }

  /**
   * Returns the command object of a Worker host, which opens the session its launcher asks for.
   * @param harness - The command's harness.
   * @param io - The Worker host's process.
   * @param log - Writes the Worker host's log lines.
   */
  public static forWorkerHost(harness: AgentHarness, io: AgentCliIo, log: (line: string) => void): AgentCliCommand {
    const parsed: ParsedArguments = {
      subcommand: "run",
      configPath: undefined,
      isYes: false,
      resumeSessionId: undefined,
      model: undefined,
      isPlainRequested: true,
      isHelpRequested: false,
    };
    // The Worker host serves the person at the launcher's terminal, so it may open Dex Web pages for them.
    return new AgentCliCommand(harness, { ...io, isInteractive: true }, parsed, log);
  }

  /**
   * Returns the exit code of an error that stopped the command: 2 for arguments or configuration, else 1.
   * @param error - The error.
   */
  public exitCodeOf(error: unknown): number {
    return error instanceof ConfigError || error instanceof UsageError || error instanceof DexConnectionSettingsError
      ? 2
      : 1;
  }

  public async run(): Promise<number> {
    if (process.platform !== "win32") {
      await cleanUpLeftoverWorkerHosts(this.environment.agentsDirectory, this.log);
    }
    const configPath =
      this.parsed.configPath === undefined
        ? defaultConfigPath(this.environment.agentsDirectory, this.harness)
        : // A relative --config is relative to the working directory, so expand it as if a file there named it.
          expandConfigPath(this.parsed.configPath, join(process.cwd(), "--config"), this.environment.homeDirectory);
    if (this.parsed.subcommand === "init") {
      await this.initConfig(configPath);
      return 0;
    }
    const loaded = await this.loadConfig(configPath);
    const effective = this.applyOverrides(loaded.effective);
    const connection = resolveDexConnectionSettings(effective, this.environment.dexConnectionVariables ?? {}, {
      configPath: loaded.configPath,
      homeDirectory: this.environment.homeDirectory,
      workingDirectory: process.cwd(),
    });
    if (this.parsed.subcommand === "run") {
      this.mediaStore = await createMediaStore(effective.media, this.environment.agentsDirectory);
    }
    const serverLines: string[] = [];
    const logServerLine = (line: string): void => {
      serverLines.push(line);
      this.log(line);
    };
    const server = await resolveDexServerForCommand(effective.dex.server, this.environment, logServerLine, this.startedDexcliDev);
    this.server = server;
    if (server.origin === "started") {
      this.startedDexcliDevLines = serverLines;
    }
    if (server.webUrl !== null) {
      this.log(`Dex Web: ${server.webUrl}`);
    }
    switch (this.parsed.subcommand) {
      case "connectors":
        return this.listConnectors(effective, server);
      case "sessions":
        return this.listSessions(effective, loaded, server, connection);
      default: {
        const outcome = await this.chatInTerminalUi(effective, loaded, server, connection);
        if (outcome?.kind === "exited") {
          return outcome.exitCode;
        }
        return this.chat(
          effective,
          loaded,
          server,
          connection,
          outcome?.resumeSessionId ?? this.parsed.resumeSessionId,
          outcome === undefined,
        );
      }
    }
  }

  /**
   * Releases the lease, closes the Worker and the Client, and stops a started `dexcli dev`, once; a second call
   * waits for the first.
   */
  public close(): Promise<void> {
    this.closing ??= this.closeOnce();
    return this.closing;
  }

  /** Returns 128 plus the number of the SIGTERM or SIGHUP that ends the command, or `undefined`. */
  public get signalExitCode(): number | undefined {
    return this.terminationSignal === undefined ? undefined : 128 + constants.signals[this.terminationSignal];
  }

  private async closeOnce(): Promise<void> {
    try {
      await this.closeResources();
    } finally {
      // Kept until here, so a second signal during the cleanup takes the force path, not Node's default.
      this.removeTerminationListeners();
    }
  }

  private async closeResources(): Promise<void> {
    await this.releaseHeldSessionLease();
    this.input?.close();
    await this.client?.close().catch(() => undefined);
    await this.worker?.close().catch(() => undefined);
    await this.localTools?.close().catch(() => undefined);
    this.mediaStore?.close?.();
    this.blobCache?.close();
    if (this.blobCacheDirectory !== undefined) {
      rmSync(this.blobCacheDirectory, { recursive: true, force: true });
    }
    this.libraryConsole?.restore();
    await this.startedDexcliDev.stop();
  }

  /** Returns the short error for a reused `dexcli dev` that stopped, or `undefined` when that did not happen. */
  public async describeLostDexServer(): Promise<string | undefined> {
    const server = this.server;
    if (server?.origin !== "reused" || server.webUrl === null) {
      return undefined;
    }
    return describeLostDexcliDev(server.webUrl);
  }

  private async initConfig(configPath: string): Promise<LoadedConfig> {
    const answers = this.parsed.isYes || !this.io.isInteractive
      ? defaultInitAnswers(this.harness)
      : await this.askInitQuestionsAtTerminal();
    if (answers === null) {
      this.io.stdout.write("\n");
      this.log("Cancelled; no configuration written.");
      throw new InitCancelledError(this.isInitInterrupted ? interruptedExitCode : 1);
    }
    const loaded = writeConfigFile(configPath, answers, this.harness);
    this.log(`Wrote ${configPath}`);
    return loaded;
  }

  private async loadConfig(configPath: string): Promise<LoadedConfig> {
    if (isConfigPresent(configPath)) {
      try {
        return readConfigFile(configPath, this.harness);
      } catch (error: unknown) {
        if (error instanceof ConfigError) {
          throw new UsageError(describeConfigError(error, configPath));
        }
        throw error;
      }
    }
    if (this.parsed.configPath !== undefined) {
      throw new UsageError(`${configPath} does not exist`);
    }
    if (this.io.isInteractive || this.parsed.isYes) {
      this.log(`No configuration at ${configPath}.`);
      return this.initConfig(configPath);
    }
    this.log(
      `No configuration at ${configPath}; running with the defaults. Run dex-agents-${this.harness.name} init to create one.`,
    );
    return inMemoryConfig(configPath, defaultInitAnswers(this.harness), this.harness);
  }

  private applyOverrides(effective: EffectiveConfig): EffectiveConfig {
    return this.parsed.model === undefined ? effective : { ...effective, model: this.parsed.model };
  }

  private async chat(
    effective: EffectiveConfig,
    loaded: LoadedConfig,
    server: DexServerConnection,
    connection: DexConnectionSettings,
    resumeSessionId: string | undefined,
    isOpeningConnectorsPage: boolean,
  ): Promise<number> {
    const opened = await this.openSessionWorker(effective, loaded, server, connection, {
      resumeSessionId,
      isOpeningConnectorsPage,
      toolProcessGroups: undefined,
      leaseHolderProcessId: process.pid,
    });
    const sessionId = opened.sessionId;
    // From here Ctrl-C ends the wait for the lease, and then the chat handles it.
    this.startedDexcliDev.releaseInterrupt();
    const holder = opened.leaseHolder;
    const takeover = opened.takeover;
    const ttlSeconds = this.environment.sessionLeaseTtlSeconds ?? defaultSessionLeaseTtlSeconds;
    // A try without an answer, such as one Ctrl-C ended, may still have taken the lease.
    const unanswered = new Set<string>();
    this.unansweredSessionLeases = { takeover, sessionIds: unanswered };
    type LeaseCall = ReturnType<typeof acquireSessionLease>;
    const trackUnanswered = async (id: string, call: () => LeaseCall): LeaseCall => {
      unanswered.add(id);
      const result = await call();
      unanswered.delete(id);
      return result;
    };
    const acquireFor = (id: string): LeaseCall =>
      trackUnanswered(id, () => acquireSessionLease(opened.client, opened.flow, id, holder, ttlSeconds));
    this.listenForTerminationSignals();
    const attemptTakeover = (): LeaseCall => trackUnanswered(sessionId, () => takeover.attempt(sessionId));
    if ((await this.waitForPlainChatLease(sessionId, attemptTakeover)) === "interrupted") {
      return interruptedExitCode;
    }
    for (const line of await takeover.completeTakeover(sessionId)) {
      this.log(line);
    }
    let lostLeaseLine: string | undefined;
    const chat = new TerminalChat(opened.client, opened.flow, sessionId, this.terminalInput(), this.io.stdout, {
      mediaStore: opened.mediaStore,
      homeDirectory: this.environment.homeDirectory,
    });
    const renewal = new SessionLeaseRenewal(holder.holderId, ttlSeconds, acquireFor, (lostSessionId, lease) => {
      lostLeaseLine = leaseLostLine(lostSessionId, describeLeaseHolder(lease));
      chat.stop();
    });
    this.heldSessionLease = { takeover, sessionId, renewal };
    renewal.renew(sessionId);
    if (opened.isResumed) {
      this.io.stdout.write(`Resumed session ${sessionId}.\n`);
    }
    this.io.stdout.write(
      `Session ${sessionId}. Type a message; /image <path> attaches an image to the next one; /exit quits. Ctrl-C cancels a turn, and again exits.\n`,
    );
    let isFailed: boolean;
    try {
      isFailed = await chat.run();
    } catch (error: unknown) {
      if (lostLeaseLine === undefined) {
        throw error;
      }
      // The stopped chat's in-flight call may fail too; the lost lease is why it ended.
      this.writeLogLine(`the chat ended after its lease was lost: ${describe(error)}`);
      isFailed = true;
    }
    if (lostLeaseLine !== undefined) {
      // Another terminal holds the session now, so nothing is released.
      this.heldSessionLease = undefined;
      this.io.stdout.write("\n");
      this.log(lostLeaseLine);
      return 1;
    }
    this.io.stdout.write(`\nSession ${sessionId} keeps running. Resume it with: dex-agents-${this.harness.name} --resume ${sessionId}\n`);
    return isFailed ? 1 : 0;
  }

  /** Waits for the session's lease; Ctrl-C ends the wait. */
  private async waitForPlainChatLease(
    sessionId: string,
    attempt: () => ReturnType<typeof acquireSessionLease>,
  ): Promise<"acquired" | "interrupted"> {
    const interrupt = new AbortController();
    const onInterrupt = (): void => interrupt.abort();
    process.on("SIGINT", onInterrupt);
    try {
      return await waitForSessionLease({
        sessionId,
        attempt,
        output: this.io.stderr,
        isAnimated: isAnimatedWaitingOutput(this.io.stderr, this.io.environment.TERM),
        interruptSignal: interrupt.signal,
        isFinalFailure: (error) => error instanceof SessionWorkerStartError,
      });
    } finally {
      process.off("SIGINT", onInterrupt);
    }
  }

  /** Releases the plain chat's lease before the Worker and Client stop; expiry is the fallback. */
  private async releaseHeldSessionLease(): Promise<void> {
    const held = this.heldSessionLease;
    const unanswered = this.unansweredSessionLeases;
    this.heldSessionLease = undefined;
    this.unansweredSessionLeases = undefined;
    held?.renewal.stop();
    const releases: Promise<void>[] = [];
    // Another environment's session is handed back by expiry instead; see SessionTakeover.leave.
    if (held !== undefined) {
      releases.push(held.takeover.leave(held.sessionId));
    }
    for (const sessionId of unanswered?.sessionIds ?? []) {
      if (unanswered !== undefined && sessionId !== held?.sessionId) {
        releases.push(unanswered.takeover.leave(sessionId));
      }
    }
    if (releases.length > 0) {
      this.writeLogLine(`releasing the session lease: sessions=${releases.length}`);
    }
    await Promise.all(releases);
  }

  /**
   * Ends the plain chat on SIGTERM and SIGHUP in order: release the lease, close the Worker and the Client, stop a
   * started `dexcli dev`, then exit with 128 plus the signal number.
   */
  private listenForTerminationSignals(): void {
    // This sequence replaces the started server's own, which would stop it before the release.
    this.startedDexcliDev.releaseSignals();
    for (const signal of ["SIGTERM", "SIGHUP"] as const) {
      const listener = (): void => {
        if (this.terminationSignal !== undefined) {
          // A second signal means: stop waiting, as the started server's own stop does.
          this.startedDexcliDev.killAndExit(128 + constants.signals[this.terminationSignal]);
          return;
        }
        this.terminationSignal = signal;
        void this.exitOnTerminationSignal(signal);
      };
      this.terminationListeners.set(signal, listener);
      process.on(signal, listener);
    }
  }

  private async exitOnTerminationSignal(signal: "SIGTERM" | "SIGHUP"): Promise<void> {
    // After SIGHUP the terminal is gone; its write errors must not end the command before the stop.
    for (const stream of [process.stdin, process.stdout, process.stderr]) {
      stream.on("error", () => undefined);
    }
    this.writeLogLine(`ending the command on ${signal}`);
    try {
      await this.close();
      await waitForQueuedOutput([this.io.stdout, this.io.stderr]);
    } finally {
      process.exit(128 + constants.signals[signal]);
    }
  }

  private removeTerminationListeners(): void {
    for (const [signal, listener] of this.terminationListeners) {
      process.off(signal, listener);
    }
    this.terminationListeners.clear();
  }

  /**
   * Runs the chat in the terminal UI when this run selects it and `dexcli` speaks a common contract.
   * @returns How the terminal UI chat ended, or `undefined` for the plain chat.
   */
  private async chatInTerminalUi(
    effective: EffectiveConfig,
    loaded: LoadedConfig,
    server: DexServerConnection,
    connection: DexConnectionSettings,
  ): Promise<TerminalUiChatOutcome | undefined> {
    const isSelected = isTerminalUiSelected({
      platform: process.platform,
      isPlainRequested: this.parsed.isPlainRequested,
      isInteractive: this.io.isInteractive,
      term: this.io.environment.TERM,
      harnessTerminalInterface: this.harness.terminalInterface ?? "dexcli",
    });
    if (!isSelected) {
      return undefined;
    }
    let dexcli: string;
    try {
      dexcli = resolve(await resolveDexcliExecutable(this.environment, this.log));
    } catch (error: unknown) {
      if (error instanceof DexcliVariableError) {
        throw error;
      }
      this.log(`Plain chat: ${describe(error)}.`);
      return undefined;
    }
    const choice = await negotiateTerminalUi(dexcli, "chat");
    if ("plainReason" in choice) {
      this.log(`Plain chat: ${choice.detail}.`);
      return undefined;
    }
    // The terminal UI reads the terminal from now on; the launcher must not.
    this.input?.close();
    this.input = undefined;
    const launcher = new TerminalUiChatLauncher(
      {
        commandName: `dex-agents-${this.harness.name}`,
        harnessName: this.harness.name,
        harnessDefaultModel: this.harness.defaultModel,
        suggestedModels: this.harness.suggestedModels ?? [],
        commandVersion: commandPackageVersion(),
        dexcli,
        start: {
          type: "start",
          effective,
          configPath: loaded.configPath,
          isFromFile: loaded.isFromFile,
          server,
          connection,
          resumeSessionId: this.parsed.resumeSessionId ?? null,
        },
        configPath: loaded.isFromFile ? loaded.configPath : null,
        homeDirectory: this.environment.homeDirectory,
        agentsDirectory: this.environment.agentsDirectory,
        logFilePath: this.logFilePath(),
        startedDexcliDevLines: this.startedDexcliDevLines,
        environment: this.environment.childProcessVariables,
        dexConnectionVariables: this.environment.dexConnectionVariables ?? {},
        isBrowserOpeningEnabled: this.environment.isBrowserOpeningEnabled !== false,
        commandSignals: {
          release: () => this.startedDexcliDev.releaseSignals(),
          reclaim: () => this.startedDexcliDev.reclaimSignals(),
        },
        openInBrowser: launchBrowser,
      },
      this.log,
    );
    return launcher.run();
  }

  /**
   * Opens the Worker half of a chat: the media store, the connectors, the local tools, the session Flow, the Worker,
   * the session graph upload, and the new or resumed session. The plain chat and the Worker host both call it.
   *
   * A resumed session that another execution environment started keeps its Flow type: the Worker polls that Flow
   * type instead of this environment's, and starts only when {@link SessionTakeover.attempt} finds the session free.
   *
   * @param effective - The effective configuration.
   * @param loaded - The loaded configuration file.
   * @param server - The Dex server.
   * @param connection - How to verify and authenticate to the Dex server.
   * @param options - The session to resume, whether to open the Connectors page, the tool process group registry of
   * a Worker host, and the process ID that the lease shows to a waiting terminal.
   * @returns The opened session; {@link close} releases it.
   * @throws {UsageError} When the session to resume does not exist, is closed, or belongs to another harness.
   * @throws {DexConnectionSettingsError} When the CA file or the bearer token file is unusable.
   */
  public async openSessionWorker(
    effective: EffectiveConfig,
    loaded: LoadedConfig,
    server: DexServerConnection,
    connection: DexConnectionSettings,
    options: {
      readonly resumeSessionId: string | undefined;
      readonly isOpeningConnectorsPage: boolean;
      readonly toolProcessGroups: ToolProcessGroupRegistry | undefined;
      readonly leaseHolderProcessId: number;
    },
  ): Promise<OpenedSessionWorker> {
    const connectionOptions = this.dexConnectionOptions(server, connection);
    this.mediaStore ??= await createMediaStore(effective.media, this.environment.agentsDirectory);
    const turns = new TurnConfigResolver(
      effective,
      readFileInstructions(effective, loaded.configPath, this.environment.homeDirectory),
      (warnings) => this.log(`Session settings keys ignored: ${warnings.join(", ")}`),
      (flowId) => this.readTurnSessionSettings(flowId),
      this.harness.supportedReasoningEfforts ?? [],
    );
    const workspace = this.prepareWorkspace(effective, loaded);
    const identity = this.sessionEnvironmentIdentity(loaded, workspace);
    const homeFlowType = namespaceSessionFlowType(this.harness.flowType, identity);
    this.blobCacheDirectory = mkdtempSync(join(tmpdir(), `dex-agents-${this.harness.name}-cache-`));
    this.blobCache = openBlobCache({ directory: this.blobCacheDirectory, maxBytes: blobCacheBytes });
    const resumeSessionId = options.resumeSessionId;
    const flowType =
      resumeSessionId === undefined
        ? homeFlowType
        : await this.callDex(effective, server, connection, () =>
            this.readResumedSessionFlowType(connectionOptions, connection, resumeSessionId),
          );
    const mediaStore = this.mediaStore;
    // Both sources stay registered even when switched off, so they refuse calls another process requested.
    const connectorsSource = await this.connectConnectors(effective, server, turns, options.isOpeningConnectorsPage, mediaStore);
    const connectorSource = connectorsSource ?? new DisabledConnectorsToolSource();
    this.localTools = new LocalToolSource(
      workspace,
      this.offeredLocalToolGroups(effective),
      turns,
      this.log,
      options.toolProcessGroups,
    );
    const toolSources: ToolSource[] = [connectorSource, this.localTools];
    const context: HarnessContext = {
      flowType,
      config: effective,
      workspace,
      instructions: turns.currentTurn().instructions,
      harnessOptions: effective.harness_options,
      toolSources,
      mediaStore,
      isSessionSummaryIndexed: isSessionSummaryIndexed(effective),
      resolveTurn: (sessionSettings) => turns.resolveTurn(sessionSettings),
      currentTurn: () => turns.currentTurn(),
      resolveToolStepPermissions: (step) => turns.resolveToolStep(step),
    };
    const flow = this.harness.createSessionFlow(context);
    requireSessionSummarySetting(flow, context.isSessionSummaryIndexed === true);
    if (flow.getFlowType() !== flowType) {
      throw new Error(`the harness built a ${flow.getFlowType()} Flow; build it with HarnessContext.flowType ${flowType}`);
    }
    turns.installOn(flow.runner);
    this.sessionFlow = flow;
    const registry = new Registry([flow]);
    // The Client exists before the Worker starts, because tool Steps read their turn's settings through it.
    const client = this.constructDexClient(registry, connectionOptions, connection);
    this.client = client;
    const leaseHolder = newSessionLeaseHolder(options.leaseHolderProcessId, homeFlowType);
    const fence = new SessionEnvironmentFence(client, flow, homeFlowType, leaseHolder.holderId);
    fence.installOn(flow.runner);
    this.worker = new Worker(registry, this.blobCache, {
      ...connectionOptions,
      pull: { appWorkerID: `dex-agents-${this.harness.name}-${hostname()}-${process.pid}`, maxConcurrentTasks: 8 },
    });
    mkdirSync(this.environment.agentsDirectory, { recursive: true });
    this.libraryConsole = new LibraryConsoleRedirection(this.logFilePath(), this.log);
    const worker = this.worker;
    let workerStart: Promise<void> | undefined;
    let isWorkerStarted = false;
    const startWorker = (): Promise<void> => {
      workerStart ??= this.callDex(effective, server, connection, async () => {
        await worker.start();
        await worker.waitPullRegistered({ timeoutMs: 30_000 });
        isWorkerStarted = true;
      });
      return workerStart;
    };
    if (flowType === homeFlowType) {
      await startWorker();
    }
    await this.uploadSessionFlowDefinition(client, flowType);
    const startEnvironment = sessionStartEnvironment(identity);
    const sessionId =
      resumeSessionId ??
      (await this.callDex(effective, server, connection, async () => {
        const newId = newSessionId(this.harness);
        await startAgentSession(client, flow, newId, { startEnvironment });
        return newId;
      }));
    const takeover = new SessionTakeover({
      client,
      flow,
      fence,
      holder: leaseHolder,
      ttlSeconds: this.environment.sessionLeaseTtlSeconds ?? defaultSessionLeaseTtlSeconds,
      homeFlowType,
      workspace,
      startWorker: () =>
        startWorker().catch((error: unknown) => {
          throw new SessionWorkerStartError(describe(error), { cause: error });
        }),
      isWorkerStarted: () => isWorkerStarted,
    });
    const connectorsUrl = effective.connectors.mcp === "auto" ? server.autoConnectorsMcpUrl : effective.connectors.mcp;
    return {
      client: this.client,
      flow,
      flowType,
      leaseHolder,
      takeover,
      sessionId,
      isResumed: resumeSessionId !== undefined,
      startEnvironment,
      mediaStore,
      connectors:
        connectorsSource === undefined || connectorsUrl === null ? undefined : { source: connectorsSource, url: connectorsUrl },
      workspace,
    };
  }

  /** Returns the Client and Worker options of this command: its TLS options and its one credential. */
  private dexConnectionOptions(server: DexServerConnection, connection: DexConnectionSettings): DexConnectionOptions {
    return createDexConnectionOptions(server.serverAddress, connection, this.environment.dexConnectionVariables ?? {});
  }

  /** Constructs a Client, naming the TLS setting that its constructor refused. */
  private constructDexClient(
    registry: Registry,
    options: DexConnectionOptions,
    connection: DexConnectionSettings,
  ): Client {
    if (this.blobCache === undefined) {
      throw new Error("the blob cache opens before the Client");
    }
    try {
      return new Client(registry, this.blobCache, options);
    } catch (error: unknown) {
      throw describeRefusedTlsOptions(error, connection.tls) ?? error;
    }
  }

  /** Runs Dex calls, turning a refused credential or an unreachable server into a one-line error. */
  private async callDex<T>(
    effective: EffectiveConfig,
    server: DexServerConnection,
    connection: DexConnectionSettings,
    call: () => Promise<T>,
  ): Promise<T> {
    try {
      return await call();
    } catch (error: unknown) {
      if (error instanceof UsageError) {
        throw error;
      }
      const description =
        describeRefusedDexCredential(error, connection.auth, server.serverAddress) ??
        describeUnavailableDexServer(error, effective.dex.server, server.serverAddress);
      throw description === undefined ? error : new DescribedDexFailure(description, { cause: error });
    }
  }

  /**
   * Uploads the session graph so Dex Web shows the session without `--flow-rendering-dir`.
   *
   * The Client uploads it instead of `WorkerOptions.flowDefinitions`, because the Worker's upload writes its outcome
   * and errors to the terminal. The outcome and any failure go to the log file only, as in the Python CLI core.
   */
  private async uploadSessionFlowDefinition(client: Client, flowType: string): Promise<void> {
    const document = sessionFlowDefinitionDocument({ flowType, actionPermission: sessionActionPermission });
    let timer: NodeJS.Timeout | undefined;
    const timeout = new Promise<never>((_, reject) => {
      timer = setTimeout(
        () => reject(new Error(`no answer within ${sessionFlowDefinitionUploadTimeoutMs} ms`)),
        sessionFlowDefinitionUploadTimeoutMs,
      );
    });
    let line: string;
    try {
      const [result] = await Promise.race([client.uploadFlowDefinitions([document]), timeout]);
      line = `session Flow definition ${result?.outcome ?? "unspecified"}: flowType=${flowType} storedDigest=${result?.storedDigest ?? ""}`;
    } catch (error: unknown) {
      line = sessionFlowDefinitionUploadFailureLine(flowType, error);
    } finally {
      clearTimeout(timer);
    }
    this.writeLogLine(line);
  }

  /** Appends one timestamped line to the log file. */
  private writeLogLine(line: string): void {
    try {
      mkdirSync(this.environment.agentsDirectory, { recursive: true });
      appendFileSync(this.logFilePath(), `${new Date().toISOString()} ${line}\n`);
    } catch {
      // An unwritable log file loses only this diagnostic line; the chat must still start.
    }
  }

  /** `~/.dex/agents/<harness>.log`, the log file the Python CLI core also writes. */
  private logFilePath(): string {
    return join(this.environment.agentsDirectory, `${this.harness.name}.log`);
  }

  /** Reads a session's committed turn settings through the command's Client. */
  private async readTurnSessionSettings(flowId: string): Promise<TurnSessionSettings | undefined> {
    if (this.client === undefined || this.sessionFlow === undefined) {
      throw new Error("tool Steps run only after the session Flow and Client exist");
    }
    const [stored] = await this.client.getAttributes(flowId, this.sessionFlow.turnSessionSettings);
    return stored;
  }

  /**
   * Returns the execution environment this command's session Flow type hashes: the host, the user, the configuration
   * file, and the workspace. So only Workers of the same execution environment take a session's Steps, unless a
   * command of another environment took the session over, and editing tool settings in the file keeps its sessions.
   */
  private sessionEnvironmentIdentity(loaded: LoadedConfig, workspace: string): SessionEnvironmentIdentity {
    return {
      hostName: hostname(),
      userId: currentUserId(),
      configIdentity: loaded.isFromFile ? realpathSync(loaded.configPath) : defaultsConfigIdentity,
      workspace,
    };
  }

  /**
   * Returns the Flow type of the session to resume, which may be another execution environment's, before the
   * session Flow exists: a Client without Flows describes it.
   * @throws {UsageError} When the session does not exist, is closed, or belongs to another harness.
   */
  private async readResumedSessionFlowType(
    connectionOptions: DexConnectionOptions,
    connection: DexConnectionSettings,
    resumeSessionId: string,
  ): Promise<string> {
    const describingClient = this.constructDexClient(new Registry([]), connectionOptions, connection);
    let info;
    try {
      info = await describingClient.describeFlow(resumeSessionId);
    } catch (error: unknown) {
      if (error instanceof FlowNotFoundError) {
        throw new UsageError(`session ${resumeSessionId} does not exist; list sessions with dex-agents-${this.harness.name} sessions`);
      }
      throw error;
    } finally {
      await describingClient.close().catch(() => undefined);
    }
    if (!isHarnessSessionFlowType(this.harness.flowType, info.flowType)) {
      throw new UsageError(otherHarnessSessionError(resumeSessionId, info.flowType, `dex-agents-${this.harness.name}`));
    }
    if (info.status !== "running") {
      throw new UsageError(`session ${resumeSessionId} is ${info.status}, so it cannot continue`);
    }
    return info.flowType;
  }

  private async connectConnectors(
    effective: EffectiveConfig,
    server: DexServerConnection,
    turns: TurnConfigResolver,
    isOpeningConnectorsPage: boolean,
    mediaStore: MediaStore,
  ): Promise<ConnectorsMcpToolSource | undefined> {
    if (effective.connectors.enabled !== null && effective.connectors.enabled.length === 0) {
      return undefined;
    }
    const isAuto = effective.connectors.mcp === "auto";
    const url = isAuto ? server.autoConnectorsMcpUrl : effective.connectors.mcp;
    if (url === null) {
      this.log(
        "Connectors are off: connectors.mcp is auto, but dex.server names a server, so no dexcli dev provides one. Set connectors.mcp to its URL.",
      );
      return undefined;
    }
    const availability = await probeConnectorsMcp(url);
    if (!availability.isAvailable && !isAuto) {
      throw new Error(`the connectors MCP server is unavailable: ${availability.reason}`);
    }
    const hint = this.connectorsUnavailableHint(server);
    const isToolSetFixed = this.harness.isToolSetFixedAtBuild === true;
    const describeUnavailable = (unavailability: ConnectorsMcpUnavailability): string => {
      if (!isToolSetFixed) {
        return connectorsUnavailableLine(unavailability, hint);
      }
      const lead = isConnectorsMcpAccessDenied(unavailability)
        ? connectorsAccessDeniedSentence(unavailability.httpStatus ?? 0)
        : "The connectors MCP server is unavailable.";
      return `${lead} ${hint} ${this.restartForConnectorTools()}`;
    };
    const source = new ConnectorsMcpToolSource(url, turns, this.log, mediaStore, {
      startupAvailability: availability,
      writeLogLine: (line) => this.writeLogLine(line),
      describeUnavailable,
    });
    if (!availability.isAvailable) {
      this.writeLogLine(`connectors MCP server ${url} is unavailable: ${availability.reason}`);
      this.log(describeUnavailable(availability));
      return source;
    }
    let identities: ReadonlyMap<string, ConnectorToolIdentity>;
    try {
      identities = await source.readIdentities();
    } catch (error: unknown) {
      this.writeLogLine(`connectors MCP server ${url} could not be listed: ${describe(error)}`);
      // The source prints the one unavailability line and stays quiet while the next turns fail the same way.
      await source.listTools();
      return source;
    }
    await this.reportUnauthorizedConnectors(effective, identities, server, isOpeningConnectorsPage);
    if (isToolSetFixed) {
      // Outside a model call the listing uses the file's configuration; a failure prints its one line.
      await source.listTools();
    }
    return source;
  }

  /** Says where the error and the connections are, for a terminal line about an unavailable server. */
  private connectorsUnavailableHint(server: DexServerConnection): string {
    const page = server.webUrl === null ? "the Dex Web Connectors page" : `${server.webUrl}/v1/connectors`;
    return `See ${this.logFilePath()} for the error and ${page} for connections.`;
  }

  private restartForConnectorTools(): string {
    return (
      `dex-agents-${this.harness.name} fixes its tools when it starts, so this run has no connector tools. ` +
      "Restart it once the connectors MCP server answers."
    );
  }

  private async reportUnauthorizedConnectors(
    effective: EffectiveConfig,
    identities: ReadonlyMap<string, ConnectorToolIdentity>,
    server: DexServerConnection,
    isOpeningConnectorsPage: boolean,
  ): Promise<string[]> {
    const missing = (effective.connectors.enabled ?? [])
      .filter(
        (entry) =>
          ![...identities.values()].some((identity) =>
            isCoveredByConnectorEntry(entry, identity.connector, identity.connection),
          ),
      )
      .map((entry) => (entry.connection === null ? entry.connector : `${entry.connector}/${entry.connection}`));
    if (missing.length === 0) {
      return missing;
    }
    const page = server.webUrl === null ? "the Dex Web Connectors page" : `${server.webUrl}/v1/connectors`;
    this.log(`Not authorized yet: ${missing.join(", ")}. Add them with "+ Add" on ${page}.`);
    if (
      isOpeningConnectorsPage &&
      this.environment.isBrowserOpeningEnabled !== false &&
      this.io.isInteractive &&
      server.webUrl !== null
    ) {
      openInBrowser(`${server.webUrl}/v1/connectors`);
    }
    return missing;
  }

  private async listConnectors(effective: EffectiveConfig, server: DexServerConnection): Promise<number> {
    const url = effective.connectors.mcp === "auto" ? server.autoConnectorsMcpUrl : effective.connectors.mcp;
    if (url === null) {
      this.io.stdout.write("connectors.mcp is auto, but dex.server names a server; set connectors.mcp to its URL.\n");
      return 1;
    }
    const availability = await probeConnectorsMcp(url);
    if (!availability.isAvailable) {
      this.io.stdout.write(`Connectors MCP server ${url} is unavailable: ${availability.reason}\n`);
      return 1;
    }
    const turns = new TurnConfigResolver(effective, null, () => undefined, () => Promise.resolve(undefined));
    const source = new ConnectorsMcpToolSource(url, turns, this.log);
    const identities = await source.readIdentities();
    const operationsByConnection = new Map<string, string[]>();
    for (const identity of identities.values()) {
      const key = `${identity.connector}/${identity.connection}`;
      const operations = operationsByConnection.get(key) ?? [];
      operations.push(
        identity.operationName ??
          (identity.operation === null ? identity.toolName : canonicalOperationName(identity.operation)),
      );
      operationsByConnection.set(key, operations);
    }
    this.io.stdout.write(`Connectors MCP server: ${url}\nAuthorized connections:\n`);
    if (operationsByConnection.size === 0) {
      this.io.stdout.write("  (none)\n");
    }
    for (const [connection, operations] of [...operationsByConnection].sort(([left], [right]) => (left < right ? -1 : 1))) {
      const isEnabled =
        effective.connectors.enabled === null ||
        effective.connectors.enabled.some((entry) => {
          const [connector = "", name = ""] = connection.split("/", 2);
          return isCoveredByConnectorEntry(entry, connector, name);
        });
      this.io.stdout.write(`  ${connection}${isEnabled ? "" : " (not enabled in the configuration)"}: ${operations.join(", ")}\n`);
    }
    const missing = await this.reportUnauthorizedConnectors(effective, identities, server, false);
    if (missing.length > 0) {
      this.io.stdout.write(`Configured but not authorized: ${missing.join(", ")}\n`);
    }
    return 0;
  }

  private async listSessions(
    effective: EffectiveConfig,
    loaded: LoadedConfig,
    server: DexServerConnection,
    connection: DexConnectionSettings,
  ): Promise<number> {
    const connectionOptions = this.dexConnectionOptions(server, connection);
    const workspace = this.prepareWorkspace(effective, loaded);
    const identity = this.sessionEnvironmentIdentity(loaded, workspace);
    const flowType = namespaceSessionFlowType(this.harness.flowType, identity);
    const registry = new Registry([]);
    this.blobCacheDirectory = mkdtempSync(join(tmpdir(), `dex-agents-${this.harness.name}-cache-`));
    this.blobCache = openBlobCache({ directory: this.blobCacheDirectory, maxBytes: blobCacheBytes });
    const client = this.constructDexClient(registry, connectionOptions, connection);
    this.client = client;
    const { sessions: loadedSessions, hasMoreSessions } = await this.callDex(effective, server, connection, () =>
      collectSessionsForList((nextPageToken) => listAgentSessions(client, flowType, { nextPageToken })),
    );
    if (loadedSessions.length === 0 && !hasMoreSessions) {
      this.io.stdout.write(`No ${flowType} sessions.\n`);
      return 0;
    }
    // A session an earlier release started has no summary fields; "—" stands for each one.
    for (const session of sortAgentSessionsByLastActivity(loadedSessions)) {
      const columns = [
        session.sessionId,
        session.status,
        session.startedAt.toISOString(),
        session.lastActivityAt?.toISOString() ?? "—",
        session.turnCount === null ? "—" : String(session.turnCount),
        session.title ?? "—",
      ];
      this.io.stdout.write(`${columns.join("\t")}\n`);
    }
    if (hasMoreSessions) {
      this.io.stdout.write(
        `More sessions exist; this list sorts the first ${loadedSessions.length} that Dex returns: ` +
          "running sessions first, then the most recently closed and started.\n",
      );
    }
    this.io.stdout.write(`Resume one with: dex-agents-${this.harness.name} --resume <session>\n`);
    return 0;
  }

  private prepareWorkspace(effective: EffectiveConfig, loaded: LoadedConfig): string {
    const workspace = expandConfigPath(effective.workspace, loaded.configPath, this.environment.homeDirectory);
    mkdirSync(workspace, { recursive: true });
    return realpathSync.native(workspace);
  }

  private offeredLocalToolGroups(effective: EffectiveConfig): ReadonlySet<LocalToolGroup> {
    const replaced = new Set(this.harness.replacedLocalTools ?? []);
    return new Set(
      localToolGroups.filter(
        (group) =>
          !replaced.has(group) && (group === "shell" ? effective.local_tools.shell.enabled : effective.local_tools[group]),
      ),
    );
  }

  private terminalInput(): TerminalInput {
    this.input ??= new TerminalInput(this.io.stdin, this.io.stdout, this.linesTypedAhead);
    return this.input;
  }

  private async askInitQuestionsAtTerminal(): Promise<InitAnswers | null> {
    try {
      return await askInitQuestions(this.harness, (question) => this.ask(question), this.log);
    } finally {
      // Closing leaves raw mode, so Ctrl-C during startup ends the process until the chat reads again.
      this.linesTypedAhead = this.input?.close() ?? [];
      this.input = undefined;
    }
  }

  /** Returns the answer, or `null` after Ctrl-C or the end of input. */
  private async ask(question: string): Promise<string | null> {
    this.io.stdout.write(question);
    const input = this.terminalInput();
    const interrupt = new AbortController();
    input.onInterrupt(() => interrupt.abort());
    const line = await input.nextLine(interrupt.signal);
    if (line === lineWaitAborted) {
      this.isInitInterrupted = true;
      return null;
    }
    return line;
  }
}

/** The configuration identity of a run without a configuration file. */
const defaultsConfigIdentity = "defaults";

/** The directory under `~/.dex/agents` that `media.store: auto` keeps images in. */
const mediaDirectoryName = "media";

/** A media store that may hold connections, which `close` releases. */
type ClosableMediaStore = MediaStore & { close?(): void };

/** The SDK Worker's task-stream reconnect messages; issue #1657 tracks a logger option to replace this. */
const workerReconnectMessage =
  /^(Worker task stream disconnected|Worker task stream did not register|Worker task registration failed|send Worker (task|keepalive) Pong)\b/;

/** Logs library `console.warn` and `console.error` calls, and shows their first line unless it is reconnect noise. */
export class LibraryConsoleRedirection {
  private readonly originalWarn = console.warn;
  private readonly originalError = console.error;

  public constructor(
    private readonly logFilePath: string,
    private readonly showLine: (line: string) => void,
  ) {
    console.warn = (...values: unknown[]) => this.handle("WARN", values);
    console.error = (...values: unknown[]) => this.handle("ERROR", values);
  }

  public restore(): void {
    console.warn = this.originalWarn;
    console.error = this.originalError;
  }

  private handle(level: string, values: readonly unknown[]): void {
    // A Dex refusal's server details may echo the credential, as in the SDK's Flow definition upload warning.
    const text = format(...values.map((value) => (isObject(value) ? (describeDexRefusalWithoutDetails(value) ?? value) : value)));
    try {
      appendFileSync(this.logFilePath, `${new Date().toISOString()} ${level} ${text}\n`);
    } catch {
      // An unwritable log file loses only this diagnostic line.
    }
    if (!workerReconnectMessage.test(text)) {
      this.showLine(`${text.split("\n", 1)[0] ?? ""} (details in ${this.logFilePath})`);
    }
  }
}

/**
 * Builds the store of `media.store`: the local directory `~/.dex/agents/media` for `auto`, else the S3 bucket.
 * The S3 store loads only for an `s3://` store, so the default store needs no AWS SDK.
 */
async function createMediaStore(media: MediaConfig, agentsDirectory: string): Promise<ClosableMediaStore> {
  try {
    if (media.store === "auto") {
      return new LocalDirectoryMediaStore(resolve(agentsDirectory, mediaDirectoryName));
    }
    let s3: typeof import("@superdurable/dex-agents/s3");
    try {
      s3 = await import("@superdurable/dex-agents/s3");
    } catch (error: unknown) {
      if ((error as NodeJS.ErrnoException).code !== "ERR_MODULE_NOT_FOUND") {
        throw error;
      }
      throw new UsageError(
        `media.store ${media.store} needs the @aws-sdk/client-s3 package next to @superdurable/dex-agents; install it with: npm install @aws-sdk/client-s3`,
      );
    }
    return s3.S3MediaStore.fromStoreUri(
      media.store,
      media.endpoint === null ? {} : { endpoint: media.endpoint, forcePathStyle: true },
    );
  } catch (error: unknown) {
    if (error instanceof TypeError || error instanceof RangeError) {
      throw new UsageError(`media.store ${media.store}: ${error.message}`);
    }
    throw error;
  }
}

/**
 * The execution environment a session's Steps must run in, as {@link namespaceSessionFlowType} hashes it. The CLI core
 * also records it in each new session's start input.
 *
 * @since 0.5.0
 */
export interface SessionEnvironmentIdentity {
  /** The host name. */
  readonly hostName: string;
  /** The user: the numeric user ID where the platform has one, else the user name. */
  readonly userId: string;
  /** The configuration file's real path, or `defaults` without a file. */
  readonly configIdentity: string;
  /** The workspace's canonical real path. */
  readonly workspace: string;
}

/** How long the command waits for the session graph upload before it logs a failure and continues. */
const sessionFlowDefinitionUploadTimeoutMs = 10_000;

/**
 * The permission every session Action requires in the session graph that the command uploads for Dex Web.
 *
 * In Dex Web, choose it in Working as to send a CLI session messages and answer its approvals and questions. Every
 * CLI core uses this text, so CLI cores of two languages that share a session Flow type upload the same graph.
 *
 * @since 0.5.0
 */
export const sessionActionPermission = "dex-agents-cli.operate";

/** The number of base36 characters in a session Flow type suffix. */
const flowTypeSuffixLength = 6;
/** 36 to the power of {@link flowTypeSuffixLength}: the number of distinct suffixes. */
const flowTypeSuffixCount = 36n ** BigInt(flowTypeSuffixLength);

/**
 * Returns the session Flow type of a harness in one execution environment: `<flowType>-<suffix>`.
 *
 * The suffix is 6 lowercase base36 characters (`0-9a-z`). Take the SHA-256 of
 * `<host name>\n<user>\n<configuration identity>\n<workspace>` in UTF-8. Read its first 8 bytes as an unsigned
 * big-endian integer, reduce it modulo 36^6, write it in base36, and left-pad it with `0` to 6 characters. Tool
 * settings, such as the shell allow list, are not part of it, so they can change without breaking `--resume`.
 *
 * @example
 * ```ts
 * const identity = { hostName: "host", userId: "501", configIdentity: "defaults", workspace: "/home/a/w" };
 * namespaceSessionFlowType("Agent", identity); // "Agent-ujnoef"
 * ```
 *
 * @param harnessFlowType - {@link AgentHarness.flowType}.
 * @param identity - The host, user, configuration file, and workspace.
 * @returns The session Flow type.
 * @since 0.5.0
 */
export function namespaceSessionFlowType(harnessFlowType: string, identity: SessionEnvironmentIdentity): string {
  const hashInput = `${identity.hostName}\n${identity.userId}\n${identity.configIdentity}\n${identity.workspace}`;
  const digest = createHash("sha256").update(hashInput, "utf8").digest();
  const suffix = (digest.readBigUInt64BE(0) % flowTypeSuffixCount).toString(36).padStart(flowTypeSuffixLength, "0");
  return `${harnessFlowType}-${suffix}`;
}

/** Returns the hashed identity plus `user_name`, recorded as the session's `startEnvironment`. */
function sessionStartEnvironment(identity: SessionEnvironmentIdentity): Record<string, string> {
  const userName = currentUserName();
  return {
    host_name: identity.hostName,
    user_id: identity.userId,
    config_identity: identity.configIdentity,
    workspace: identity.workspace,
    ...(userName === undefined ? {} : { user_name: userName }),
  };
}

/** Returns the numeric user ID where the platform has one, else the user name. */
function currentUserId(): string {
  return typeof process.getuid === "function" ? String(process.getuid()) : userInfo().username;
}

/** Returns the user name, or `undefined` when the user ID has none, as in some containers. */
function currentUserName(): string | undefined {
  try {
    return userInfo().username;
  } catch {
    return undefined;
  }
}

function parseCommandArguments(argv: readonly string[]): ParsedArguments {
  const { values, positionals } = parseArgs({
    args: [...argv],
    allowPositionals: true,
    strict: true,
    options: {
      config: { type: "string", short: "c" },
      yes: { type: "boolean", short: "y" },
      resume: { type: "string", short: "r" },
      model: { type: "string", short: "m" },
      plain: { type: "boolean" },
      help: { type: "boolean", short: "h" },
    },
  });
  if (positionals.length > 1) {
    throw new UsageError(`unexpected arguments: ${positionals.slice(1).join(" ")}`);
  }
  const subcommand = positionals[0] ?? "run";
  if (!subcommands.includes(subcommand as Subcommand)) {
    throw new UsageError(`unknown subcommand ${subcommand}`);
  }
  if (values.model !== undefined && values.model.trim() === "") {
    throw new UsageError("--model must not be empty");
  }
  return {
    subcommand: subcommand as Subcommand,
    configPath: values.config,
    isYes: values.yes === true,
    resumeSessionId: values.resume,
    model: values.model,
    isPlainRequested: values.plain === true,
    isHelpRequested: values.help === true,
  };
}

function usage(harness: AgentHarness): string {
  const command = `dex-agents-${harness.name}`;
  return [
    `Usage: ${command} [run|init|connectors|sessions] [options]`,
    "",
    "  run          Chat with the agent in this terminal (the default).",
    "  init         Create or replace the configuration file.",
    "  connectors   List the authorized connections and the configured ones.",
    "  sessions     List this command's sessions.",
    "",
    `  -c, --config <file>   Configuration file (default ~/.dex/agents/${harness.name}.yaml).`,
    "  -y, --yes             Write and use the defaults instead of asking.",
    "  -r, --resume <id>     Continue a session.",
    "  -m, --model <id>      Override the configured model.",
    "      --plain           Chat in plain text, without the terminal UI.",
    "  -h, --help            Show this help.",
  ].join("\n");
}

function newSessionId(harness: AgentHarness): string {
  const timestamp = new Date().toISOString().replace(/[-:]/g, "").replace(/\..*$/, "").replace("T", "-");
  return `${harness.name}-${timestamp}-${randomBytes(3).toString("hex")}`;
}

function openInBrowser(url: string): void {
  void launchBrowser(url);
}

/** How long a browser opener may run before it counts as having opened the page. */
const browserOpenerWaitMs = 4_000;

/**
 * Opens a URL with the platform's browser opener.
 * @returns Whether the opener started and did not fail within 4 s.
 */
function launchBrowser(url: string): Promise<boolean> {
  const command = process.platform === "darwin" ? "open" : process.platform === "win32" ? "explorer" : "xdg-open";
  return new Promise((resolve) => {
    const child = spawn(command, [url], { stdio: "ignore", detached: true });
    const timer = setTimeout(() => resolve(true), browserOpenerWaitMs);
    timer.unref();
    child.once("error", () => {
      clearTimeout(timer);
      resolve(false);
    });
    child.once("exit", (code) => {
      clearTimeout(timer);
      resolve(code === 0);
    });
    child.unref();
  });
}

/** A closed terminal fails writes with EIO; the command must still stop the dexcli dev it started. */
function ignoreWriteErrorsOfClosedTerminal(stream: Writable): void {
  if ((stream as { isTTY?: boolean }).isTTY !== true) {
    return;
  }
  stream.on("error", (error: NodeJS.ErrnoException) => {
    if (error.code !== "EIO") {
      throw error;
    }
  });
}

function describe(error: unknown): string {
  return describeFailureWithoutDexDetails(error);
}

/**
 * Returns the log line of a failed session graph upload; a Dex refusal leaves out the server's details.
 * @param flowType - The session Flow type.
 * @param error - The failure.
 */
export function sessionFlowDefinitionUploadFailureLine(flowType: string, error: unknown): string {
  return `session Flow definition upload failed; Dex Web shows this session only with --flow-rendering-dir: flowType=${flowType} ${describe(error)}`;
}

function isObject(value: unknown): value is object {
  return typeof value === "object" && value !== null;
}
