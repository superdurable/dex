// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { spawn, type ChildProcess } from "node:child_process";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, realpathSync, statSync, writeFileSync } from "node:fs";
import { createServer, type Server } from "node:net";
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
import { namespaceSessionFlowType, resolveConfig, TurnConfigResolver, type HarnessContext } from "@superdurable/dex-agents-cli-core";

import { createPiSessionFlow, piHarness } from "../../src/index.js";
import type { MockMessagesApi } from "./mock-messages-api.js";

const binPath = fileURLToPath(new URL("../../src/bin.js", import.meta.url));
const defaultWaitMs = 120_000;

/** The API key every command gets unless a test removes it. */
export const mockApiKey = "sk-ant-mock-pi-cli-key";

/** Collects a stream's text and waits for patterns in it, each search starting after the previous match. */
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

/** The command running as a child process in its own process group. */
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
   * Starts the command.
   * @param home - The test home.
   * @param commandArguments - Arguments after the command name.
   * @param nodeArguments - Node options before the command, such as `--import` of a test hook.
   * @returns The process.
   */
  public static start(
    home: TestHome,
    commandArguments: readonly string[] = [],
    nodeArguments: readonly string[] = [],
  ): CliProcess {
    const child = spawn(process.execPath, [...nodeArguments, binPath, ...commandArguments], {
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

  /**
   * Kills the command's whole process group.
   * @returns A Promise that resolves after it exited.
   */
  public async kill(): Promise<void> {
    if (this.child.exitCode === null && this.child.signalCode === null && this.child.pid !== undefined) {
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
    const timeout = delay(timeoutMs).then(() => "timeout" as const);
    const result = await Promise.race([this.exited, timeout]);
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

/** A port that accepts connections and closes them at once: every proxied request fails, so only the mock is reached. */
export class RefusingProxy {
  private constructor(private readonly server: Server) {}

  /**
   * Starts the proxy on a free loopback port; it closes every connection at once.
   * @returns The proxy.
   */
  public static async start(): Promise<RefusingProxy> {
    const server = createServer((socket) => socket.destroy());
    await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
    return new RefusingProxy(server);
  }

  /** The proxy URL. */
  public get url(): string {
    const address = this.server.address();
    return typeof address === "object" && address !== null ? `http://127.0.0.1:${address.port}` : "";
  }

  /**
   * Stops the proxy.
   * @returns A Promise that resolves after it closed.
   */
  public async stop(): Promise<void> {
    await new Promise<void>((resolve) => this.server.close(() => resolve()));
  }
}

/** A temporary home directory for the command, pointed at one dexcli dev and the scripted Messages API. */
export class TestHome {
  /** The home directory. */
  public readonly directory: string;
  /** The provider variables every process gets: unless a test removes it, the mock key. */
  public readonly providerEnvironment: Record<string, string>;

  /**
   * Creates the home.
   * @param ports - The Dex and Dex Web ports that `dex.server: auto` uses.
   * @param api - The scripted Messages API.
   * @param proxyUrl - The refusing proxy every other request goes to.
   */
  public constructor(
    private readonly ports: { readonly dexPort: number; readonly webPort: number },
    private readonly api: MockMessagesApi,
    private readonly proxyUrl: string,
  ) {
    this.directory = realpathSync(mkdtempSync(join(tmpdir(), "dex-agents-pi-home-")));
    this.providerEnvironment = { ANTHROPIC_API_KEY: mockApiKey };
  }

  /** `~/.dex/agents/pi.yaml`. */
  public get configPath(): string {
    return join(this.directory, ".dex", "agents", "pi.yaml");
  }

  /** `~/dex-agent-workspace`. */
  public get workspace(): string {
    return join(this.directory, "dex-agent-workspace");
  }

  /**
   * Writes the configuration file; JSON is valid YAML. Its `pi` section points the anthropic provider at the
   * scripted Messages API, unless the values set `provider_base_urls` themselves.
   * @param values - The configuration.
   */
  public writeConfig(values: Readonly<Record<string, unknown>>): void {
    mkdirSync(join(this.directory, ".dex", "agents"), { recursive: true });
    const section = (values["pi"] ?? {}) as Readonly<Record<string, unknown>>;
    const pi = { provider_base_urls: { anthropic: this.api.baseUrl }, ...section };
    writeFileSync(this.configPath, `${JSON.stringify({ ...values, pi }, null, 2)}\n`);
  }

  /**
   * Returns the session Flow type the command uses with this home's configuration.
   * @returns The Flow type.
   */
  public sessionFlowType(): string {
    mkdirSync(this.workspace, { recursive: true });
    return namespaceSessionFlowType(piHarness.flowType, {
      hostName: hostname(),
      userId: typeof process.getuid === "function" ? String(process.getuid()) : userInfo().username,
      configIdentity: existsSync(this.configPath) ? realpathSync(this.configPath) : "defaults",
      workspace: realpathSync.native(this.workspace),
    });
  }

  /**
   * Returns the command's environment: no provider variable of the host.
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
      TMPDIR: tmpdir(),
      DEXCLI: dexcli,
      ...(process.env.DEX_SERVER_ZD_BINARY === undefined ? {} : { DEX_SERVER_ZD_BINARY: process.env.DEX_SERVER_ZD_BINARY }),
      DEX_AGENTS_DEX_PORT: String(this.ports.dexPort),
      DEX_AGENTS_WEB_PORT: String(this.ports.webPort),
      HTTPS_PROXY: this.proxyUrl,
      HTTP_PROXY: this.proxyUrl,
      NO_PROXY: "127.0.0.1,localhost",
      ...this.providerEnvironment,
    };
  }

  /**
   * Returns the text of the command's state files under `~/.dex`.
   * @returns The concatenated text.
   */
  public stateText(): string {
    return listFiles(join(this.directory, ".dex"))
      .filter((path) => /\.(ya?ml|json|log)$/.test(path))
      .map((path) => readFileSync(path, "utf8"))
      .join("\n");
  }
}

/**
 * Lists the files under a directory, recursively; `[]` when it does not exist.
 * @param directory - The directory.
 * @returns The file paths.
 */
export function listFiles(directory: string): string[] {
  if (!existsSync(directory)) {
    return [];
  }
  return readdirSync(directory).flatMap((name) => {
    const path = join(directory, name);
    return statSync(path).isDirectory() ? listFiles(path) : [path];
  });
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
 * Opens a test-side client for the sessions of one command Flow type, without a Worker.
 * @param engine - The engine.
 * @param flowType - The namespaced session Flow type.
 * @returns The environment, to close, and the client.
 */
export async function openSessionClient(
  engine: PrivateDexEngine,
  flowType: string,
): Promise<{ environment: AgentTestEnvironment; sessions: AgentSessionTestClient }> {
  const effective = resolveConfig({}, piHarness.configSection, piHarness.defaultModel);
  const turns = new TurnConfigResolver(effective, null, () => undefined, () => Promise.resolve(undefined));
  const context: HarnessContext = {
    flowType,
    config: effective,
    workspace: realpathSync(tmpdir()),
    instructions: null,
    harnessOptions: {},
    toolSources: [],
    resolveTurn: (settings) => turns.resolveTurn(settings),
    currentTurn: () => turns.currentTurn(),
    resolveToolStepPermissions: (step) => turns.resolveToolStep(step),
  };
  const flow = createPiSessionFlow(context, { ANTHROPIC_API_KEY: mockApiKey });
  const environment = await AgentTestEnvironment.start({
    serverAddress: engine.serverAddress,
    flows: [flow],
    isWorkerInProcess: false,
  });
  return { environment, sessions: new AgentSessionTestClient(environment.client, flow) };
}
