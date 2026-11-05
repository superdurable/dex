// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0


import assert from "node:assert/strict";
import { once } from "node:events";
import http2 from "node:http2";
import net from "node:net";
import test from "node:test";

import { InvokeWorkerRPCRequest, InvokeWorkerRPCResponse, PollWorkerTaskResponse } from "../src/gen/dex.js";
import { Registry } from "../src/index.js";
import { PullWorker } from "../src/pull-worker.js";
import type { WorkerDispatcher } from "../src/worker-dispatcher.js";

const DATA_FRAME = 0;
const RST_STREAM_FRAME = 3;

interface ClientFrame {
  readonly type: number;
  readonly streamId: number;
  readonly isEndStream: boolean;
  readonly errorCode?: number;
}

test("a stop-timeout reset of a busy slot sends RST_STREAM without END_STREAM", async () => {
  const wire = await startWireHarness();
  const handler = new GatedHandler();
  const worker = wire.worker(handler, 50);
  try {
    worker.start();
    const stream = await wire.nextStream();
    wire.write(stream, registeredFrame());
    await worker.waitStarted();
    // Fails when the grpc-js call-chain walk stops finding the HTTP/2 stream.
    const poll = [...(Reflect.get(worker, "streams") as Set<{ http2Streams: readonly unknown[] }>)][0]!;
    assert.equal(poll.http2Streams.length, 1);
    wire.write(stream, rpcTaskFrame("busy"));
    await handler.started;
    await worker.close();
    await wire.untilStreamEnds(1);
    assert.deepEqual(wire.streamEnding(1), [{ type: RST_STREAM_FRAME, streamId: 1, isEndStream: false, errorCode: http2.constants.NGHTTP2_INTERNAL_ERROR }]);
  } finally { handler.release(); await worker.close(); await wire.close(); }
});

test("a malformed compressed task frame on a busy slot sends RST_STREAM without END_STREAM", async (context) => {
  context.mock.method(console, "warn", () => {});
  const wire = await startWireHarness("gzip");
  const handler = new GatedHandler();
  const worker = wire.worker(handler, 300);
  try {
    worker.start();
    const stream = await wire.nextStream();
    wire.write(stream, registeredFrame());
    wire.write(stream, rpcTaskFrame("busy"));
    await handler.started;
    // Compressed flag 1 with a 3-byte payload that is not gzip.
    stream.write(Buffer.from([1, 0, 0, 0, 3, 1, 2, 3]));
    await wire.untilStreamEnds(1);
    assert.equal(wire.streamEnding(1).at(-1)?.type, RST_STREAM_FRAME);
    assert.equal(wire.streamEnding(1).some((frame) => frame.isEndStream), false);
    await handler.aborted;
  } finally { handler.release(); await worker.close(); await wire.close(); }
});

test("an unencodable final output on a busy slot sends RST_STREAM without END_STREAM", async (context) => {
  context.mock.method(console, "error", () => {});
  context.mock.method(console, "warn", () => {});
  const wire = await startWireHarness();
  const handler = new GatedHandler({} as InvokeWorkerRPCResponse);
  const worker = wire.worker(handler, 300);
  try {
    worker.start();
    const stream = await wire.nextStream();
    wire.write(stream, registeredFrame());
    wire.write(stream, rpcTaskFrame("busy"));
    await handler.started;
    handler.release();
    await wire.untilStreamEnds(1);
    assert.equal(wire.streamEnding(1).at(-1)?.type, RST_STREAM_FRAME);
    assert.equal(wire.streamEnding(1).some((frame) => frame.isEndStream), false);
  } finally { handler.release(); await worker.close(); await wire.close(); }
});

test("before registration, a grpc-js internal cancel still sends END_STREAM then RST_STREAM", async (context) => {
  context.mock.method(console, "warn", () => {});
  const wire = await startWireHarness("gzip");
  const handler = new GatedHandler();
  const worker = wire.worker(handler, 300);
  try {
    worker.start();
    const stream = await wire.nextStream();
    // The reset hook is installed at Registered; before it, no task can run on the slot.
    stream.write(Buffer.from([1, 0, 0, 0, 3, 1, 2, 3]));
    await wire.untilStreamEnds(1);
    assert.deepEqual(wire.streamEnding(1), [
      { type: DATA_FRAME, streamId: 1, isEndStream: true },
      { type: RST_STREAM_FRAME, streamId: 1, isEndStream: false, errorCode: http2.constants.NGHTTP2_CANCEL },
    ]);
    assert.equal(handler.calls, 0);
  } finally { handler.release(); await worker.close(); await wire.close(); }
});

test("start fails before any handler runs when the reset hook cannot find the HTTP/2 stream", async (context) => {
  context.mock.method(console, "error", () => {});
  const wire = await startWireHarness();
  const handler = new GatedHandler();
  const worker = wire.worker(handler, 300);
  Reflect.set(worker, "findCallHttp2Streams", () => []);
  try {
    const starting = assert.rejects(worker.waitStarted(), /cannot find the HTTP\/2 stream below @grpc\/grpc-js 1\.14\.\d+/);
    worker.start();
    const stream = await wire.nextStream();
    wire.write(stream, registeredFrame());
    wire.write(stream, rpcTaskFrame("sent-after-registered"));
    await starting;
    await wire.untilStreamEnds(1);
    // The refused slot is idle, so it ends with a clean half-close and Dex returns the task unrun.
    assert.deepEqual(wire.streamEnding(1), [{ type: DATA_FRAME, streamId: 1, isEndStream: true }]);
    assert.equal(handler.calls, 0);
    assert.equal(worker.registration.snapshot().isReady, false);
  } finally { handler.release(); await worker.close(); await wire.close(); }
});

class GatedHandler {
  public calls = 0;
  public readonly started: Promise<void>;
  public readonly aborted: Promise<void>;
  public release!: () => void;
  private markStarted!: () => void;
  private markAborted!: () => void;
  private readonly gate = new Promise<void>((resolve) => { this.release = resolve; });

  public constructor(private readonly response: InvokeWorkerRPCResponse = InvokeWorkerRPCResponse.create()) {
    this.started = new Promise((resolve) => { this.markStarted = resolve; });
    this.aborted = new Promise((resolve) => { this.markAborted = resolve; });
  }

  public async invokeRPC(_request: unknown, signal: AbortSignal): Promise<InvokeWorkerRPCResponse> {
    this.calls++;
    signal.addEventListener("abort", () => this.markAborted(), { once: true });
    this.markStarted();
    await Promise.race([this.gate, this.aborted]);
    return this.response;
  }
}

/** Serves raw HTTP/2 gRPC frames and records every client frame through a TCP proxy. */
async function startWireHarness(responseEncoding?: "gzip") {
  const server = http2.createServer();
  const sessions = new Set<http2.ServerHttp2Session>();
  const sockets = new Set<net.Socket>();
  const streams: http2.ServerHttp2Stream[] = [];
  const frames: ClientFrame[] = [];
  let consumed = 0;
  server.on("session", (session) => { sessions.add(session); session.on("error", () => {}); });
  server.on("stream", (stream) => {
    stream.on("error", () => {});
    stream.on("data", () => {});
    stream.respond({ ":status": 200, "content-type": "application/grpc",
      ...(responseEncoding === undefined ? {} : { "grpc-encoding": responseEncoding }) });
    streams.push(stream);
  });
  server.listen(0, "127.0.0.1");
  await once(server, "listening");
  const proxy = net.createServer((client) => {
    const upstream = net.connect((server.address() as net.AddressInfo).port, "127.0.0.1");
    sockets.add(client);
    sockets.add(upstream);
    let pending = Buffer.alloc(0);
    let hasPreface = false;
    client.on("data", (data: Buffer) => {
      pending = Buffer.concat([pending, data]);
      if (!hasPreface) {
        if (pending.length < 24) return;
        pending = pending.subarray(24);
        hasPreface = true;
      }
      while (pending.length >= 9) {
        const length = pending.readUIntBE(0, 3);
        if (pending.length < 9 + length) break;
        const type = pending[3]!;
        const streamId = pending.readUInt32BE(5) & 0x7fffffff;
        if (streamId !== 0 && (type === DATA_FRAME || type === RST_STREAM_FRAME)) {
          frames.push({ type, streamId, isEndStream: (pending[4]! & 1) === 1,
            ...(type === RST_STREAM_FRAME ? { errorCode: pending.readUInt32BE(9) } : {}) });
        }
        pending = pending.subarray(9 + length);
      }
    });
    client.pipe(upstream).pipe(client);
    client.on("error", () => {});
    upstream.on("error", () => {});
  });
  proxy.listen(0, "127.0.0.1");
  await once(proxy, "listening");
  const address = `127.0.0.1:${(proxy.address() as net.AddressInfo).port}`;
  return {
    worker(handler: GatedHandler, stopDrainTimeoutMs: number): PullWorker {
      const worker = new PullWorker(new Registry([]), handler as unknown as WorkerDispatcher, address, {
        maxConcurrentTasks: 1, reservedRpcTasks: 0, stopDrainTimeoutMs,
      });
      Reflect.set(worker, "registeredFlows", new Map([["TestFlow", { steps: [], rpcs: [{ name: "getWorker" }] }]]));
      return worker;
    },
    async nextStream(): Promise<http2.ServerHttp2Stream> {
      await until(() => streams.length > consumed);
      return streams[consumed++]!;
    },
    write(stream: http2.ServerHttp2Stream, response: PollWorkerTaskResponse): void {
      const payload = PollWorkerTaskResponse.encode(response).finish();
      const header = Buffer.alloc(5);
      header.writeUInt32BE(payload.length, 1);
      stream.write(Buffer.concat([header, payload]));
    },
    /** Returns the frames that ended the stream: every END_STREAM DATA frame and RST_STREAM. */
    streamEnding(streamId: number): ClientFrame[] {
      return frames.filter((frame) => frame.streamId === streamId && (frame.isEndStream || frame.type === RST_STREAM_FRAME))
        .map((frame) => frame.type === DATA_FRAME ? { type: frame.type, streamId, isEndStream: true } : frame);
    },
    async untilStreamEnds(streamId: number): Promise<void> {
      await until(() => frames.some((frame) => frame.streamId === streamId && frame.type === RST_STREAM_FRAME)
        || frames.some((frame) => frame.streamId === streamId && frame.isEndStream));
      // Lets a RST_STREAM that follows an END_STREAM arrive before the caller inspects the frames.
      await new Promise<void>((resolve) => setTimeout(resolve, 100));
    },
    async close(): Promise<void> {
      for (const session of sessions) session.destroy();
      for (const socket of sockets) socket.destroy();
      await new Promise<void>((resolve) => proxy.close(() => resolve()));
      await new Promise<void>((resolve) => server.close(() => resolve()));
    },
  };
}

function registeredFrame(): PollWorkerTaskResponse {
  return { frame: { $case: "registered", value: { supportsUnknownNameRejection: true, maxConcurrentTasksPerAppWorker: 0 } } };
}

function rpcTaskFrame(taskId: string): PollWorkerTaskResponse {
  return { frame: { $case: "task", value: { taskId, deadline: undefined,
    request: { $case: "rpc", value: InvokeWorkerRPCRequest.create({ flowType: "TestFlow", rpcName: "getWorker" }) } } } };
}

async function until(condition: () => boolean, timeoutMs = 5_000): Promise<void> {
  const deadline = Date.now() + timeoutMs;
  while (!condition()) {
    if (Date.now() >= deadline) throw new Error("timed out waiting for HTTP/2 frames");
    await new Promise((resolve) => setTimeout(resolve, 5));
  }
}
