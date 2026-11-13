// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { maxImageBytes, maxImagesPerMessage } from "@superdurable/dex-agents";

import type { AgentHarness } from "../agent-harness.js";
import type { AgentCliCommand, AgentCliIo, OpenedSessionWorker } from "../agent-cli.js";
import { readCliEnvironment } from "../cli-environment.js";
import type { SessionFlowCapabilities } from "../terminal-ui/launch-document.js";
import type { LocalRequestOutcome } from "../terminal-ui/request-outcome-table.js";
import { refusal } from "../terminal-ui/local-request-relay.js";
import { readProcessIdentity } from "./process-identity.js";
import { RecordedToolProcessGroups } from "./tool-process-groups.js";
import { executeWorkerHostMethod } from "./worker-host-methods.js";
import type {
  LauncherToWorkerHostMessage,
  WorkerHostStartRequest,
  WorkerHostToLauncherMessage,
} from "./worker-host-protocol.js";
import { removeWorkerHostRunRecord, workerHostRunRecordPath } from "./worker-host-run-record.js";
import { WorkerHostWatchdog } from "./worker-host-watchdog.js";

/** Builds the command object the Worker host opens its session with. */
export type WorkerHostCommandFactory = (log: (line: string) => void) => AgentCliCommand;

/**
 * Runs a command's Worker host: the hidden `__worker-host` entry that the launcher starts. It opens the session
 * the launcher asks for, serves the session's Worker and the relayed local requests, and stops when the launcher
 * asks, disconnects, or dies.
 *
 * @param harness - The command's harness.
 * @param io - The process; stdout and stderr are the command's log file.
 * @param createCommand - Builds the command object that opens the session.
 * @returns The exit code.
 */
export async function runWorkerHost(
  harness: AgentHarness,
  io: AgentCliIo,
  createCommand: WorkerHostCommandFactory,
): Promise<number> {
  if (process.send === undefined || process.platform === "win32") {
    io.stderr.write(`${io.argv[0] ?? ""} is internal to dex-agents-${harness.name}; run the command without it.\n`);
    return 2;
  }
  const host = new WorkerHost(harness, io, createCommand);
  return host.run();
}

/** One Worker host process. */
class WorkerHost {
  private readonly launcherPid = process.ppid;
  private readonly watchdog: WorkerHostWatchdog;
  private readonly recordPath: string;
  private readonly command: AgentCliCommand;
  private toolGroups: RecordedToolProcessGroups | undefined;
  private opened: OpenedSessionWorker | undefined;
  private start: WorkerHostStartRequest | undefined;
  private shutdown: Promise<number> | undefined;
  private readonly stopRequested: Promise<void>;
  private requestStop: () => void = () => undefined;
  private readonly startReceived: Promise<WorkerHostStartRequest | undefined>;
  private resolveStart: (start: WorkerHostStartRequest | undefined) => void = () => undefined;

  public constructor(
    private readonly harness: AgentHarness,
    private readonly io: AgentCliIo,
    createCommand: WorkerHostCommandFactory,
  ) {
    const environment = readCliEnvironment(io.environment);
    this.recordPath = workerHostRunRecordPath(environment.agentsDirectory, harness.name, process.pid);
    this.command = createCommand((line) => this.log(line));
    this.watchdog = new WorkerHostWatchdog(this.launcherPid, () => void this.stop());
    this.startReceived = new Promise((resolve) => {
      this.resolveStart = resolve;
    });
    this.stopRequested = new Promise((resolve) => {
      this.requestStop = resolve;
    });
    process.on("message", (message: LauncherToWorkerHostMessage) => this.receive(message));
    process.on("disconnect", () => {
      this.resolveStart(undefined);
      void this.stop();
    });
    process.on("SIGTERM", () => {
      this.send({ type: "worker_state", state: "stopping" });
      void this.stop();
    });
  }

  public async run(): Promise<number> {
    // A launcher that died before this line leaves init as the parent, which the watchdog could not tell apart.
    const launcher = this.launcherPid === 1 ? undefined : readProcessIdentity(this.launcherPid);
    const self = readProcessIdentity(process.pid);
    if (launcher === undefined || self === undefined) {
      this.log("the Worker host could not read its own or its launcher's process identity");
      return 1;
    }
    this.toolGroups = new RecordedToolProcessGroups(this.recordPath, launcher, self);
    const start = await this.startReceived;
    if (start === undefined) {
      return this.stop();
    }
    this.start = start;
    try {
      this.opened = await this.command.openSessionWorker(
        start.effective,
        { effective: start.effective, configPath: start.configPath, isFromFile: start.isFromFile },
        start.server,
        start.connection,
        {
          resumeSessionId: start.resumeSessionId ?? undefined,
          isOpeningConnectorsPage: true,
          toolProcessGroups: this.toolGroups,
        },
      );
    } catch (error: unknown) {
      this.send({ type: "startup_failed", exitCode: this.command.exitCodeOf(error), message: describe(error) });
      return this.stop();
    }
    this.send({
      type: "worker_ready",
      sessionId: this.opened.sessionId,
      flowType: this.opened.flowType,
      isResumed: this.opened.isResumed,
      workspace: this.opened.workspace,
      capabilities: sessionFlowCapabilities(this.opened, this.harness),
    });
    await this.stopRequested;
    return this.stop();
  }

  private receive(message: LauncherToWorkerHostMessage): void {
    switch (message.type) {
      case "start":
        this.resolveStart(message);
        return;
      case "execute":
        void this.execute(message.method, message.params, message.idempotencyKey).then((outcome) =>
          this.send({ type: "executed", requestNumber: message.requestNumber, outcome }),
        );
        return;
      case "shutdown":
        void this.stop();
        return;
    }
  }

  private async execute(
    method: string,
    params: Readonly<Record<string, unknown>>,
    idempotencyKey: string,
  ): Promise<LocalRequestOutcome> {
    const opened = this.opened;
    const start = this.start;
    if (opened === undefined || start === undefined || this.shutdown !== undefined) {
      return refusal("host_stopped", "The Worker host stopped.");
    }
    try {
      return await executeWorkerHostMethod(method, params, idempotencyKey, {
        harnessName: this.harness.name,
        opened,
        connectorsEnabled: start.effective.connectors.enabled,
        log: (line) => this.log(line),
      });
    } catch (error: unknown) {
      this.log(`${method} failed: ${describe(error)}`);
      return refusal("failed", `${method} failed in the Worker host.`);
    }
  }

  /** Stops once: the watchdog starts its hard bound, the tool groups stop, then the Worker and Client close. */
  private stop(): Promise<number> {
    this.requestStop();
    this.shutdown ??= (async () => {
      this.watchdog.shutdownStarted();
      await this.toolGroups?.terminateAll();
      await this.command.close();
      removeWorkerHostRunRecord(this.recordPath);
      return 0;
    })();
    return this.shutdown;
  }

  private log(line: string): void {
    this.io.stderr.write(`${new Date().toISOString()} ${line}\n`);
    this.send({ type: "log", text: line });
  }

  private send(message: WorkerHostToLauncherMessage): void {
    if (process.connected && process.send !== undefined) {
      // The callback absorbs a write after the launcher disconnected; "disconnect" stops the host.
      process.send(message, undefined, undefined, (error) => error ?? undefined);
    }
  }
}

/** Returns what the session Flow the host built supports. */
function sessionFlowCapabilities(opened: OpenedSessionWorker, harness: AgentHarness): SessionFlowCapabilities {
  const runner = opened.flow.runner as { buildUserMessageWithImages?: unknown };
  const reasoningEfforts = harness.supportedReasoningEfforts ?? [];
  return {
    ...(typeof runner.buildUserMessageWithImages === "function"
      ? { images: { max_per_message: maxImagesPerMessage, max_bytes: maxImageBytes } }
      : {}),
    plan_mode: opened.flow.options.planModeEnabled === true,
    turn_control: true,
    ...(reasoningEfforts.length === 0 ? {} : { reasoning_effort: { values: [...reasoningEfforts] } }),
  };
}

function describe(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}
