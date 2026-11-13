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
import { readDexConnectionVariables } from "../cli-environment.js";
import { chatLaunchDocument, noticeText, terminalUiAuthOf } from "./launch-document.js";
import { LocalRequestRelay, refusal, type LocalRequestMethod } from "./local-request-relay.js";
import type { LocalRequestOutcome } from "./request-outcome-table.js";
import { isOpenUrlAllowed } from "./open-url-policy.js";
import { TerminalUiChannel, type ChannelMessage } from "./terminal-ui-channel.js";
import { buildTerminalUiEnvironment, terminalUiAuthVariables } from "./terminal-ui-environment.js";
import { LauncherOutputRedirect, TerminalModeGuard } from "./terminal-mode.js";

/** How long the terminal UI has, from its spawn, to send `ready`. */
export const terminalUiReadyTimeoutMs = 5_000;
/** How long the terminal UI gets between SIGTERM and SIGKILL. */
const terminalUiTermGraceMs = 2_000;
/** How long a terminal UI that closed its channel or rejected the launch gets to exit on its own. */
const contractEndGraceMs = 1_000;
/** How long a SIGINT the whole foreground group got gives the terminal UI before the launcher forwards it. */
const interruptGraceMs = 1_000;
/** How long the terminal UI gets to exit in order after a forwarded SIGINT. */
const orderlyInterruptExitMs = 2_000;
/** The fd number the channel gets in the terminal UI: the fourth stdio entry. */
const channelFdInChild = 3;
/** The relayed methods of a chat, in launch document order. */
const relayedChatMethods = ["put_image", "start_session", "list_connectors"] as const;

/** What the launcher knows before it starts the Worker host. */
export interface TerminalUiChatLaunch {
  readonly commandName: string;
  readonly harnessName: string;
  /** The harness's `defaultModel`, for the launch document's `display.models`. */
  readonly harnessDefaultModel: string | null;
  /** The harness's `suggestedModels`, for the launch document's `display.models`. */
  readonly suggestedModels: readonly string[];
  readonly commandVersion: string;
  /** The `dexcli` executable, an absolute path. */
  readonly dexcli: string;
  readonly start: WorkerHostStartRequest;
  readonly configPath: string | null;
  readonly homeDirectory: string;
  readonly agentsDirectory: string;
  readonly logFilePath: string;
  /** The lines the command printed while it started a `dexcli dev`; empty when it started none. */
  readonly startedDexcliDevLines: readonly string[];
  /** The launcher's environment, which the Worker host inherits. */
  readonly environment: NodeJS.ProcessEnv;
  /** Whether `open_url` may start a browser. */
  readonly isBrowserOpeningEnabled: boolean;
  /** The command's own signal handlers, which the handoff takes over while the terminal UI runs. */
  readonly commandSignals: { release(): void; reclaim(): void };
  /** Opens a URL in the user's browser; resolves with whether a browser opener took it. */
  readonly openInBrowser: (url: string) => Promise<boolean>;
}

/** How a terminal UI chat ended: an exit code, or the plain chat on the same session. */
export type TerminalUiChatOutcome =
  | { readonly kind: "exited"; readonly exitCode: number }
  | { readonly kind: "plain"; readonly resumeSessionId: string };

/** How the terminal UI process ended. */
interface TerminalUiExit {
  readonly code: number | null;
  readonly signal: NodeJS.Signals | null;
  /** Why it could not start, when it could not. */
  readonly error?: string;
}

/**
 * Runs one chat in `dexcli agents-tui`: starts the Worker host, hands the terminal to the terminal UI with the
 * channel and an allow-listed environment, relays local requests, and restores the terminal when the terminal UI
 * ends. A failure before `ready` continues in the plain chat on the same session.
 */
export class TerminalUiChatLauncher {
  private readonly writeLine: (line: string) => void;
  private host: WorkerHostProcess | undefined;
  /** The Worker host's log lines before its session opened, which the launch document carries as notices. */
  private readonly workerHostStartupLines: string[] = [];

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
    const host = new WorkerHostProcess(this.launch.logFilePath, this.launch.environment, (text) =>
      this.workerHostStartupLines.push(text),
    );
    this.host = host;
    const startup = await host.start(this.launch.start);
    if (startup.type !== "worker_ready") {
      // Without a terminal UI to show them in, the startup lines go to the terminal.
      for (const line of this.workerHostStartupLines) {
        this.writeLine(line);
      }
    }
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
    this.launch.commandSignals.release();
    const signals = new LauncherSignals();
    try {
      const redirect = await LauncherOutputRedirect.start(this.launch.logFilePath);
      let handoff: TerminalUiHandoff;
      try {
        handoff = new TerminalUiHandoff(this.launch, host, ready, signals, this.workerHostStartupLines);
      } catch (error: unknown) {
        // No terminal UI started, so nothing else stops the Worker host.
        redirect.close();
        await this.stopHost();
        throw error;
      }
      try {
        await handoff.finished;
      } finally {
        redirect.close();
      }
      return await this.finishHandOff(handoff, ready, terminalMode, signals);
    } finally {
      // The launcher keeps SIGTERM, SIGHUP, and SIGINT until the Worker host stopped, then hands them back.
      signals.close();
      this.launch.commandSignals.reclaim();
    }
  }

  private async finishHandOff(
    handoff: TerminalUiHandoff,
    ready: WorkerHostReady,
    terminalMode: TerminalModeGuard,
    signals: LauncherSignals,
  ): Promise<TerminalUiChatOutcome> {
    const exit = handoff.exit;
    const isAbnormalEnd =
      exit.error !== undefined || exit.signal !== null || ![0, 1, 2, 3, 130, 143].includes(exit.code ?? -1);
    if (!signals.isHangUp) {
      terminalMode.restore(isAbnormalEnd || handoff.wasStoppedByLauncher, (text) => process.stdout.write(text));
    }
    const hasHostStoppedDuringChat = handoff.hostExitCode !== undefined;
    const sessionId = ready.sessionId;
    const isFallback = !handoff.isReady && handoff.failure !== undefined;
    await this.stopHost();
    // A signal may also arrive while the Worker host stops; it decides the outcome then.
    if (isFallback && !this.isStoppedByUser(handoff, signals)) {
      this.writeLine(`Plain chat: ${handoff.failure}.`);
      return { kind: "plain", resumeSessionId: sessionId };
    }
    if (signals.isHangUp) {
      return { kind: "exited", exitCode: 129 };
    }
    // After ready, dexcli's exit summary says how to resume: /new may have switched to another session.
    if (!handoff.isReady) {
      this.writeResumeLine(sessionId);
    }
    if (handoff.wasStoppedByLauncher) {
      return { kind: "exited", exitCode: 143 };
    }
    if (exit.signal === "SIGINT" || exit.code === 130 || signals.isInterrupted) {
      return { kind: "exited", exitCode: 130 };
    }
    if (handoff.contractEnd === undefined && (exit.code === 0 || exit.code === 1)) {
      return { kind: "exited", exitCode: exit.code === 1 || hasHostStoppedDuringChat ? 1 : 0 };
    }
    const reason = handoff.contractEnd ?? `dexcli exit ${describeExit(exit)}`;
    this.writeLine(`The chat closed unexpectedly (${reason}).`);
    return { kind: "exited", exitCode: 1 };
  }

  private isStoppedByUser(handoff: TerminalUiHandoff, signals: LauncherSignals): boolean {
    return handoff.wasStoppedByLauncher || signals.isHangUp || signals.isInterrupted;
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
    const exitCode = await host.shutdown();
    if (exitCode === undefined) {
      // Its run record stays, so the next start's leftover cleanup stops it.
      this.writeLine(`The Worker host (pid ${pid ?? "unknown"}) did not stop; the next start of this command stops it.`);
      return;
    }
    if (pid !== undefined) {
      await cleanUpWorkerHostRecord(this.launch.agentsDirectory, this.launch.harnessName, pid);
    }
  }
}

/**
 * The launcher's signal handling while the terminal UI runs. SIGINT gets a handler, never `SIG_IGN`, so the
 * terminal UI does not inherit an ignored SIGINT. SIGTSTP keeps its default, so job control stops the launcher
 * with the terminal UI.
 */
class LauncherSignals {
  /** Whether the terminal hung up. */
  public isHangUp = false;
  /** Whether SIGINT reached the launcher. */
  public isInterrupted = false;
  private onTerminate: (() => void) | undefined;
  private onInterruptRequest: (() => void) | undefined;
  private isTerminateRequested = false;
  private readonly onInterrupt = (): void => {
    const isFirst = !this.isInterrupted;
    this.isInterrupted = true;
    if (isFirst) {
      this.onInterruptRequest?.();
    }
  };
  private readonly onTerm = (): void => {
    this.isTerminateRequested = true;
    this.onTerminate?.();
  };
  private onHangUpRequest: (() => void) | undefined;
  private readonly onHangUp = (): void => {
    const isFirst = !this.isHangUp;
    this.isHangUp = true;
    if (isFirst) {
      this.onHangUpRequest?.();
    }
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

  /**
   * Sets what the first SIGINT does; one that already arrived calls it at once.
   * @param handler - Ends the terminal UI in order.
   */
  public whenInterrupted(handler: () => void): void {
    this.onInterruptRequest = handler;
    if (this.isInterrupted) {
      handler();
    }
  }

  /**
   * Sets what the first SIGHUP does; one that already arrived calls it at once.
   * @param handler - Ends the terminal UI.
   */
  public whenHungUp(handler: () => void): void {
    this.onHangUpRequest = handler;
    if (this.isHangUp) {
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
  /** Settles once the terminal UI exited, or could not start. */
  public readonly finished: Promise<void>;
  /** Whether `ready` arrived. */
  public isReady = false;
  /** Why the handoff failed before `ready`, when it did. */
  public failure: string | undefined;
  /** Why the contract ended after `ready` without the terminal UI exiting first, when it did. */
  public contractEnd: string | undefined;
  /** Whether the launcher stopped the terminal UI, for SIGTERM. */
  public wasStoppedByLauncher = false;
  /** The Worker host's exit status, when it exited while the terminal UI ran. */
  public hostExitCode: number | undefined;
  /** How the terminal UI ended; set once {@link finished} settled. */
  public exit: TerminalUiExit = { code: null, signal: null };
  private readonly child: ChildProcess;
  private readonly channel: TerminalUiChannel;
  private readonly relay: LocalRequestRelay;
  private readyTimer: NodeJS.Timeout | undefined;
  private readonly queuedMessages: Record<string, unknown>[] = [];
  private hasEnded = false;
  private resolveFinished: () => void = () => undefined;

  public constructor(
    private readonly launch: TerminalUiChatLaunch,
    host: WorkerHostProcess,
    ready: WorkerHostReady,
    signals: LauncherSignals,
    workerHostStartupLines: readonly string[],
  ) {
    const methods = new Map<string, LocalRequestMethod>();
    for (const method of relayedChatMethods) {
      if (method !== "put_image" || ready.capabilities.images !== undefined) {
        methods.set(method, (params, key) => host.execute(method, params, key));
      }
    }
    methods.set("open_url", (params) => this.openUrl(params));
    const connectionAuth = launch.start.connection.auth;
    const terminalUiAuth = terminalUiAuthOf(connectionAuth);
    const authVariables = terminalUiAuthVariables(connectionAuth, readDexConnectionVariables(launch.environment));
    const document = chatLaunchDocument({
      commandName: launch.commandName,
      harnessName: launch.harnessName,
      commandVersion: launch.commandVersion,
      serverAddress: launch.start.server.serverAddress,
      tls: launch.start.connection.tls,
      auth: terminalUiAuth,
      webUrl: launch.start.server.webUrl,
      flowType: ready.flowType,
      sessionId: ready.sessionId,
      isResumed: ready.isResumed,
      model: launch.start.effective.model,
      harnessDefaultModel: launch.harnessDefaultModel,
      suggestedModels: launch.suggestedModels,
      workspace: ready.workspace,
      configPath: launch.configPath,
      methods: [...methods.keys(), "get_request_result"],
      capabilities: ready.capabilities,
      logPath: launch.logFilePath,
      homeDirectory: launch.homeDirectory,
      startedDexcliDevLines: launch.startedDexcliDevLines,
      workerHostStartupLines,
    });
    this.finished = new Promise((resolve) => {
      this.resolveFinished = resolve;
    });
    this.child = spawn(launch.dexcli, ["agents-tui", "--channel-fd", String(channelFdInChild)], {
      stdio: ["inherit", "inherit", "inherit", "pipe"],
      env: buildTerminalUiEnvironment({ ...launch.environment, ...authVariables }, terminalUiAuth),
    });
    // A failed spawn emits "error" and "close" but no "exit"; either ends the handoff, once.
    this.child.once("exit", (code, signal) => this.end({ code, signal }));
    this.child.once("error", (error) => {
      if (this.child.pid === undefined) {
        this.end({ code: null, signal: null, error: error.message });
      }
    });
    const socket = this.child.stdio[channelFdInChild] as Duplex;
    this.channel = new TerminalUiChannel(socket, {
      onMessage: (message) => this.receive(message),
      onProtocolError: (reason) =>
        this.endContract(`the terminal UI sent a line the contract does not allow: ${reason}`, true),
      onEnd: () => this.endContract("the terminal UI closed its channel", false),
    });
    this.relay = new LocalRequestRelay(methods, true, (id, outcome) =>
      this.channel.send({ type: "response", id, ...outcome }),
    );
    try {
      this.channel.send(document);
      this.readyTimer = setTimeout(() => {
        if (!this.isReady) {
          this.failure ??= `the terminal UI was not ready within ${terminalUiReadyTimeoutMs / 1000} seconds`;
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
      signals.whenInterrupted(() => void this.interrupt());
      // A hangup of the terminal reaches dexcli too; a SIGHUP to the launcher alone must still end it.
      signals.whenHungUp(() => void this.stopUnlessExiting());
    } catch (error: unknown) {
      // dexcli already runs: stop it, so the pre-ready failure path stops the Worker host and goes on in plain text.
      this.failure ??= `the launcher could not start the terminal UI: ${error instanceof Error ? error.message : String(error)}`;
      this.channel.close();
      void this.stop();
    }
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
        this.failure ??= sentence(message.message) ?? "the terminal UI rejected the launch";
        void this.stopUnlessExiting();
      }
      return;
    }
    this.relay.handleMessage(message);
  }

  /**
   * The channel ended or broke: the contract is over, so the terminal UI must end too.
   * @param reason - Why, for the line the launcher prints.
   * @param isFailure - Whether it is a failure however the terminal UI then exits: a protocol error is; an EOF is
   * not when the terminal UI then exits by itself, which is how it ends a chat.
   */
  private endContract(reason: string, isFailure: boolean): void {
    if (this.hasEnded) {
      return;
    }
    if (!this.isReady) {
      this.failure ??= reason;
    } else if (isFailure) {
      this.contractEnd ??= reason;
    }
    void this.stopUnlessExiting(() => {
      if (this.isReady) {
        this.contractEnd ??= reason;
      }
    });
  }

  private end(exit: TerminalUiExit): void {
    if (this.hasEnded) {
      return;
    }
    this.hasEnded = true;
    this.exit = exit;
    clearTimeout(this.readyTimer);
    if (!this.isReady) {
      this.failure ??=
        exit.error === undefined
          ? `dexcli agents-tui exited with ${describeExit(exit)} before it was ready`
          : `dexcli agents-tui could not start: ${exit.error}`;
    }
    this.channel.close();
    this.resolveFinished();
  }

  private sendWhenReady(message: Record<string, unknown>): void {
    if (this.isReady) {
      this.channel.send(message);
    } else {
      this.queuedMessages.push(message);
    }
  }

  private async openUrl(params: Readonly<Record<string, unknown>>): Promise<LocalRequestOutcome> {
    const url = params.url;
    if (typeof url !== "string" || !isOpenUrlAllowed(this.launch.start.server.webUrl, url)) {
      return refusal("refused", "open_url only opens Dex Web pages.");
    }
    if (!this.launch.isBrowserOpeningEnabled) {
      return refusal("browser_off", "Opening a browser is switched off for this command.");
    }
    return (await this.launch.openInBrowser(url)) ? { result: {} } : refusal("open_failed", "No browser opened the page.");
  }

  /**
   * Ends the terminal UI in order after SIGINT reached the launcher. A SIGINT sent to the whole foreground group
   * already reached the terminal UI, so it gets a moment to exit on its own before the launcher forwards SIGINT.
   */
  private async interrupt(): Promise<void> {
    if (!(await this.hasExitedWithin(interruptGraceMs))) {
      this.child.kill("SIGINT");
      if (!(await this.hasExitedWithin(orderlyInterruptExitMs))) {
        await this.stop();
      }
    }
  }

  /**
   * Gives a terminal UI that is already exiting a moment, then stops it.
   * @param onStopping - Runs when the terminal UI did not exit on its own.
   */
  private async stopUnlessExiting(onStopping: () => void = () => undefined): Promise<void> {
    if (!(await this.hasExitedWithin(contractEndGraceMs))) {
      onStopping();
      await this.stop();
    }
  }

  private async hasExitedWithin(timeoutMs: number): Promise<boolean> {
    const deadline = Date.now() + timeoutMs;
    while (!this.hasEnded && Date.now() < deadline) {
      await delay(50, undefined, { ref: false });
    }
    return this.hasEnded;
  }

  /** Sends SIGTERM to the terminal UI, then SIGKILL after 2 s. */
  private async stop(): Promise<void> {
    if (this.hasEnded) {
      return;
    }
    this.child.kill("SIGTERM");
    if (!(await this.hasExitedWithin(terminalUiTermGraceMs))) {
      this.child.kill("SIGKILL");
    }
  }
}

function describeExit(exit: TerminalUiExit): string {
  if (exit.error !== undefined) {
    return "unknown, it could not start";
  }
  if (exit.signal !== null) {
    return String(128 + (constants.signals[exit.signal] ?? 0));
  }
  return String(exit.code ?? "unknown");
}

/** Returns a message without its final period, so it fits after `Plain chat:`. */
function sentence(value: unknown): string | undefined {
  return typeof value === "string" && value.trim() !== "" ? value.trim().replace(/\.$/, "") : undefined;
}
