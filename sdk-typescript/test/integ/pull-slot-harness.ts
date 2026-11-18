// Portions of this file are derived from indeedeng/iwf-java-sdk.
// Those portions are licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
//
// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications are licensed under the Sustainable Use License 1.0.
// Third-Party Materials remain under the Apache License, Version 2.0.
// See LICENSE and LEGACY_NOTICES.md.

import assert from "node:assert/strict";
import { spawn, type ChildProcess } from "node:child_process";
import { randomUUID } from "node:crypto";
import { closeSync, mkdtempSync, openSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { join } from "node:path";
import type { TestContext } from "node:test";

import {
  Client as GrpcClient,
  Metadata,
  Server,
  ServerCredentials,
  credentials,
  status,
  type ClientDuplexStream,
  type ServerDuplexStream,
  type ServerWritableStream,
  type ServiceDefinition,
  type ServiceError,
  type StatusObject,
  type UntypedServiceImplementation,
} from "@grpc/grpc-js";

import {
  Client,
  Registry,
  StepList,
  Timer,
  Wait,
  Worker,
  gracefulComplete,
  openBlobCache,
  rpc,
  stringCodec,
  type BlobCache,
  type Context,
  type Flow,
  type PullSlotStreamState,
  type PullWorkerOptions,
  type RPCResult,
  type Step,
  type StepDecision,
} from "../../src/index.js";
import {
  FlowServiceClient,
  FlowServiceService,
  PollWorkerSlotKind,
  PollWorkerTaskRequest,
  PollWorkerTaskResponse,
  WorkerTaskServiceService,
} from "../../src/gen/dex.js";
import type { PullSlotSettings, PullSlotStream, PullSlotStreams } from "../../src/pull-slot-streams.js";
import type { PullWorker } from "../../src/pull-worker.js";
import { sdkVersion } from "../../src/sdk-version.generated.js";

export const STEP_AND_RPC = PollWorkerSlotKind.POLL_WORKER_SLOT_KIND_STEP_AND_RPC;
export const RPC_ONLY = PollWorkerSlotKind.POLL_WORKER_SLOT_KIND_RPC_ONLY;
const POLL_WORKER_TASK_PATH = WorkerTaskServiceService.pollWorkerTask.path;
const PROXY_RESET_MESSAGE = "pull slot proxy reset the stream";

/** Collects cleanups of one test and runs them in reverse order after it. */
export class TestScope {
  private readonly cleanups: (() => Promise<void> | void)[] = [];

  public constructor(context: TestContext) {
    context.after(async () => {
      for (const cleanup of this.cleanups.reverse()) {
        try {
          await cleanup();
        } catch (failure) {
          console.error("test cleanup failed", failure);
        }
      }
    });
  }

  public defer(cleanup: () => Promise<void> | void): void {
    this.cleanups.push(cleanup);
  }

  public temporaryDirectory(prefix: string): string {
    const directory = mkdtempSync(join(tmpdir(), prefix));
    this.defer(() => rmSync(directory, { recursive: true, force: true }));
    return directory;
  }

  public blobCache(): BlobCache {
    const cache = openBlobCache({ directory: this.temporaryDirectory("dex-ts-slot-cache-"), maxBytes: 1 << 20 });
    this.defer(() => cache.close());
    return cache;
  }
}

/** Matching limits for a test server; zero keeps the server default. */
export interface ServerLimits {
  readonly maxConcurrentTasksCap?: number;
  readonly maxStreams?: number;
}

/** One dex-server-zd process per test, so a test can set caps and restart it. */
export class AdaptiveSlotTestServer {
  public address = "";
  private child: ChildProcess | undefined;
  private exited: Promise<void> = Promise.resolve();

  private constructor(
    private readonly scope: TestScope,
    private readonly binary: string,
    private readonly dataDirectory: string,
    private readonly logPath: string,
  ) {}

  public static async start(scope: TestScope, limits: ServerLimits = {}): Promise<AdaptiveSlotTestServer> {
    const binary = process.env.DEX_SERVER_ZD_BINARY;
    assert.ok(binary !== undefined && binary !== "", "set DEX_SERVER_ZD_BINARY to a dex-server-zd binary");
    const dataDirectory = scope.temporaryDirectory("dex-ts-slot-server-");
    const server = new AdaptiveSlotTestServer(scope, binary, dataDirectory, join(dataDirectory, "server.log"));
    scope.defer(() => server.stop());
    await server.launch(limits);
    return server;
  }

  /** Starts a process on the same data directory, on a new address. */
  public async launch(limits: ServerLimits): Promise<void> {
    this.address = `127.0.0.1:${await availablePort()}`;
    let configuration = "dev:\n  durabilityMode: processCrashSafe\nmatching:\n  pingInterval: 2s\n";
    if (limits.maxConcurrentTasksCap !== undefined) configuration += `  maxConcurrentTasksCap: ${limits.maxConcurrentTasksCap}\n`;
    if (limits.maxStreams !== undefined) configuration += `  maxStreams: ${limits.maxStreams}\n`;
    const configurationPath = join(this.dataDirectory, "adaptive-slot-config.yaml");
    writeFileSync(configurationPath, configuration);
    const logFile = openSync(this.logPath, "a");
    const child = spawn(this.binary, [
      "dev", "--data-dir", join(this.dataDirectory, "data"), "--listen", this.address, "--config", configurationPath,
    ], { detached: true, stdio: ["ignore", logFile, logFile] });
    closeSync(logFile);
    this.child = child;
    this.exited = new Promise((resolve) => child.once("exit", () => resolve()));
    await this.waitUntilServing();
  }

  /** Ends the server like a crash: every stream breaks and nothing drains. */
  public async kill(): Promise<void> {
    this.signalProcessGroup("SIGKILL");
    await this.exited;
  }

  public async restart(limits: ServerLimits): Promise<void> {
    await this.launch(limits);
  }

  public async stop(): Promise<void> {
    if (this.child === undefined || this.child.exitCode !== null || this.child.signalCode !== null) return;
    this.signalProcessGroup("SIGTERM");
    const hasExited = await Promise.race([this.exited.then(() => true), delay(10_000).then(() => false)]);
    if (!hasExited) await this.kill();
  }

  public logTail(): string {
    try {
      return readFileSync(this.logPath, "utf8").split("\n").slice(-200).join("\n");
    } catch (failure) {
      return `read dex-server-zd log: ${String(failure)}`;
    }
  }

  /** Returns a client of the current process; a restart needs a new client. */
  public newClient(registry: Registry): Client {
    const client = new Client(registry, this.scope.blobCache(), { serverAddress: this.address });
    this.scope.defer(() => client.close());
    return client;
  }

  private signalProcessGroup(signal: NodeJS.Signals): void {
    const pid = this.child?.pid;
    if (pid === undefined) return;
    try {
      process.kill(-pid, signal);
    } catch (failure) {
      if ((failure as NodeJS.ErrnoException).code !== "ESRCH") throw failure;
    }
  }

  private async waitUntilServing(): Promise<void> {
    const client = new FlowServiceClient(this.address, credentials.createInsecure());
    try {
      const deadline = Date.now() + 30_000;
      let hasExited = false;
      void this.exited.then(() => { hasExited = true; });
      for (;;) {
        const isServing = await new Promise<boolean>((resolve) => {
          client.getServerInfo({}, new Metadata(), { deadline: Date.now() + 1_000 }, (error) => resolve(error === null));
        });
        if (isServing) return;
        if (hasExited) throw new Error(`dex-server-zd exited before serving:\n${this.logTail()}`);
        if (Date.now() > deadline) throw new Error(`dex-server-zd did not serve:\n${this.logTail()}`);
        await delay(50);
      }
    } finally {
      client.close();
    }
  }
}

/** One Register or acknowledgement that the proxy saw. */
export interface ProxyRegister {
  readonly appWorkerID: string;
  readonly slotKind: PollWorkerSlotKind;
  readonly atMs: number;
}

interface ProxyStream {
  readonly appWorkerID: string;
  readonly slotKind: PollWorkerSlotKind;
  readonly downstream: ServerDuplexStream<Buffer, Buffer>;
  upstream: ClientDuplexStream<Buffer, Buffer> | undefined;
  isRegistered: boolean;
  hasEnded: boolean;
  isReset: boolean;
  isDownstreamClosed: boolean;
  stage: string;
  readonly upstreamAnswered: Deferred;
}

/** Holds STEP_AND_RPC Registers; isReleasedByRpcOnly releases it at the first RPC_ONLY acknowledgement. */
interface StepRegisterGate {
  readonly release: Deferred;
  readonly isReleasedByRpcOnly: boolean;
  readonly held: ProxyStream[];
  // Resolves when the first STEP_AND_RPC Register waits; an RPC_ONLY Register waits for it.
  readonly stepHeld: Deferred;
  // Holds at most this many Registers; later ones pass.
  remainingHolds: number;
}

/** Forwards a Worker's gRPC traffic to the current server and observes its slot streams. */
export class PullSlotProxy {
  public readonly registers: ProxyRegister[] = [];
  public readonly acknowledgements: ProxyRegister[] = [];
  public readonly rejections: ProxyRegister[] = [];
  public readonly workerResets: ProxyRegister[] = [];
  private readonly server = new Server();
  private readonly streams = new Set<ProxyStream>();
  private readonly upstreams: GrpcClient[] = [];
  private upstream!: GrpcClient;
  private port = 0;
  private stepRegisterGate: StepRegisterGate | undefined;
  private acknowledgementGate: Deferred | undefined;
  private shouldFailStepRegisters = false;
  private passStepRegisters = 0;
  private halfCloseGate: Deferred | undefined;
  private heldHalfCloses = 0;
  private firstAcknowledgementAtMs = 0;
  // Set at test cleanup; a Register released then must not open a call on a closed upstream.
  private isClosed = false;

  private constructor() {}

  public static async start(scope: TestScope, upstreamAddress: string): Promise<PullSlotProxy> {
    const proxy = new PullSlotProxy();
    proxy.setUpstream(upstreamAddress);
    proxy.server.addService(passthroughDefinition(FlowServiceService), proxy.passthroughImplementation(FlowServiceService));
    proxy.server.addService(passthroughDefinition(WorkerTaskServiceService), {
      pollWorkerTask: (call: ServerDuplexStream<Buffer, Buffer>) => proxy.forwardPollWorkerTask(call),
    });
    proxy.port = await new Promise<number>((resolve, reject) => {
      proxy.server.bindAsync("127.0.0.1:0", ServerCredentials.createInsecure(), (error, port) => {
        if (error !== null) reject(error); else resolve(port);
      });
    });
    scope.defer(() => proxy.close());
    return proxy;
  }

  public get address(): string {
    return `127.0.0.1:${this.port}`;
  }

  /** Points new calls at address; calls already open stay on the old server. */
  public setUpstream(address: string): void {
    this.upstream = new GrpcClient(address, credentials.createInsecure());
    this.upstreams.push(this.upstream);
  }

  /** Holds STEP_AND_RPC Registers until the RPC_ONLY stream is acknowledged (design Test 17). */
  public holdStepRegistersUntilRpcOnlyAcknowledged(): void {
    this.stepRegisterGate = {
      release: deferred(), isReleasedByRpcOnly: true, held: [], stepHeld: deferred(), remainingHolds: Number.POSITIVE_INFINITY,
    };
  }

  /**
   * Holds the next STEP_AND_RPC Registers, at most holdCount of them, until release is called.
   * @returns The release function.
   */
  public holdStepRegisters(holdCount = Number.POSITIVE_INFINITY): () => void {
    const gate: StepRegisterGate = { release: deferred(), isReleasedByRpcOnly: false, held: [], stepHeld: deferred(), remainingHolds: holdCount };
    this.stepRegisterGate = gate;
    return () => {
      if (this.stepRegisterGate === gate) this.stepRegisterGate = undefined;
      gate.release.resolve();
    };
  }

  /** Counts the STEP_AND_RPC Registers that the manual gate holds now. */
  public heldStepRegisters(): number {
    return this.stepRegisterGate?.held.filter((stream) => !stream.hasEnded).length ?? 0;
  }

  /** Answers every later STEP_AND_RPC Register with UNAVAILABLE without reaching the server. */
  public failStepRegistersFromNow(): void {
    this.shouldFailStepRegisters = true;
    this.passStepRegisters = 0;
  }

  public passNextStepRegister(): void {
    this.passStepRegisters++;
  }

  public stopFailingStepRegisters(): void {
    this.shouldFailStepRegisters = false;
  }

  /**
   * Keeps every Worker half-close from reaching the server until release is called.
   * @returns A function reporting how many half-closes the gate holds, and the release function.
   */
  public holdHalfCloses(): { heldCount: () => number; release: () => void } {
    const gate = deferred();
    this.halfCloseGate = gate;
    this.heldHalfCloses = 0;
    return {
      heldCount: () => this.heldHalfCloses,
      release: () => {
        this.halfCloseGate = undefined;
        gate.resolve();
      },
    };
  }

  /**
   * Delays every acknowledgement until release is called.
   * @returns The release function.
   */
  public holdAcknowledgements(): () => void {
    const gate = deferred();
    this.acknowledgementGate = gate;
    return () => {
      this.acknowledgementGate = undefined;
      gate.resolve();
    };
  }

  /**
   * Resets one acknowledged stream of the kind, like a network failure.
   * @returns Whether a stream was reset.
   */
  public resetRegisteredStream(appWorkerID: string, slotKind: PollWorkerSlotKind): boolean {
    for (const stream of this.streams) {
      if (stream.appWorkerID === appWorkerID && stream.slotKind === slotKind && stream.isRegistered && !stream.hasEnded) {
        stream.isReset = true;
        stream.upstream?.cancel();
        this.endDownstream(stream, { code: status.UNAVAILABLE, details: PROXY_RESET_MESSAGE, metadata: new Metadata() });
        return true;
      }
    }
    return false;
  }

  /**
   * Counts the acknowledged, live streams of a kind, as the server holds them.
   * @returns The stream count.
   */
  public registeredStreams(appWorkerID: string, slotKind: PollWorkerSlotKind): number {
    let count = 0;
    for (const stream of this.streams) {
      if (stream.appWorkerID === appWorkerID && stream.slotKind === slotKind && stream.isRegistered && !stream.hasEnded) count++;
    }
    return count;
  }

  public registersSince(appWorkerID: string, sinceMs: number): ProxyRegister[] {
    return filterRegisters(this.registers, appWorkerID, sinceMs);
  }

  public acknowledgementsSince(appWorkerID: string, sinceMs: number): ProxyRegister[] {
    return filterRegisters(this.acknowledgements, appWorkerID, sinceMs);
  }

  public rejectionsSince(appWorkerID: string, sinceMs: number): ProxyRegister[] {
    return filterRegisters(this.rejections, appWorkerID, sinceMs);
  }

  public workerResetsSince(appWorkerID: string, sinceMs: number): ProxyRegister[] {
    return filterRegisters(this.workerResets, appWorkerID, sinceMs);
  }

  public firstAcknowledgementAt(): number {
    return this.firstAcknowledgementAtMs;
  }

  /**
   * Summarizes what the proxy saw of appWorkerID, for failure messages.
   * @returns A multi-line description.
   */
  public describe(appWorkerID: string): string {
    const lines = [["registers", this.registers], ["acknowledgements", this.acknowledgements],
      ["rejections", this.rejections], ["workerResets", this.workerResets]].map(([name, events]) =>
      `${String(name)}: ${(events as ProxyRegister[]).filter((event) => event.appWorkerID === appWorkerID)
        .map((event) => `${PollWorkerSlotKind[event.slotKind]}@${event.atMs}`).join(" ")}`);
    for (const stream of this.streams) {
      if (stream.appWorkerID === appWorkerID) lines.push(`live ${PollWorkerSlotKind[stream.slotKind]} registered=${stream.isRegistered} stage=${stream.stage}`);
    }
    return lines.join("\n");
  }

  private async close(): Promise<void> {
    this.isClosed = true;
    this.stepRegisterGate?.release.resolve();
    this.acknowledgementGate?.resolve();
    this.halfCloseGate?.resolve();
    for (const stream of this.streams) stream.upstream?.cancel();
    this.server.forceShutdown();
    for (const upstream of this.upstreams) upstream.close();
  }

  private passthroughImplementation(service: Record<string, { path: string; requestStream: boolean; responseStream: boolean }>): UntypedServiceImplementation {
    const implementation: UntypedServiceImplementation = {};
    for (const [name, method] of Object.entries(service)) {
      if (!method.requestStream && !method.responseStream) {
        implementation[name] = (call: { request: Buffer; metadata: Metadata }, callback: (error: ServiceError | null, value?: Buffer) => void) => {
          this.upstream.makeUnaryRequest(method.path, identity, identity, call.request, call.metadata, {},
            (error, response) => callback(error, response));
        };
      } else if (!method.requestStream) {
        implementation[name] = (call: ServerWritableStream<Buffer, Buffer>) => {
          const upstream = this.upstream.makeServerStreamRequest(method.path, identity, identity, call.request, call.metadata, {});
          upstream.on("data", (frame: Buffer) => call.write(frame));
          upstream.on("error", () => {});
          upstream.on("status", (result: StatusObject) => {
            if (result.code === status.OK) call.end(); else call.emit("error", result);
          });
          call.on("cancelled", () => upstream.cancel());
        };
      } else {
        implementation[name] = (call: ServerDuplexStream<Buffer, Buffer>) => {
          const upstream = this.upstream.makeBidiStreamRequest(method.path, identity, identity, call.metadata, {});
          call.on("data", (frame: Buffer) => upstream.write(frame));
          call.on("end", () => upstream.end());
          call.on("error", () => {});
          call.on("cancelled", () => upstream.cancel());
          upstream.on("data", (frame: Buffer) => call.write(frame));
          upstream.on("error", () => {});
          upstream.on("status", (result: StatusObject) => {
            if (result.code === status.OK) call.end(); else call.emit("error", result);
          });
        };
      }
    }
    return implementation;
  }

  // Opens the upstream stream only after the Register passes its gate.
  private forwardPollWorkerTask(downstream: ServerDuplexStream<Buffer, Buffer>): void {
    const pending: Buffer[] = [];
    let stream: ProxyStream | undefined;
    let hasPendingEnd = false;
    downstream.on("error", () => {});
    downstream.on("data", (frame: Buffer) => {
      if (stream === undefined) {
        stream = this.acceptRegister(downstream, frame, pending, () => hasPendingEnd);
      } else if (stream.upstream !== undefined) {
        stream.upstream.write(frame);
      } else {
        pending.push(frame);
      }
    });
    downstream.on("end", () => {
      if (stream?.upstream === undefined) {
        hasPendingEnd = true;
        return;
      }
      void this.forwardHalfClose(stream);
    });
    downstream.on("cancelled", () => {
      if (stream === undefined || stream.isDownstreamClosed) return;
      stream.isDownstreamClosed = true;
      if (!stream.isReset) this.workerResets.push({ appWorkerID: stream.appWorkerID, slotKind: stream.slotKind, atMs: Date.now() });
      stream.upstream?.cancel();
      this.markEnded(stream);
    });
  }

  private acceptRegister(downstream: ServerDuplexStream<Buffer, Buffer>, first: Buffer, pending: Buffer[], hasPendingEnd: () => boolean): ProxyStream {
    const request = PollWorkerTaskRequest.decode(first);
    const register = request.frame?.$case === "register" ? request.frame.value : undefined;
    const stream: ProxyStream = {
      appWorkerID: register?.appWorkerId ?? "", slotKind: register?.slotKind ?? STEP_AND_RPC, downstream, upstream: undefined,
      isRegistered: false, hasEnded: false, isReset: false, isDownstreamClosed: false, stage: "arrived", upstreamAnswered: deferred(),
    };
    this.registers.push({ appWorkerID: stream.appWorkerID, slotKind: stream.slotKind, atMs: Date.now() });
    this.streams.add(stream);
    const isStep = stream.slotKind === STEP_AND_RPC;
    const gate = this.stepRegisterGate;
    const isHeld = isStep && gate !== undefined && gate.remainingHolds > 0;
    if (isHeld) {
      gate.remainingHolds--;
      gate.held.push(stream);
      gate.stepHeld.resolve();
    }
    const shouldFail = isStep && this.shouldFailStepRegisters && this.passStepRegisters === 0;
    if (isStep && this.shouldFailStepRegisters && this.passStepRegisters > 0) this.passStepRegisters--;
    if (shouldFail) {
      this.endDownstream(stream, { code: status.UNAVAILABLE, details: "pull slot proxy failed the Register", metadata: new Metadata() });
      return stream;
    }
    void this.openUpstream(stream, first, pending, hasPendingEnd, isHeld ? gate : undefined, !isStep ? gate : undefined);
    return stream;
  }

  private async openUpstream(
    stream: ProxyStream,
    first: Buffer,
    pending: Buffer[],
    hasPendingEnd: () => boolean,
    holdingGate: StepRegisterGate | undefined,
    rpcOnlyGate: StepRegisterGate | undefined,
  ): Promise<void> {
    const ended = new Promise<void>((resolve) => stream.downstream.once("cancelled", () => resolve()));
    if (holdingGate !== undefined) {
      stream.stage = "gated";
      await Promise.race([holdingGate.release.promise, ended]);
    }
    // The RPC_ONLY stream registers only once a STEP_AND_RPC Register waits, so that Register meets a full cap.
    if (rpcOnlyGate?.isReleasedByRpcOnly === true) {
      stream.stage = "waiting for a held Step-and-RPC Register";
      await Promise.race([rpcOnlyGate.stepHeld.promise, ended]);
    }
    if (stream.isDownstreamClosed || this.isClosed) return;
    stream.stage = "opening upstream";
    const upstream = this.upstream.makeBidiStreamRequest(POLL_WORKER_TASK_PATH, identity, identity, stream.downstream.metadata, {});
    stream.upstream = upstream;
    upstream.write(first);
    for (const frame of pending.splice(0)) upstream.write(frame);
    stream.stage = "register sent upstream";
    if (hasPendingEnd()) void this.forwardHalfClose(stream);
    let chain = Promise.resolve();
    upstream.on("error", () => {});
    upstream.on("data", (frame: Buffer) => {
      chain = chain.then(async () => {
        stream.upstreamAnswered.resolve();
        const response = PollWorkerTaskResponse.decode(frame);
        if (response.frame?.$case === "registered") await this.recordAcknowledgement(stream);
        if (!stream.isDownstreamClosed) stream.downstream.write(frame);
      });
    });
    upstream.on("status", (result: StatusObject) => {
      chain = chain.then(() => {
        stream.upstreamAnswered.resolve();
        if (result.code === status.RESOURCE_EXHAUSTED) {
          this.rejections.push({ appWorkerID: stream.appWorkerID, slotKind: stream.slotKind, atMs: Date.now() });
        }
        if (stream.isReset) return;
        this.endDownstream(stream, result);
      });
    });
  }

  private async forwardHalfClose(stream: ProxyStream): Promise<void> {
    const gate = this.halfCloseGate;
    if (gate !== undefined) {
      this.heldHalfCloses++;
      await gate.promise;
    }
    stream.upstream?.end();
  }

  private async recordAcknowledgement(stream: ProxyStream): Promise<void> {
    if (this.acknowledgementGate !== undefined) await this.acknowledgementGate.promise;
    stream.isRegistered = true;
    this.acknowledgements.push({ appWorkerID: stream.appWorkerID, slotKind: stream.slotKind, atMs: Date.now() });
    if (this.firstAcknowledgementAtMs === 0) this.firstAcknowledgementAtMs = Date.now();
    const gate = this.stepRegisterGate;
    if (gate === undefined || !gate.isReleasedByRpcOnly || stream.slotKind !== RPC_ONLY) return;
    this.stepRegisterGate = undefined;
    gate.release.resolve();
    // The Worker sees this acknowledgement only after the server answered every held Register.
    for (const held of gate.held) {
      const isAnswered = await Promise.race([held.upstreamAnswered.promise.then(() => true), delay(10_000).then(() => false)]);
      assert.ok(isAnswered, "a held Register got no answer from the server");
    }
  }

  private endDownstream(stream: ProxyStream, result: Partial<StatusObject>): void {
    if (stream.isDownstreamClosed) return;
    stream.isDownstreamClosed = true;
    this.markEnded(stream);
    stream.upstreamAnswered.resolve();
    if (result.code === status.OK) stream.downstream.end();
    else stream.downstream.emit("error", { code: result.code, details: result.details, metadata: result.metadata ?? new Metadata() });
  }

  private markEnded(stream: ProxyStream): void {
    stream.hasEnded = true;
    this.streams.delete(stream);
  }
}

/** Holds node slots under another app Worker ID and retakes any freed slot at once. */
export class PullSlotCompetitor {
  private readonly client: GrpcClient;
  private readonly held = new Set<ClientDuplexStream<Buffer, Buffer>>();
  private readonly loops: Promise<void>[] = [];
  private isStopped = false;

  private constructor(address: string, private readonly appWorkerID: string) {
    this.client = new GrpcClient(address, credentials.createInsecure());
  }

  public static start(scope: TestScope, address: string, appWorkerID: string, loopCount: number): PullSlotCompetitor {
    const competitor = new PullSlotCompetitor(address, appWorkerID);
    for (let index = 0; index < loopCount; index++) competitor.loops.push(competitor.holdSlot());
    scope.defer(() => competitor.stop());
    return competitor;
  }

  public heldStreams(): number {
    return this.held.size;
  }

  /** Ends one held stream; its loop re-registers into the freed slot at once. */
  public closeOneStream(): void {
    for (const call of this.held) {
      call.cancel();
      return;
    }
  }

  public async stop(): Promise<void> {
    if (this.isStopped) return;
    this.isStopped = true;
    for (const call of this.held) call.cancel();
    await Promise.all(this.loops);
    this.client.close();
  }

  private async holdSlot(): Promise<void> {
    while (!this.isStopped) {
      await this.registerAndHold();
      if (!this.isStopped) await delay(1);
    }
  }

  private registerAndHold(): Promise<void> {
    return new Promise((resolve) => {
      const call = this.client.makeBidiStreamRequest(POLL_WORKER_TASK_PATH, identity, identity, new Metadata(), {});
      let isRegistered = false;
      const finish = () => {
        this.held.delete(call);
        resolve();
      };
      call.on("error", () => {});
      call.on("status", finish);
      call.on("data", (frame: Buffer) => {
        const response = PollWorkerTaskResponse.decode(frame);
        if (!isRegistered) {
          if (response.frame?.$case !== "registered") {
            call.cancel();
            return;
          }
          isRegistered = true;
          if (this.isStopped) call.cancel(); else this.held.add(call);
          return;
        }
        if (response.frame?.$case === "ping") call.write(encodeRequest({ frame: { $case: "pong", value: {} } }));
      });
      call.write(encodeRequest({ frame: { $case: "register", value: {
        queue: "", appWorkerId: this.appWorkerID, flowTypes: ["pullSlotCompetitorFlow"], sdkName: "typescript",
        sdkVersion, supportsReservedSlotDispatch: true, slotKind: STEP_AND_RPC, taskQueue: "",
      } } }));
    });
  }
}

export const ADAPTIVE_SLOT_MODE_BLOCK = "block";
export const ADAPTIVE_SLOT_MODE_STUBBORN = "stubborn";
export const ADAPTIVE_SLOT_MODE_QUICK = "quick";
export const STUBBORN_RPC_KEY = "rpc";

export interface AdaptiveSlotEntry {
  readonly flowId: string;
  readonly kind: "step" | "rpc";
  readonly attempt: number;
}

/** Records handler entries and releases blocked handlers. */
export class AdaptiveSlotHandlers {
  public isRpcStubborn = false;
  private readonly entries: AdaptiveSlotEntry[] = [];
  private readonly releaseAll = deferred();
  // Releases handlers that ignore cancellation, keyed by Flow ID or "rpc".
  private readonly stubbornReleases = new Map<string, Deferred>();

  public get released(): Promise<void> {
    return this.releaseAll.promise;
  }

  public enter(entry: AdaptiveSlotEntry): void {
    this.entries.push(entry);
  }

  public release(): void {
    this.releaseAll.resolve();
  }

  public stubbornRelease(key: string): Promise<void> {
    let release = this.stubbornReleases.get(key);
    if (release === undefined) {
      release = deferred();
      this.stubbornReleases.set(key, release);
    }
    return release.promise;
  }

  public releaseStubborn(...keys: string[]): void {
    for (const key of keys) {
      void this.stubbornRelease(key);
      this.stubbornReleases.get(key)!.resolve();
    }
  }

  /** Lets every handler that ignores cancellation return, so a failed test can stop its Worker. */
  public releaseAllStubborn(): void {
    this.releaseStubborn(...this.stubbornReleases.keys(), STUBBORN_RPC_KEY);
  }

  /**
   * Waits for count more handler entries.
   * @returns The entries, oldest first.
   */
  public async awaitEntries(count: number, timeoutMs = 60_000): Promise<AdaptiveSlotEntry[]> {
    await eventually(() => this.entries.length >= count, timeoutMs, () => `only ${this.entries.length} of ${count} handlers started`);
    return this.entries.splice(0, count);
  }

  public drainEntries(): AdaptiveSlotEntry[] {
    return this.entries.splice(0);
  }
}

class AdaptiveSlotStep implements Step<string> {
  public readonly inputCodec = stringCodec;
  public constructor(private readonly handlers: AdaptiveSlotHandlers) {}
  public getStepType(): string { return "AdaptiveSlotStep"; }
  public async execute(context: Context, mode: string): Promise<StepDecision> {
    this.handlers.enter({ flowId: context.flowId, kind: "step", attempt: context.attempt });
    if (mode === ADAPTIVE_SLOT_MODE_BLOCK) {
      await Promise.race([this.handlers.released, aborted(context.cancellationSignal)]);
      context.cancellationSignal.throwIfAborted();
    } else if (mode === ADAPTIVE_SLOT_MODE_STUBBORN && context.attempt === 1) {
      // Only the first attempt ignores cancellation, so the retry after a restart completes.
      await this.handlers.stubbornRelease(context.flowId);
    }
    return gracefulComplete(mode);
  }
}

/** Runs one Step whose input chooses how long it holds its slot. */
export class AdaptiveSlotFlow implements Flow<string> {
  private readonly start: AdaptiveSlotStep;
  public constructor(public readonly handlers: AdaptiveSlotHandlers) { this.start = new AdaptiveSlotStep(handlers); }
  public getFlowType(): string { return "AdaptiveSlotFlow"; }
  public getSteps() { return StepList.startStep(this.start); }

  @rpc({ outputCodec: stringCodec })
  public getSlotStatus(_context: Context): RPCResult<string> {
    return { output: "ok" };
  }
}

class AdaptiveSlotParkedStep implements Step<string> {
  public readonly inputCodec = stringCodec;
  public getStepType(): string { return "AdaptiveSlotParkedStep"; }
  public waitFor(): Wait { return Wait.until(Timer.byDuration(3_600_000)); }
  public execute(): StepDecision { return gracefulComplete("parked"); }
}

/** Stays running without a slot, so RPCs can target it. */
export class AdaptiveSlotParkedFlow implements Flow<string> {
  private readonly start = new AdaptiveSlotParkedStep();
  public constructor(private readonly handlers: AdaptiveSlotHandlers) {}
  public getFlowType(): string { return "AdaptiveSlotParkedFlow"; }
  public getSteps() { return StepList.startStep(this.start); }

  @rpc({ outputCodec: stringCodec })
  public async blockUntilReleased(context: Context): Promise<RPCResult<string>> {
    this.handlers.enter({ flowId: context.flowId, kind: "rpc", attempt: context.attempt });
    if (this.handlers.isRpcStubborn) {
      await this.handlers.stubbornRelease(STUBBORN_RPC_KEY);
      return { output: "released" };
    }
    await Promise.race([this.handlers.released, aborted(context.cancellationSignal)]);
    context.cancellationSignal.throwIfAborted();
    return { output: "released" };
  }

  @rpc({ outputCodec: stringCodec })
  public getSlotStatus(_context: Context): RPCResult<string> {
    return { output: "ok" };
  }
}

/** A started Worker and the outcome of its run, which ends at close or at a start failure. */
export class AdaptiveSlotWorkerRun {
  public hasSettled = false;
  public failure: unknown;
  private readonly settled: Promise<void>;

  public constructor(
    public readonly worker: Worker,
    public readonly registry: Registry,
    public readonly appWorkerID: string,
    public readonly flow: AdaptiveSlotFlow,
    public readonly parked: AdaptiveSlotParkedFlow,
  ) {
    this.settled = worker.run().then(
      () => { this.hasSettled = true; },
      (failure: unknown) => { this.hasSettled = true; this.failure = failure; },
    );
  }

  public state(): PullSlotStreamState {
    return this.worker.pullSlotStreamState();
  }

  /**
   * Waits for the run to end.
   * @returns Its failure, or undefined after a clean close.
   */
  public async awaitRunResult(timeoutMs: number): Promise<unknown> {
    const hasSettled = await Promise.race([this.settled.then(() => true), delay(timeoutMs).then(() => false)]);
    assert.ok(hasSettled, "the Worker run did not end");
    return this.failure;
  }
}

export interface AdaptiveSlotWorkerSettings {
  readonly shrinkWindowMs: number;
  readonly beforeRegisterSendForTest?: (stream: PullSlotStream) => Promise<void>;
  /** Registers only the parked Flow, so the Worker never takes the other Flow's Steps. */
  readonly isParkedOnly?: boolean;
}

/**
 * Starts a Worker with both test Flows and a test shrink window.
 * @returns The running Worker.
 */
export function startAdaptiveSlotWorker(
  scope: TestScope,
  address: string,
  handlers: AdaptiveSlotHandlers,
  pull: PullWorkerOptions,
  settings: AdaptiveSlotWorkerSettings,
): AdaptiveSlotWorkerRun {
  const flow = new AdaptiveSlotFlow(handlers);
  const parked = new AdaptiveSlotParkedFlow(handlers);
  const flows: Flow<string>[] = settings.isParkedOnly === true ? [parked] : [flow, parked];
  const registry = new Registry(flows);
  const appWorkerID = pull.appWorkerID ?? `adaptive-${randomUUID().slice(0, 8)}`;
  const worker = new Worker(registry, scope.blobCache(), { serverAddress: address, pull: { ...pull, appWorkerID } });
  setPullSlotSettingsForTest(worker, {
    shrinkWindowMs: settings.shrinkWindowMs,
    ...(settings.beforeRegisterSendForTest === undefined ? {} : { beforeRegisterSendForTest: settings.beforeRegisterSendForTest }),
  });
  const run = new AdaptiveSlotWorkerRun(worker, registry, appWorkerID, flow, parked);
  scope.defer(async () => {
    // A failed test may leave handlers that ignore cancellation; close waits for every handler.
    handlers.releaseAllStubborn();
    await worker.close();
  });
  return run;
}

// Tests replace the slot timings through the private state machine, as other tests do with Reflect.
export function setPullSlotSettingsForTest(worker: Worker, settings: PullSlotSettings): void {
  const pullWorker = Reflect.get(worker, "pullWorker") as PullWorker;
  (Reflect.get(pullWorker, "slots") as PullSlotStreams).settings = settings;
}

/** A barrier that holds armed slot streams after their admission and before their Register is written. */
export class RegisterSendBarrier {
  public isArmed = false;
  public held = 0;
  private passes = 0;
  private readonly waiters: (() => void)[] = [];
  private readonly released = deferred();

  public readonly hold = async (_stream: PullSlotStream): Promise<void> => {
    if (!this.isArmed) return;
    this.held++;
    try {
      if (this.passes > 0) {
        this.passes--;
        return;
      }
      await Promise.race([this.released.promise, new Promise<void>((resolve) => this.waiters.push(resolve))]);
    } finally {
      this.held--;
    }
  };

  public pass(): void {
    const waiter = this.waiters.shift();
    if (waiter === undefined) this.passes++; else waiter();
  }

  public release(): void {
    this.released.resolve();
  }
}

/**
 * Starts count Flows of the mode one after another.
 * @returns Their Flow IDs.
 */
export async function startAdaptiveSlotFlows(client: Client, flow: AdaptiveSlotFlow, mode: string, count: number): Promise<string[]> {
  const flowIds: string[] = [];
  for (let index = 0; index < count; index++) {
    const id = `adaptive-slot-${randomUUID()}`;
    await client.startFlow(flow, id, mode);
    flowIds.push(id);
  }
  return flowIds;
}

/**
 * Starts count Flows of the mode concurrently.
 * @returns Their Flow IDs.
 */
export async function startAdaptiveSlotFlowsConcurrently(client: Client, flow: AdaptiveSlotFlow, mode: string, count: number): Promise<string[]> {
  const flowIds = Array.from({ length: count }, () => `adaptive-slot-${randomUUID()}`);
  await Promise.all(flowIds.map((id) => client.startFlow(flow, id, mode)));
  return flowIds;
}

export async function startParkedFlow(client: Client, parked: AdaptiveSlotParkedFlow): Promise<string> {
  const id = `adaptive-parked-${randomUUID()}`;
  await client.startFlow(parked, id, "parked");
  return id;
}

export async function requireFlowsCompleted(client: Client, flowIds: readonly string[]): Promise<void> {
  for (const id of flowIds) {
    const result = await client.waitForFlow(id, 120_000);
    assert.equal(result.status, "completed", `Flow ${id}`);
  }
}

/**
 * Starts a quick Flow every 100 ms until stop resolves.
 * @returns Their Flow IDs.
 */
export async function startAdaptiveSlotTrickle(client: Client, flow: AdaptiveSlotFlow, stop: Promise<void>): Promise<string[]> {
  const flowIds: string[] = [];
  let isStopped = false;
  void stop.then(() => { isStopped = true; });
  while (!isStopped) {
    const id = `adaptive-trickle-${randomUUID()}`;
    await client.startFlow(flow, id, ADAPTIVE_SLOT_MODE_QUICK);
    flowIds.push(id);
    await Promise.race([delay(100), stop]);
  }
  return flowIds;
}

export async function requireSlotState(
  run: AdaptiveSlotWorkerRun,
  description: string,
  condition: (state: PullSlotStreamState) => boolean,
  timeoutMs = 30_000,
): Promise<void> {
  let last = run.state();
  await eventually(() => {
    last = run.state();
    return condition(last);
  }, timeoutMs, () => `slot streams never reached ${description}: ${JSON.stringify(last)}`);
}

export async function requireRegisteredStreams(proxy: PullSlotProxy, appWorkerID: string, stepStreams: number, rpcOnlyStreams: number): Promise<void> {
  await eventually(
    () => proxy.registeredStreams(appWorkerID, STEP_AND_RPC) === stepStreams && proxy.registeredStreams(appWorkerID, RPC_ONLY) === rpcOnlyStreams,
    20_000,
    () => `server streams of ${appWorkerID}: want ${stepStreams} Step-and-RPC and ${rpcOnlyStreams} RPC-only, have `
      + `${proxy.registeredStreams(appWorkerID, STEP_AND_RPC)} and ${proxy.registeredStreams(appWorkerID, RPC_ONLY)}\n${proxy.describe(appWorkerID)}`,
  );
}

export function requireNoPermanentRegistrationError(run: AdaptiveSlotWorkerRun): void {
  const registration = run.worker.pullRegistrationState();
  assert.equal(registration.isReady, true);
  for (const queue of registration.queues) {
    assert.equal(queue.lastPermanentError, undefined, `slot kind ${queue.queue} recorded a permanent error`);
  }
  assert.equal(run.hasSettled, false, `the Worker run ended during a soft rejection: ${String(run.failure)}`);
}

/** Checks that Registers come in at most one batch per window. */
export function requireRegisterBatchesPerWindow(registers: readonly ProxyRegister[], windowMs: number, batchSize: number): void {
  let batchStart = registers[0]!.atMs;
  let batchCount = 0;
  for (const register of registers) {
    if (register.atMs - batchStart > windowMs / 2) {
      assert.ok(register.atMs - batchStart >= windowMs * 0.8, "two growth batches in one window");
      batchStart = register.atMs;
      batchCount = 0;
    }
    batchCount++;
    assert.ok(batchCount <= batchSize, `a batch of ${batchCount} exceeds ${batchSize}`);
  }
}

/**
 * Groups Registers that arrive within half a window of a batch's first one.
 * @returns The batch count.
 */
export function countRegisterBatches(registers: readonly ProxyRegister[], windowMs: number): number {
  let batches = 0;
  let batchStart = 0;
  for (const register of registers) {
    if (batches === 0 || register.atMs - batchStart > windowMs / 2) {
      batches++;
      batchStart = register.atMs;
    }
  }
  return batches;
}

export function stepRegisters(registers: readonly ProxyRegister[]): ProxyRegister[] {
  return registers.filter((register) => register.slotKind === STEP_AND_RPC);
}

/**
 * Invariant 5: ceil(log2(E - L + 1)) + 1 windows.
 * @returns The window count.
 */
export function adaptiveSlotWindowsToFloor(effectiveCeiling: number, floor: number): number {
  let windows = 1;
  for (let span = 1; span < effectiveCeiling - floor + 1; span *= 2) windows++;
  return windows;
}

/** Records every sample where S + H + Sr + Hr exceeds C. */
export class SlotUsageSampler {
  public readonly violations: PullSlotStreamState[] = [];
  private readonly timer: ReturnType<typeof setInterval>;

  public constructor(scope: TestScope, run: AdaptiveSlotWorkerRun) {
    this.timer = setInterval(() => {
      const state = run.state();
      if (state.open + state.orphanedHandlers + state.rpcOnlyOpen + state.orphanedRpcOnlyHandlers > state.totalCapacity
        && this.violations.length < 8) {
        this.violations.push(state);
      }
    }, 5);
    scope.defer(() => { this.stopSampling(); });
  }

  public requireWithinCapacity(): void {
    assert.deepEqual(this.stopSampling(), [], "slot usage exceeded the capacity");
  }

  /**
   * Stops the sampler.
   * @returns The samples above the capacity.
   */
  public stopSampling(): PullSlotStreamState[] {
    clearInterval(this.timer);
    return this.violations;
  }
}

/** Captures console messages of one level for one test. */
export function captureConsole(context: TestContext, level: "warn" | "info"): string[] {
  const messages: string[] = [];
  context.mock.method(console, level, (...values: unknown[]) => { messages.push(values.map(String).join(" ")); });
  return messages;
}

export function hasMessage(messages: readonly string[], ...fragments: string[]): boolean {
  return messages.some((message) => fragments.every((fragment) => message.includes(fragment)));
}

export async function eventually(condition: () => boolean, timeoutMs: number, describe: () => string): Promise<void> {
  const deadline = Date.now() + timeoutMs;
  while (!condition()) {
    if (Date.now() >= deadline) assert.fail(describe());
    await delay(10);
  }
}

/** Fails if condition becomes true at any sample during durationMs. */
export async function never(condition: () => boolean, durationMs: number, message: string): Promise<void> {
  const deadline = Date.now() + durationMs;
  while (Date.now() < deadline) {
    assert.ok(!condition(), message);
    await delay(10);
  }
  assert.ok(!condition(), message);
}

/** Waits for a promise whose result depends on client retries, and logs it. */
export async function awaitSettledWithin(promise: Promise<unknown>, timeoutMs: number, description: string): Promise<void> {
  const outcome = await Promise.race([
    promise.then((value) => `returned ${String(value)}`, (failure: unknown) => `failed ${String(failure)}`),
    delay(timeoutMs).then(() => undefined),
  ]);
  assert.ok(outcome !== undefined, `${description} did not return`);
  console.log(`${description} ${outcome}`);
}

export function delay(milliseconds: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, milliseconds));
}

export interface Deferred {
  readonly promise: Promise<void>;
  resolve(): void;
}

export function deferred(): Deferred {
  let resolve!: () => void;
  const promise = new Promise<void>((resolvePromise) => { resolve = resolvePromise; });
  return { promise, resolve };
}

function aborted(signal: AbortSignal): Promise<void> {
  if (signal.aborted) return Promise.resolve();
  return new Promise((resolve) => signal.addEventListener("abort", () => resolve(), { once: true }));
}

function identity(value: Buffer): Buffer {
  return value;
}

function encodeRequest(request: PollWorkerTaskRequest): Buffer {
  return Buffer.from(PollWorkerTaskRequest.encode(request).finish());
}

function filterRegisters(registers: readonly ProxyRegister[], appWorkerID: string, sinceMs: number): ProxyRegister[] {
  return registers.filter((register) => register.appWorkerID === appWorkerID && register.atMs >= sinceMs);
}

function passthroughDefinition(service: Record<string, { path: string; requestStream: boolean; responseStream: boolean }>): ServiceDefinition {
  return Object.fromEntries(Object.entries(service).map(([name, method]) => [name, {
    path: method.path, requestStream: method.requestStream, responseStream: method.responseStream,
    requestSerialize: identity, requestDeserialize: identity, responseSerialize: identity, responseDeserialize: identity,
  }])) as ServiceDefinition;
}

function availablePort(): Promise<number> {
  return new Promise((resolve, reject) => {
    const server = createServer();
    server.once("error", reject);
    server.listen(0, "127.0.0.1", () => {
      const address = server.address();
      server.close(() => {
        if (address === null || typeof address === "string") reject(new Error("no TCP port"));
        else resolve(address.port);
      });
    });
  });
}
