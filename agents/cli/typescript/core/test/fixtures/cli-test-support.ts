// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { spawn, type ChildProcess } from "node:child_process";
import { existsSync, mkdirSync, mkdtempSync, realpathSync, writeFileSync } from "node:fs";
import { hostname, tmpdir, userInfo } from "node:os";
import { join } from "node:path";
import type { Readable } from "node:stream";
import { setTimeout as delay } from "node:timers/promises";
import { fileURLToPath } from "node:url";

import {
  AgentSessionTestClient,
  AgentTestEnvironment,
  type PrivateDexEngine,
} from "@superdurable/dex-agents/testing";

import {
  namespaceSessionFlowType,
  resolveConfig,
  TurnConfigResolver,
  type AgentHarness,
  type HarnessContext,
} from "../../src/index.js";
import {
  fixedToolSetScriptedHarness,
  scriptedHarness,
  scriptedHarnessDirectoryVariable,
  scriptedHarnessVariantVariable,
  scriptedHarnessWithoutImages,
  shellReplacingScriptedHarness,
} from "./scripted-harness.js";

const cliEntryPath = fileURLToPath(new URL("./scripted-harness-cli.js", import.meta.url));
const defaultWaitMs = 90_000;

/**
 * Collects a stream's text and waits for patterns in it, each search starting after the previous match.
 */
export class TextCollector {
  private text = "";
  private cursor = 0;

  /**
   * Starts collecting.
   * @param stream - The stream.
   */
  public constructor(stream: Readable) {
    stream.setEncoding("utf8");
    stream.on("data", (chunk: string) => {
      this.text += chunk;
    });
  }

  /** All text so far. */
  public get all(): string {
    return this.text;
  }

  /**
   * Waits until the text after the cursor contains a pattern, then moves the cursor past it.
   * @param pattern - Text or a regular expression.
   * @param timeoutMs - How long to wait.
   * @returns The matched text.
   */
  public async waitFor(pattern: string | RegExp, timeoutMs = defaultWaitMs): Promise<string> {
    const deadline = Date.now() + timeoutMs;
    for (;;) {
      const rest = this.text.slice(this.cursor);
      const match = typeof pattern === "string" ? (rest.includes(pattern) ? pattern : null) : (pattern.exec(rest)?.[0] ?? null);
      if (match !== null) {
        this.cursor += rest.indexOf(match) + match.length;
        return match;
      }
      if (Date.now() >= deadline) {
        throw new Error(`timed out waiting for ${String(pattern)}; output after the cursor:\n${rest}`);
      }
      await delay(50);
    }
  }
}

/** A CLI command running as a child process in its own process group. */
export class CliProcess {
  /** stdout. */
  public readonly stdout: TextCollector;
  /** stderr. */
  public readonly stderr: TextCollector;
  private readonly exited: Promise<number | null>;

  private constructor(private readonly child: ChildProcess) {
    this.stdout = new TextCollector(child.stdout as Readable);
    this.stderr = new TextCollector(child.stderr as Readable);
    this.exited = new Promise((resolve) => child.once("exit", (code) => resolve(code)));
  }

  /**
   * Starts the scripted harness command.
   * @param home - The test home.
   * @param commandArguments - Arguments after the command name.
   * @returns The process.
   */
  public static start(home: TestHome, commandArguments: readonly string[] = []): CliProcess {
    const child = spawn(process.execPath, [cliEntryPath, ...commandArguments], {
      env: home.environment(),
      stdio: ["pipe", "pipe", "pipe"],
      detached: true,
    });
    return new CliProcess(child);
  }

  /**
   * Writes one line to stdin.
   * @param line - The line, without the newline.
   */
  public writeLine(line: string): void {
    this.child.stdin?.write(`${line}\n`);
  }

  /** Sends SIGINT, as Ctrl-C does. */
  public interrupt(): void {
    this.child.kill("SIGINT");
  }

  /**
   * Kills the command's whole process group with SIGKILL, as a crash would.
   * @returns A Promise that resolves after it exited.
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
    let result: number | null | "timeout";
    try {
      result = await Promise.race([this.exited, timeout]);
    } finally {
      // A pending timer would keep the test process alive after the suite ends.
      clearTimeout(timer);
    }
    if (result === "timeout") {
      throw new Error(`the command did not exit; stdout:\n${this.stdout.all}\nstderr:\n${this.stderr.all}`);
    }
    return result;
  }

  /**
   * Waits for the session line and returns the session ID.
   * @returns The session ID.
   */
  public async waitForSession(): Promise<string> {
    const line = await this.stdout.waitFor(/Session (\S+)\. Type a message/);
    return /Session (\S+)\./.exec(line)?.[1] ?? "";
  }
}

/** A temporary home directory with its own `~/.dex`, workspace, and marker directory. */
export class TestHome {
  /** The home directory. */
  public readonly directory: string;
  /** The scripted model's marker directory. */
  public readonly markerDirectory: string;

  /**
   * Creates the home.
   * @param ports - The Dex and Dex Web ports that `dex.server: auto` uses.
   */
  public constructor(
    private readonly ports: { readonly dexPort: number; readonly webPort: number },
    /** The harness the command runs. */
    public readonly harness: AgentHarness = scriptedHarness,
  ) {
    this.directory = mkdtempSync(join(tmpdir(), "dex-agents-cli-home-"));
    this.markerDirectory = join(this.directory, "markers");
    mkdirSync(this.markerDirectory, { recursive: true });
  }

  /** `~/.dex/agents/scripted.yaml`. */
  public get configPath(): string {
    return join(this.directory, ".dex", "agents", "scripted.yaml");
  }

  /** `~/dex-agent-workspace`. */
  public get workspace(): string {
    return join(this.directory, "dex-agent-workspace");
  }

  /**
   * Writes the configuration file.
   * @param yaml - Its text.
   */
  public writeConfig(yaml: string): void {
    mkdirSync(join(this.directory, ".dex", "agents"), { recursive: true });
    writeFileSync(this.configPath, yaml);
  }

  /** Whether the configuration file exists. */
  public hasConfig(): boolean {
    return existsSync(this.configPath);
  }

  /**
   * Returns the session Flow type the command uses with this home's configuration.
   * @returns The Flow type.
   */
  public sessionFlowType(workspace = this.workspace): string {
    mkdirSync(workspace, { recursive: true });
    return namespaceSessionFlowType(this.harness.flowType, {
      hostName: hostname(),
      userId: typeof process.getuid === "function" ? String(process.getuid()) : userInfo().username,
      configIdentity: this.hasConfig() ? realpathSync(this.configPath) : "defaults",
      workspace: realpathSync.native(workspace),
    });
  }

  /**
   * Returns the command's environment.
   * @returns The variables.
   */
  public environment(): NodeJS.ProcessEnv {
    const dexcli = process.env.DEXCLI;
    if (dexcli === undefined || dexcli === "") {
      throw new Error("DEXCLI must name the dexcli binary");
    }
    return {
      PATH: process.env.PATH ?? "",
      HOME: this.directory,
      // A dexcli dev the command starts runs no published Connectors MCP release.
      DEX_CONNECTORS_MCP_BINARY: join(this.directory, "dex-connectors-mcp-not-installed"),
      // Tests never open the user's web browser.
      DEX_AGENTS_OPEN_BROWSER: "0",
      DEXCLI: dexcli,
      ...(process.env.DEX_SERVER_ZD_BINARY === undefined ? {} : { DEX_SERVER_ZD_BINARY: process.env.DEX_SERVER_ZD_BINARY }),
      ...(process.env.PLAYWRIGHT_BROWSERS_PATH === undefined
        ? {}
        : { PLAYWRIGHT_BROWSERS_PATH: process.env.PLAYWRIGHT_BROWSERS_PATH }),
      DEX_AGENTS_DEX_PORT: String(this.ports.dexPort),
      DEX_AGENTS_WEB_PORT: String(this.ports.webPort),
      [scriptedHarnessDirectoryVariable]: this.markerDirectory,
      ...(this.harness === shellReplacingScriptedHarness ? { [scriptedHarnessVariantVariable]: "shell-replacing" } : {}),
      ...(this.harness === scriptedHarnessWithoutImages ? { [scriptedHarnessVariantVariable]: "without-images" } : {}),
      ...(this.harness === fixedToolSetScriptedHarness ? { [scriptedHarnessVariantVariable]: "fixed-tool-set" } : {}),
    };
  }

  /** `~/.dex/agents/media`, where `media.store: auto` keeps images. */
  public get mediaDirectory(): string {
    return join(this.directory, ".dex", "agents", "media");
  }
}

/**
 * Returns the Dex and Dex Web ports of a private engine.
 * @param engine - The engine.
 * @returns The ports.
 */
export function enginePorts(engine: PrivateDexEngine): { dexPort: number; webPort: number } {
  return {
    dexPort: Number(engine.serverAddress.split(":").at(-1)),
    webPort: Number(new URL(engine.webUrl).port),
  };
}

/**
 * Clients for the scripted harness's sessions, without a Worker: one per session Flow type, which depends on the
 * test home's configuration file.
 */
export class SessionClients {
  private readonly environments = new Map<string, Promise<{ environment: AgentTestEnvironment; sessions: AgentSessionTestClient }>>();

  /**
   * Creates the clients.
   * @param engine - The engine.
   */
  public constructor(private readonly engine: PrivateDexEngine) {}

  /**
   * Returns the client for the sessions of a test home's command.
   * @param home - The test home; its configuration file must exist, or the command ran without one.
   * @returns The client.
   */
  public async of(home: TestHome): Promise<AgentSessionTestClient> {
    const flowType = home.sessionFlowType();
    let opened = this.environments.get(flowType);
    if (opened === undefined) {
      opened = this.open(flowType, home.harness);
      this.environments.set(flowType, opened);
    }
    return (await opened).sessions;
  }

  /**
   * Closes every client.
   * @returns A Promise that resolves after they closed.
   */
  public async close(): Promise<void> {
    for (const opened of this.environments.values()) {
      await (await opened).environment.close();
    }
    this.environments.clear();
  }

  private async open(
    flowType: string,
    harness: AgentHarness,
  ): Promise<{ environment: AgentTestEnvironment; sessions: AgentSessionTestClient }> {
    const effective = resolveConfig({}, harness.configSection, harness.defaultModel);
    const turns = new TurnConfigResolver(effective, null, () => undefined, () => Promise.resolve(undefined));
    const context: HarnessContext = {
      flowType,
      config: effective,
      workspace: tmpdir(),
      instructions: null,
      harnessOptions: {},
      toolSources: [],
      resolveTurn: (settings) => turns.resolveTurn(settings),
      currentTurn: () => turns.currentTurn(),
      resolveToolStepPermissions: (step) => turns.resolveToolStep(step),
    };
    const flow = harness.createSessionFlow(context);
    const environment = await AgentTestEnvironment.start({
      serverAddress: this.engine.serverAddress,
      flows: [flow],
      isWorkerInProcess: false,
    });
    return { environment, sessions: new AgentSessionTestClient(environment.client, flow) };
  }
}
