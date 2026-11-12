// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { spawn, type ChildProcess } from "node:child_process";
import { constants } from "node:os";
import type { Duplex } from "node:stream";
import { setTimeout as delay } from "node:timers/promises";

import { WorkerHostProcess } from "../worker-host/worker-host-process.js";
import type { WorkerHostReady, WorkerHostStartRequest } from "../worker-host/worker-host-protocol.js";
import { cleanUpWorkerHostRecord } from "../worker-host/leftover-worker-hosts.js";
import { chatLaunchDocument, launchAuth, noticeText } from "./launch-document.js";
import { LocalRequestRelay, refusal, type LocalRequestMethod } from "./local-request-relay.js";
import { isOpenUrlAllowed } from "./open-url-policy.js";
import { TerminalUiChannel, type ChannelMessage } from "./terminal-ui-channel.js";
import { buildTerminalUiEnvironment } from "./terminal-ui-environment.js";
import { LauncherOutputRedirect, TerminalModeGuard } from "./terminal-mode.js";

/** How long the terminal UI has, from its spawn, to send `ready`. */
export const terminalUiReadyTimeoutMs = 5_000;
/** How long the terminal UI gets between SIGTERM and SIGKILL. */
const terminalUiTermGraceMs = 2_000;
/** The fd number the channel gets in the terminal UI: the fourth stdio entry. */
const channelFdInChild = 3;
/** The relayed methods of a chat, in launch document order. */
const relayedChatMethods = ["put_image", "start_session", "list_connectors"] as const;

/** What the launcher knows before it starts the Worker host. */
export interface TerminalUiChatLaunch {
  readonly commandName: string;
  readonly harnessName: string;
  readonly commandVersion: string;
  /** The `dexcli` executable, an absolute path. */
  readonly dexcli: string;
  readonly start: WorkerHostStartRequest;
  readonly configPath: string | null;
  readonly homeDirectory: string;
  readonly agentsDirectory: string;
  readonly logFilePath: string;
  /** The launcher's environment, which the Worker host inherits. */
  readonly environment: NodeJS.ProcessEnv;
  /** Whether `open_url` may start a browser. */
  readonly isBrowserOpeningEnabled: boolean;
  /** Opens a URL in the user's browser. */
  readonly openInBrowser: (url: string) => void;
}

/** How a terminal UI chat ended: an exit code, or the plain chat on the same session. */
export type TerminalUiChatOutcome =
  | { readonly kind: "exited"; readonly exitCode: number }
  | { readonly kind: "plain"; readonly resumeSessionId: string };

/** How the terminal UI process ended. */
interface TerminalUiExit {
  readonly code: number | null;
  readonly signal: NodeJS.Signals | null;
}

/**
 * Runs one chat in `dexcli agents-tui`: starts the Worker host, hands the terminal to the terminal UI with the
 * channel and an allow-listed environment, relays local requests, and restores the terminal when the terminal UI
 * ends. A failure before `ready` continues in the plain chat on the same session.
 */
export class TerminalUiChatLauncher {
  private readonly writeLine: (line: string) => void;
  private host: WorkerHostProcess | undefined;

  /**
   * Creates the launcher.
   * @param launch - The command, the configuration, and the environment.
   * @param log - Writes a line for the person at the terminal, before and after the handoff.
   */
  public constructor(
    private readonly launch: TerminalUiChatLaunch,
    log: (line: string) => void,
  ) {
    this.writeLine = log;
  }

  /**
   * Runs the chat.
   * @returns How it ended.
   */
  public async run(): Promise<TerminalUiChatOutcome> {
    const host = new WorkerHostProcess(this.launch.logFilePath, this.launch.environment, (text) => this.writeLine(text));
    this.host = host;
    const startup = await host.start(this.launch.start);
    if (startup.type === "startup_failed") {
      this.writeLine(`${this.launch.commandName}: ${startup.message}`);
      await this.stopHost();
      return { kind: "exited", exitCode: startup.exitCode };
    }
    if (startup.type === "exited") {
      this.writeLine(
        `${this.launch.commandName}: the Worker host stopped (exit ${startup.exitCode}) before the session opened; see ${this.launch.logFilePath}`,
      );
      await this.stopHost();
      return { kind: "exited", exitCode: 1 };
    }
    if (startup.isResumed) {
      this.writeLine(`Resumed session ${startup.sessionId}.`);
    }
    return this.handOff(host, startup);
  }

  private async handOff(host: WorkerHostProcess, ready: WorkerHostReady): Promise<TerminalUiChatOutcome> {
    const terminalMode = new TerminalModeGuard();
    const signals = new LauncherSignals();
    const redirect = new LauncherOutputRedirect(this.launch.logFilePath);
    let handoff: TerminalUiHandoff;
    try {
      handoff = new TerminalUiHandoff(this.launch, host, ready, signals);
      await handoff.finished;
    } finally {
      redirect.close();
      signals.close();
    }
    const exit = handoff.exit;
    const isAbnormalEnd = exit.signal !== null || ![0, 1, 2, 3, 130, 143].includes(exit.code ?? -1);
    if (!signals.isHangUp) {
      terminalMode.restore(isAbnormalEnd || handoff.wasStoppedByLauncher, (text) => process.stdout.write(text));
    }
    const hasHostStoppedDuringChat = handoff.hostExitCode !== undefined;
    const sessionId = ready.sessionId;
    if (!handoff.isReady && handoff.failure !== undefined && !handoff.wasStoppedByLauncher && !signals.isHangUp) {
      this.writeLine(`Plain chat: ${handoff.failure}.`);
      await this.stopHost();
      return { kind: "plain", resumeSessionId: sessionId };
    }
    await this.stopHost();
    if (signals.isHangUp) {
      return { kind: "exited", exitCode: 129 };
    }
    if (handoff.wasStoppedByLauncher) {
      this.writeResumeLine(sessionId);
      return { kind: "exited", exitCode: 143 };
    }
    if (exit.signal === "SIGINT" || exit.code === 130) {
      this.writeResumeLine(sessionId);
      return { kind: "exited", exitCode: 130 };
    }
    if (exit.code === 0 || exit.code === 1) {
      this.writeResumeLine(sessionId);
      return { kind: "exited", exitCode: exit.code === 1 || hasHostStoppedDuringChat ? 1 : 0 };
    }
    this.writeLine(`The chat closed unexpectedly (dexcli exit ${describeExit(exit)}).`);
    this.writeResumeLine(sessionId);
    return { kind: "exited", exitCode: 1 };
  }

  private writeResumeLine(sessionId: string): void {
    this.writeLine(`Session ${sessionId} keeps running. Resume it with: ${this.launch.commandName} --resume ${sessionId}`);
  }

  /** Stops the Worker host within its bounds, then stops what its run record still lists. */
  private async stopHost(): Promise<void> {
    const host = this.host;
    if (host === undefined) {
      return;
    }
    const pid = host.pid;
    await host.shutdown();
    if (pid !== undefined) {
      await cleanUpWorkerHostRecord(this.launch.agentsDirectory, this.launch.harnessName, pid);
    }
  }
}

/**
 * The launcher's signal handling while the terminal UI runs. SIGINT gets a handler that does nothing, never
 * `SIG_IGN`, so the terminal UI does not inherit an ignored SIGINT. SIGTSTP keeps its default, so job control stops
 * the launcher with the terminal UI.
 */
class LauncherSignals {
  /** Whether the terminal hung up. */
  public isHangUp = false;
  private onTerminate: (() => void) | undefined;
  private isTerminateRequested = false;
  private readonly onInterrupt = (): void => undefined;
  private readonly onTerm = (): void => {
    this.isTerminateRequested = true;
    this.onTerminate?.();
  };
  private readonly onHangUp = (): void => {
    this.isHangUp = true;
  };

  public constructor() {
    process.on("SIGINT", this.onInterrupt);
    process.on("SIGTERM", this.onTerm);
    process.on("SIGHUP", this.onHangUp);
  }

  /**
   * Sets what SIGTERM does; a SIGTERM that already arrived calls it at once.
   * @param handler - Stops the terminal UI.
   */
  public whenTerminated(handler: () => void): void {
    this.onTerminate = handler;
    if (this.isTerminateRequested) {
      handler();
    }
  }

  public close(): void {
    process.off("SIGINT", this.onInterrupt);
    process.off("SIGTERM", this.onTerm);
    process.off("SIGHUP", this.onHangUp);
  }
}

/** One run of the terminal UI process with its channel. */
class TerminalUiHandoff {
  /** Settles once the terminal UI exited. */
  public readonly finished: Promise<void>;
  /** Whether `ready` arrived. */
  public isReady = false;
  /** Why the handoff failed before `ready`, when it did. */
  public failure: string | undefined;
  /** Whether the launcher stopped the terminal UI, for SIGTERM. */
  public wasStoppedByLauncher = false;
  /** The Worker host's exit status, when it exited while the terminal UI ran. */
  public hostExitCode: number | undefined;
  /** How the terminal UI ended; set once {@link finished} settled. */
  public exit: TerminalUiExit = { code: null, signal: null };
  private readonly child: ChildProcess;
  private readonly channel: TerminalUiChannel;
  private readonly relay: LocalRequestRelay;
  private readonly readyTimer: NodeJS.Timeout;
  private readonly queuedMessages: Record<string, unknown>[] = [];
  private launchRejection: string | undefined;

  public constructor(
    private readonly launch: TerminalUiChatLaunch,
    host: WorkerHostProcess,
    ready: WorkerHostReady,
    signals: LauncherSignals,
  ) {
    const methods = new Map<string, LocalRequestMethod>();
    for (const method of relayedChatMethods) {
      if (method !== "put_image" || ready.capabilities.images !== undefined) {
        methods.set(method, (params, key) => host.execute(method, params, key));
      }
    }
    methods.set("open_url", (params) => Promise.resolve(this.openUrl(params)));
    const document = chatLaunchDocument({
      commandName: launch.commandName,
      harnessName: launch.harnessName,
      commandVersion: launch.commandVersion,
      serverAddress: launch.start.server.serverAddress,
      isTls: launch.start.server.isTls,
      webUrl: launch.start.server.webUrl,
      flowType: ready.flowType,
      sessionId: ready.sessionId,
      isResumed: ready.isResumed,
      model: launch.start.effective.model,
      workspace: ready.workspace,
      configPath: launch.configPath,
      methods: [...methods.keys(), "get_request_result"],
      capabilities: ready.capabilities,
      logPath: launch.logFilePath,
      homeDirectory: launch.homeDirectory,
    });
    this.child = spawn(launch.dexcli, ["agents-tui", "--channel-fd", String(channelFdInChild)], {
      stdio: ["inherit", "inherit", "inherit", "pipe"],
      env: buildTerminalUiEnvironment(launch.environment, launchAuth),
    });
    const socket = this.child.stdio[channelFdInChild] as Duplex;
    this.channel = new TerminalUiChannel(socket, {
      onMessage: (message) => this.receive(message),
      onProtocolError: () => undefined,
      onEnd: () => undefined,
    });
    this.relay = new LocalRequestRelay(methods, true, (id, outcome) =>
      this.channel.send({ type: "response", id, ...outcome }),
    );
    this.channel.send(document);
    this.readyTimer = setTimeout(() => {
      if (!this.isReady) {
        this.failure = `the terminal UI was not ready within ${terminalUiReadyTimeoutMs / 1000} seconds`;
        void this.stop();
      }
    }, terminalUiReadyTimeoutMs);
    host.listen({
      onLog: (text) => this.sendWhenReady({ type: "notice", level: "warning", text: noticeText(text) }),
      onStopping: () => this.sendWhenReady({ type: "worker", state: "stopping" }),
      onExit: (exitCode) => {
        this.hostExitCode = exitCode;
        this.sendWhenReady({ type: "worker", state: "stopped", exit_code: exitCode });
      },
    });
    signals.whenTerminated(() => {
      this.wasStoppedByLauncher = true;
      void this.stop();
    });
    this.finished = new Promise((resolve) => {
      this.child.once("exit", (code, signal) => {
        this.exit = { code, signal };
        clearTimeout(this.readyTimer);
        if (!this.isReady) {
          this.failure ??= this.launchRejection ?? `dexcli agents-tui exited with ${describeExit(this.exit)} before it was ready`;
        }
        this.channel.close();
        resolve();
      });
    });
    this.child.once("error", (error) => {
      this.failure ??= `dexcli agents-tui could not start: ${error.message}`;
    });
  }

  private receive(message: ChannelMessage): void {
    if (!this.isReady) {
      if (message.type === "ready") {
        this.isReady = true;
        clearTimeout(this.readyTimer);
        for (const queued of this.queuedMessages.splice(0)) {
          this.channel.send(queued);
        }
      } else if (message.type === "launch_rejected") {
        this.launchRejection = sentence(message.message) ?? "the terminal UI rejected the launch";
      }
      return;
    }
    this.relay.handleMessage(message);
  }

  private sendWhenReady(message: Record<string, unknown>): void {
    if (this.isReady) {
      this.channel.send(message);
    } else {
      this.queuedMessages.push(message);
    }
  }

  private openUrl(params: Readonly<Record<string, unknown>>) {
    const url = params.url;
    if (typeof url !== "string" || !isOpenUrlAllowed(this.launch.start.server.webUrl, url)) {
      return refusal("refused", "open_url only opens Dex Web pages.");
    }
    if (this.launch.isBrowserOpeningEnabled) {
      this.launch.openInBrowser(url);
    }
    return { result: {} };
  }

  /** Sends SIGTERM to the terminal UI, then SIGKILL after 2 s. */
  private async stop(): Promise<void> {
    if (this.child.exitCode !== null || this.child.signalCode !== null) {
      return;
    }
    this.child.kill("SIGTERM");
    await delay(terminalUiTermGraceMs, undefined, { ref: false });
    if (this.child.exitCode === null && this.child.signalCode === null) {
      this.child.kill("SIGKILL");
    }
  }
}

function describeExit(exit: TerminalUiExit): string {
  if (exit.signal !== null) {
    return String(128 + (constants.signals[exit.signal] ?? 0));
  }
  return String(exit.code ?? "unknown");
}

/** Returns a message without its final period, so it fits after `Plain chat:`. */
function sentence(value: unknown): string | undefined {
  return typeof value === "string" && value.trim() !== "" ? value.trim().replace(/\.$/, "") : undefined;
}
