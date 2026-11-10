// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { spawn } from "node:child_process";
import { createHash, randomBytes } from "node:crypto";
import { mkdirSync, mkdtempSync, realpathSync, rmSync } from "node:fs";
import { hostname, tmpdir } from "node:os";
import { join } from "node:path";
import type { Readable, Writable } from "node:stream";
import { parseArgs } from "node:util";

import { Client, FlowNotFoundError, Registry, Worker, openBlobCache, type BlobCache } from "@superdurable/dex";
import { startAgentSession, type ToolSource } from "@superdurable/dex-agents";

import type { AgentHarness, HarnessContext, LocalToolGroup } from "./agent-harness.js";
import { localToolGroups } from "./agent-harness.js";
import { ConfigError, canonicalOperationName, isCoveredByConnectorEntry, type EffectiveConfig } from "./cli-config.js";
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
  type LoadedConfig,
} from "./config-file.js";
import {
  ConnectorsMcpToolSource,
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
 * import { runAgentCli } from "@superdurable/dex-agents-cli";
 * import { claudeSdkHarness } from "./claude-sdk-harness.js";
 *
 * process.exit(await runAgentCli(claudeSdkHarness));
 * ```
 * @param harness - The command's harness.
 * @param io - The process; defaults to {@link processAgentCliIo}.
 * @returns The exit code: 0 on success, 1 for a failure, 2 for invalid arguments or configuration.
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

/** One command run, owning everything it opened. */
class AgentCliCommand {
  private readonly environment: CliEnvironment;
  private input: TerminalInput | undefined;
  private worker: Worker | undefined;
  private client: Client | undefined;
  private blobCache: BlobCache | undefined;
  private blobCacheDirectory: string | undefined;
  private localTools: LocalToolSource | undefined;

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
      const loaded = await this.initConfig(configPath);
      return loaded === undefined ? 1 : 0;
    }
    const loaded = await this.loadConfig(configPath);
    if (loaded === undefined) {
      return 1;
    }
    const effective = this.applyOverrides(loaded.effective);
    const server = await resolveDexServer(effective.dex.server, this.environment, this.log);
    if (server.webUrl !== null) {
      this.log(`Dex Web: ${server.webUrl}`);
    }
    switch (this.parsed.subcommand) {
      case "connectors":
        return this.listConnectors(effective, server);
      case "sessions":
        return this.listSessions(loaded, server);
      default:
        return this.chat(effective, loaded, server);
    }
  }

  public async close(): Promise<void> {
    this.input?.close();
    await this.client?.close().catch(() => undefined);
    await this.worker?.close().catch(() => undefined);
    await this.localTools?.close().catch(() => undefined);
    this.blobCache?.close();
    if (this.blobCacheDirectory !== undefined) {
      rmSync(this.blobCacheDirectory, { recursive: true, force: true });
    }
  }

  private async initConfig(configPath: string): Promise<LoadedConfig | undefined> {
    const answers = this.parsed.isYes || !this.io.isInteractive
      ? defaultInitAnswers(this.harness)
      : await askInitQuestions(this.harness, (question) => this.ask(question), this.log);
    if (answers === null) {
      this.log("Input ended before the configuration was complete; nothing was written.");
      return undefined;
    }
    const loaded = writeConfigFile(configPath, answers, this.harness);
    this.log(`Wrote ${configPath}`);
    return loaded;
  }

  private async loadConfig(configPath: string): Promise<LoadedConfig | undefined> {
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
    );
    const workspace = this.prepareWorkspace(effective, loaded);
    const flowType = this.sessionFlowType(loaded);
    const connectorSource = await this.connectConnectors(effective, server, turns, true);
    const offeredGroups = this.offeredLocalToolGroups(effective);
    const toolSources: ToolSource[] = connectorSource === undefined ? [] : [connectorSource];
    if (offeredGroups.size > 0) {
      this.localTools = new LocalToolSource(workspace, offeredGroups, turns, this.log);
      toolSources.push(this.localTools);
    }
    const context: HarnessContext = {
      flowType,
      config: effective,
      workspace,
      instructions: turns.currentTurn().instructions,
      harnessOptions: effective.harness_options,
      toolSources,
      resolveTurn: (sessionSettings) => turns.resolveTurn(sessionSettings),
      currentTurn: () => turns.currentTurn(),
    };
    const flow = this.harness.createSessionFlow(context);
    if (flow.getFlowType() !== flowType) {
      throw new Error(`the harness built a ${flow.getFlowType()} Flow; build it with HarnessContext.flowType ${flowType}`);
    }
    turns.installOn(flow.runner);
    const registry = new Registry([flow]);
    this.blobCacheDirectory = mkdtempSync(join(tmpdir(), `dex-agents-${this.harness.name}-cache-`));
    this.blobCache = openBlobCache({ directory: this.blobCacheDirectory, maxBytes: blobCacheBytes });
    const connection = {
      serverAddress: server.serverAddress,
      ...(server.isTls ? { flowServiceTls: {} } : {}),
    };
    this.worker = new Worker(registry, this.blobCache, {
      ...connection,
      pull: { appWorkerID: `dex-agents-${this.harness.name}-${hostname()}-${process.pid}`, maxConcurrentTasks: 8 },
    });
    await this.worker.start();
    await this.worker.waitPullRegistered({ timeoutMs: 30_000 });
    this.client = new Client(registry, this.blobCache, connection);
    const sessionId = await this.openSession(this.client, flow, flowType);
    this.io.stdout.write(
      `Session ${sessionId}. Type a message; /exit quits. Ctrl-C cancels a turn, and again exits.\n`,
    );
    const chat = new TerminalChat(this.client, flow, sessionId, this.terminalInput(), this.io.stdout);
    const isFailed = await chat.run();
    this.io.stdout.write(`\nSession ${sessionId} keeps running. Resume it with: dex-agents-${this.harness.name} --resume ${sessionId}\n`);
    return isFailed ? 1 : 0;
  }

  /**
   * Returns this command's session Flow type: the harness's Flow type with a hash of the host name and the
   * configuration file's real path, or "defaults" without a file. So only Workers with the same workspace and
   * policies take a session's Steps, and editing the file keeps its sessions.
   */
  private sessionFlowType(loaded: LoadedConfig): string {
    const configIdentity = loaded.isFromFile ? realpathSync(loaded.configPath) : defaultsConfigIdentity;
    return namespaceSessionFlowType(this.harness.flowType, hostname(), configIdentity);
  }

  private async openSession(
    client: Client,
    flow: Parameters<typeof startAgentSession>[1],
    flowType: string,
  ): Promise<string> {
    const resumeSessionId = this.parsed.resumeSessionId;
    if (resumeSessionId === undefined) {
      const sessionId = newSessionId(this.harness);
      await startAgentSession(client, flow, sessionId, {});
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
        `session ${resumeSessionId} is a ${info.flowType} session, but this machine and configuration run ${flowType} sessions; resume it with the configuration that started it`,
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
    const source = new ConnectorsMcpToolSource(url, turns, this.log);
    if (!availability.isAvailable) {
      this.log(`Connector tools are unavailable for now; each turn tries again: ${availability.reason}`);
      return source;
    }
    await this.reportUnauthorizedConnectors(effective, await source.readIdentities(), server, isOpeningConnectorsPage);
    return source;
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
    if (isOpeningConnectorsPage && this.io.isInteractive && server.webUrl !== null) {
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
    const turns = new TurnConfigResolver(effective, null, () => undefined);
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

  private async listSessions(loaded: LoadedConfig, server: DexServerConnection): Promise<number> {
    const flowType = this.sessionFlowType(loaded);
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
    this.input ??= new TerminalInput(this.io.stdin, this.io.stdout);
    return this.input;
  }

  private async ask(question: string): Promise<string | null> {
    this.io.stdout.write(question);
    const line = await this.terminalInput().nextLine();
    return line === lineWaitAborted ? null : line;
  }
}

/** The configuration identity of a run without a configuration file. */
const defaultsConfigIdentity = "defaults";

/**
 * Returns the session Flow type of a harness on one host and configuration: `<flowType>-<hash>`, where the hash
 * is the first 12 hexadecimal digits of the SHA-256 of `<host name>\n<configuration identity>` in UTF-8. The
 * configuration identity is the real path of the configuration file, or `defaults` without a file.
 *
 * @param harnessFlowType - {@link AgentHarness.flowType}.
 * @param hostName - The host name.
 * @param configIdentity - The configuration file's real path, or `defaults`.
 * @returns The session Flow type.
 * @since 0.5.0
 */
export function namespaceSessionFlowType(harnessFlowType: string, hostName: string, configIdentity: string): string {
  const hash = createHash("sha256").update(`${hostName}\n${configIdentity}`, "utf8").digest("hex").slice(0, 12);
  return `${harnessFlowType}-${hash}`;
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
