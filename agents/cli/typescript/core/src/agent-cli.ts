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
import { hostname, tmpdir, userInfo } from "node:os";
import { join, resolve } from "node:path";
import type { Readable, Writable } from "node:stream";
import { parseArgs } from "node:util";

import { Client, FlowNotFoundError, Registry, Worker, openBlobCache, type BlobCache } from "@superdurable/dex";
import {
  LocalDirectoryMediaStore,
  sessionFlowDefinitionDocument,
  startAgentSession,
  type AgentFrameworkRunner,
  type AgentSessionFlow,
  type DeferredToolOutcome,
  type MediaStore,
  type ToolSource,
  type TurnSessionSettings,
} from "@superdurable/dex-agents";

import type { AgentHarness, HarnessContext, LocalToolGroup } from "./agent-harness.js";
import { localToolGroups } from "./agent-harness.js";
import {
  ConfigError,
  canonicalOperationName,
  isCoveredByConnectorEntry,
  type EffectiveConfig,
  type MediaConfig,
} from "./cli-config.js";
import { readCliEnvironment, type CliEnvironment } from "./cli-environment.js";
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
  probeConnectorsMcp,
  type ConnectorToolIdentity,
} from "./connector-tool-source.js";
import { resolveDexServer, type DexServerConnection } from "./dex-server.js";
import { LocalToolSource } from "./local-tools/local-tool-source.js";
import { TerminalChat } from "./terminal-chat.js";
import { TerminalInput, lineWaitAborted } from "./terminal-input.js";
import { TurnConfigResolver } from "./turn-config-resolver.js";

const subcommands = ["run", "init", "connectors", "sessions"] as const;
type Subcommand = (typeof subcommands)[number];
const sessionsPageSize = 50;
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

/**
 * Runs one Dex Agents CLI command for a harness: `run` (the default), `init`, `connectors`, or `sessions`.
 *
 * `run` loads `~/.dex/agents/<name>.yaml` or `--config`. Without the file, it asks the `init` questions at a
 * terminal and writes the file, writes the defaults with `--yes`, and otherwise runs on the defaults without a
 * file. It then reuses or starts the Dex server, connects the connectors MCP server, starts a Worker in this
 * process, and chats in the terminal with a new session or the `--resume` one. Exiting keeps the session.
 *
 * A harness command's bin calls it and exits with its result, because open connections can keep Node running:
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
 * @since 0.5.0
 */
export async function runAgentCli(harness: AgentHarness, io: AgentCliIo = processAgentCliIo()): Promise<number> {
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
  try {
    return await command.run();
  } catch (error: unknown) {
    if (error instanceof InitCancelledError) {
      return error.exitCode;
    }
    log(`dex-agents-${harness.name}: ${describe(error)}`);
    return error instanceof ConfigError || error instanceof UsageError ? 2 : 1;
  } finally {
    await command.close();
  }
}

interface ParsedArguments {
  readonly subcommand: Subcommand;
  readonly configPath: string | undefined;
  readonly isYes: boolean;
  readonly resumeSessionId: string | undefined;
  readonly model: string | undefined;
  readonly isHelpRequested: boolean;
}

class UsageError extends Error {}

/** Ctrl-C or the end of input during the `init` questions, after which nothing was written. */
class InitCancelledError extends Error {
  public constructor(public readonly exitCode: number) {
    super("the init questions were cancelled");
  }
}

/** The shell's exit code for a command that Ctrl-C ended. */
const interruptedExitCode = 130;

/** One command run, owning everything it opened. */
class AgentCliCommand {
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

  public constructor(
    private readonly harness: AgentHarness,
    private readonly io: AgentCliIo,
    private readonly parsed: ParsedArguments,
    private readonly log: (line: string) => void,
  ) {
    this.environment = readCliEnvironment(io.environment);
  }

  public async run(): Promise<number> {
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
    if (this.parsed.subcommand === "run") {
      this.mediaStore = await createMediaStore(effective.media, this.environment.agentsDirectory);
    }
    const server = await resolveDexServer(effective.dex.server, this.environment, this.log);
    if (server.webUrl !== null) {
      this.log(`Dex Web: ${server.webUrl}`);
    }
    switch (this.parsed.subcommand) {
      case "connectors":
        return this.listConnectors(effective, server);
      case "sessions":
        return this.listSessions(effective, loaded, server);
      default:
        return this.chat(effective, loaded, server);
    }
  }

  public async close(): Promise<void> {
    this.input?.close();
    await this.client?.close().catch(() => undefined);
    await this.worker?.close().catch(() => undefined);
    await this.localTools?.close().catch(() => undefined);
    this.mediaStore?.close?.();
    this.blobCache?.close();
    if (this.blobCacheDirectory !== undefined) {
      rmSync(this.blobCacheDirectory, { recursive: true, force: true });
    }
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

  private async chat(effective: EffectiveConfig, loaded: LoadedConfig, server: DexServerConnection): Promise<number> {
    const turns = new TurnConfigResolver(
      effective,
      readFileInstructions(effective, loaded.configPath, this.environment.homeDirectory),
      (warnings) => this.log(`Session settings keys ignored: ${warnings.join(", ")}`),
      (flowId) => this.readTurnSessionSettings(flowId),
    );
    const workspace = this.prepareWorkspace(effective, loaded);
    const identity = this.sessionEnvironmentIdentity(loaded, workspace);
    const flowType = namespaceSessionFlowType(this.harness.flowType, identity);
    const mediaStore = this.mediaStore;
    if (mediaStore === undefined) {
      throw new Error("the run subcommand builds the media store before the chat starts");
    }
    // Both sources stay registered even when switched off, so they refuse calls another process requested.
    const connectorSource =
      (await this.connectConnectors(effective, server, turns, true, mediaStore)) ?? new DisabledConnectorsToolSource();
    this.localTools = new LocalToolSource(workspace, this.offeredLocalToolGroups(effective), turns, this.log);
    const toolSources: ToolSource[] = [connectorSource, this.localTools];
    const context: HarnessContext = {
      flowType,
      config: effective,
      workspace,
      instructions: turns.currentTurn().instructions,
      harnessOptions: effective.harness_options,
      toolSources,
      mediaStore,
      resolveTurn: (sessionSettings) => turns.resolveTurn(sessionSettings),
      currentTurn: () => turns.currentTurn(),
      resolveToolStepPermissions: (step) => turns.resolveToolStep(step),
    };
    const flow = this.harness.createSessionFlow(context);
    if (flow.getFlowType() !== flowType) {
      throw new Error(`the harness built a ${flow.getFlowType()} Flow; build it with HarnessContext.flowType ${flowType}`);
    }
    turns.installOn(flow.runner);
    this.sessionFlow = flow;
    const registry = new Registry([flow]);
    this.blobCacheDirectory = mkdtempSync(join(tmpdir(), `dex-agents-${this.harness.name}-cache-`));
    this.blobCache = openBlobCache({ directory: this.blobCacheDirectory, maxBytes: blobCacheBytes });
    const connection = {
      serverAddress: server.serverAddress,
      ...(server.isTls ? { flowServiceTls: {} } : {}),
    };
    // The Client exists before the Worker starts, because tool Steps read their turn's settings through it.
    this.client = new Client(registry, this.blobCache, connection);
    this.worker = new Worker(registry, this.blobCache, {
      ...connection,
      pull: { appWorkerID: `dex-agents-${this.harness.name}-${hostname()}-${process.pid}`, maxConcurrentTasks: 8 },
    });
    await this.worker.start();
    await this.worker.waitPullRegistered({ timeoutMs: 30_000 });
    await this.uploadSessionFlowDefinition(this.client, flowType);
    const sessionId = await this.openSession(this.client, flow, flowType, identity);
    this.io.stdout.write(
      `Session ${sessionId}. Type a message; /image <path> attaches an image to the next one; /exit quits. Ctrl-C cancels a turn, and again exits.\n`,
    );
    const chat = new TerminalChat(this.client, flow, sessionId, this.terminalInput(), this.io.stdout, {
      mediaStore,
      homeDirectory: this.environment.homeDirectory,
    });
    const isFailed = await chat.run();
    this.io.stdout.write(`\nSession ${sessionId} keeps running. Resume it with: dex-agents-${this.harness.name} --resume ${sessionId}\n`);
    return isFailed ? 1 : 0;
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
      line = `session Flow definition upload failed; Dex Web shows this session only with --flow-rendering-dir: flowType=${flowType} ${describe(error)}`;
    } finally {
      clearTimeout(timer);
    }
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
   * file, and the workspace. So only Workers of the same execution environment take a session's Steps, and editing
   * tool settings in the file keeps its sessions.
   */
  private sessionEnvironmentIdentity(loaded: LoadedConfig, workspace: string): SessionEnvironmentIdentity {
    return {
      hostName: hostname(),
      userId: currentUserId(),
      configIdentity: loaded.isFromFile ? realpathSync(loaded.configPath) : defaultsConfigIdentity,
      workspace,
    };
  }

  private async openSession(
    client: Client,
    flow: Parameters<typeof startAgentSession>[1],
    flowType: string,
    identity: SessionEnvironmentIdentity,
  ): Promise<string> {
    const resumeSessionId = this.parsed.resumeSessionId;
    if (resumeSessionId === undefined) {
      const sessionId = newSessionId(this.harness);
      await startAgentSession(client, flow, sessionId, { startEnvironment: sessionStartEnvironment(identity) });
      return sessionId;
    }
    let info;
    try {
      info = await client.describeFlow(resumeSessionId);
    } catch (error: unknown) {
      if (error instanceof FlowNotFoundError) {
        throw new UsageError(`session ${resumeSessionId} does not exist; list sessions with dex-agents-${this.harness.name} sessions`);
      }
      throw error;
    }
    if (info.flowType !== flowType) {
      throw new UsageError(
        `session ${resumeSessionId} is a ${info.flowType} session, but this user, configuration file, and workspace run ${flowType} sessions; resume it as the user, with the configuration file and workspace, that started it`,
      );
    }
    if (info.status !== "running") {
      throw new UsageError(`session ${resumeSessionId} is ${info.status}, so it cannot continue`);
    }
    this.io.stdout.write(`Resumed session ${resumeSessionId}.\n`);
    return resumeSessionId;
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
    const source = new ConnectorsMcpToolSource(url, turns, this.log, mediaStore);
    const isToolSetFixed = this.harness.isToolSetFixedAtBuild === true;
    if (!availability.isAvailable) {
      this.log(
        isToolSetFixed
          ? `The connectors MCP server is unavailable: ${availability.reason}. ${this.restartForConnectorTools()}`
          : `Connector tools are unavailable for now; each turn tries again: ${availability.reason}`,
      );
      return source;
    }
    if (!isToolSetFixed) {
      await this.reportUnauthorizedConnectors(effective, await source.readIdentities(), server, isOpeningConnectorsPage);
      return source;
    }
    let identities: ReadonlyMap<string, ConnectorToolIdentity>;
    try {
      identities = await source.readIdentities();
    } catch (error: unknown) {
      this.log(`The connectors MCP server could not be listed: ${describe(error)}. ${this.restartForConnectorTools()}`);
      return source;
    }
    await this.reportUnauthorizedConnectors(effective, identities, server, isOpeningConnectorsPage);
    // Outside a model call the listing uses the file's configuration.
    await source.listTools();
    if (!source.isAvailable) {
      this.log(`The connectors MCP server could not be listed. ${this.restartForConnectorTools()}`);
    }
    return source;
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
  ): Promise<number> {
    const workspace = this.prepareWorkspace(effective, loaded);
    const identity = this.sessionEnvironmentIdentity(loaded, workspace);
    const flowType = namespaceSessionFlowType(this.harness.flowType, identity);
    const registry = new Registry([]);
    this.blobCacheDirectory = mkdtempSync(join(tmpdir(), `dex-agents-${this.harness.name}-cache-`));
    this.blobCache = openBlobCache({ directory: this.blobCacheDirectory, maxBytes: blobCacheBytes });
    this.client = new Client(registry, this.blobCache, {
      serverAddress: server.serverAddress,
      ...(server.isTls ? { flowServiceTls: {} } : {}),
    });
    const page = await this.client.searchFlows(`FlowType = "${flowType}"`, sessionsPageSize);
    if (page.flows.length === 0) {
      this.io.stdout.write(`No ${flowType} sessions.\n`);
      return 0;
    }
    for (const flow of page.flows) {
      this.io.stdout.write(`${flow.flowId}\t${flow.status}\t${flow.startedAt.toISOString()}\n`);
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
 * In Dex Web, choose it in Working as to answer a CLI session's approvals and questions. Every CLI core uses this
 * text, so CLI cores of two languages that share a session Flow type upload the same graph.
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
    "  -h, --help            Show this help.",
  ].join("\n");
}

function newSessionId(harness: AgentHarness): string {
  const timestamp = new Date().toISOString().replace(/[-:]/g, "").replace(/\..*$/, "").replace("T", "-");
  return `${harness.name}-${timestamp}-${randomBytes(3).toString("hex")}`;
}

function openInBrowser(url: string): void {
  const command = process.platform === "darwin" ? "open" : process.platform === "win32" ? "explorer" : "xdg-open";
  const child = spawn(command, [url], { stdio: "ignore", detached: true });
  child.once("error", () => undefined);
  child.unref();
}

function describe(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}
