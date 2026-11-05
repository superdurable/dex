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
import { createHash, randomBytes } from "node:crypto";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { createServer as createHttpServer, type IncomingHttpHeaders, type Server as HttpServer } from "node:http";
import {
  createSecureServer as createSecureHttp2Server,
  type Http2SecureServer,
  type ServerHttp2Session,
  type ServerHttp2Stream,
} from "node:http2";
import { connect, createServer as createTcpServer, type AddressInfo, type Socket } from "node:net";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test, { after, before } from "node:test";
import * as tls from "node:tls";

import { status } from "@grpc/grpc-js";

import {
  ApiKeyCredential,
  Attribute,
  Client,
  DexServiceError,
  FileCredential,
  Registry,
  StepList,
  Worker,
  gracefulComplete,
  openBlobCache,
  rpc,
  stringCodec,
  type BlobCache,
  type Context,
  type Flow,
  type FlowServiceTlsOptions,
  type PersistenceSchema,
  type RPCResult,
  type Step,
  type StepDecision,
} from "../../src/index.js";
import { createTestCertificates, type TestCertificates } from "../tls-test-certificates.js";
import { flowId } from "./environment.js";

const tlsTransportValue = new Attribute("tls-transport-value", stringCodec);

class TlsTransportStep implements Step<string> {
  public readonly inputCodec = stringCodec;

  public getStepType(): string {
    return "TlsTransportStep";
  }

  public execute(context: Context, input: string): StepDecision {
    tlsTransportValue.set(context, input);
    return gracefulComplete(input);
  }
}

class TlsTransportFlow implements Flow<string> {
  public getFlowType(): string {
    return "TlsTransportFlow";
  }

  public getSteps(): StepList<string> {
    return StepList.startStep(new TlsTransportStep());
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { attributes: [tlsTransportValue] };
  }

  @rpc({ outputCodec: stringCodec })
  public getTransportValue(context: Context): RPCResult<string> {
    return { output: tlsTransportValue.get(context) };
  }
}

interface TlsFixture {
  readonly directory: string;
  readonly token: string;
  readonly tokenFile: string;
  readonly certificates: TestCertificates;
  readonly caPem: string;
  readonly server: ServerZdProcess;
  readonly proxy: TlsTerminatingProxy;
  readonly tokenEndpoint: TokenEndpoint;
  readonly blobCache: BlobCache;
}

function causeChain(failure: unknown): string {
  const messages: string[] = [];
  for (let current = failure; current instanceof Error; current = current.cause) {
    messages.push(current.message, current instanceof DexServiceError ? current.detail : "");
  }
  return messages.join("\n");
}

class ServerZdProcess {
  private output = "";
  private readonly exited: Promise<void>;
  private readonly ready: Promise<void>;
  private readonly readinessTimer: ReturnType<typeof setTimeout>;
  private resolveReady!: () => void;
  private rejectReady!: (failure: Error) => void;

  private constructor(private readonly child: ChildProcess, public readonly port: number) {
    this.exited = new Promise((resolve) => child.once("exit", () => resolve()));
    this.ready = new Promise((resolve, reject) => {
      this.resolveReady = resolve;
      this.rejectReady = reject;
    });
    this.readinessTimer = setTimeout(() => this.rejectReady(
      new Error(`dex-server-zd did not become ready:\n${this.output}`)), 60_000);
    child.stdout?.on("data", (chunk: Buffer) => this.observeOutput(chunk));
    child.stderr?.on("data", (chunk: Buffer) => this.observeOutput(chunk));
    void this.exited.then(() => this.rejectBeforeReadiness());
  }

  public static async start(directory: string, configFile: string): Promise<ServerZdProcess> {
    const binary = process.env.DEX_SERVER_ZD_BINARY;
    if (binary === undefined || binary === "") {
      throw new Error("DEX_SERVER_ZD_BINARY must name a dex-server-zd binary; run ./run-integration-tests.sh");
    }
    const port = await availablePort();
    const child = spawn(binary, [
      "dev", "--data-dir", join(directory, "zd"), "--listen", `127.0.0.1:${port}`, "--config", configFile,
    ], { stdio: ["ignore", "pipe", "pipe"] });
    const server = new ServerZdProcess(child, port);
    try {
      await server.ready;
    } catch (failure) {
      await server.stop();
      throw failure;
    }
    return server;
  }

  public async stop(): Promise<void> {
    if (this.child.exitCode === null && this.child.signalCode === null) this.child.kill("SIGTERM");
    const killTimer = setTimeout(() => this.child.kill("SIGKILL"), 15_000);
    await this.exited;
    clearTimeout(killTimer);
  }

  private observeOutput(chunk: Buffer): void {
    this.output += chunk.toString("utf8");
    if (this.output.includes("dex-server-zd serving")) {
      clearTimeout(this.readinessTimer);
      this.resolveReady();
    }
  }

  private rejectBeforeReadiness(): void {
    clearTimeout(this.readinessTimer);
    this.rejectReady(new Error(`dex-server-zd exited before readiness:\n${this.output}`));
  }
}

class TlsTerminatingProxy {
  private readonly sockets = new Set<Socket>();
  private readonly handshakes: { serverName: string | false | null; alpnProtocol: string | false | null }[] = [];
  private readonly server: tls.Server;

  private constructor(
    certificates: TestCertificates,
    private readonly upstreamPort: number,
    alpnProtocols: readonly string[],
  ) {
    this.server = tls.createServer({
      cert: certificates.certificatePem,
      key: certificates.privateKeyPem,
      minVersion: "TLSv1.2",
      ...(alpnProtocols.length === 0 ? {} : { ALPNProtocols: [...alpnProtocols] }),
    }, (downstream) => this.forward(downstream));
  }

  public static async start(
    certificates: TestCertificates,
    upstreamPort: number,
    alpnProtocols: readonly string[] = ["h2"],
  ): Promise<TlsTerminatingProxy> {
    const proxy = new TlsTerminatingProxy(certificates, upstreamPort, alpnProtocols);
    await new Promise<void>((resolve) => proxy.server.listen(0, "127.0.0.1", resolve));
    return proxy;
  }

  public get port(): number {
    return (this.server.address() as AddressInfo).port;
  }

  public hasObservedHandshake(serverName: string, alpnProtocol: string | false): boolean {
    return this.handshakes.some((handshake) =>
      handshake.serverName === serverName && handshake.alpnProtocol === alpnProtocol);
  }

  public async close(): Promise<void> {
    const closed = new Promise<void>((resolve) => this.server.close(() => resolve()));
    for (const socket of this.sockets) socket.destroy();
    await closed;
  }

  private forward(downstream: tls.TLSSocket): void {
    this.handshakes.push({ serverName: downstream.servername, alpnProtocol: downstream.alpnProtocol });
    const upstream = connect(this.upstreamPort, "127.0.0.1");
    for (const socket of [downstream, upstream]) {
      this.sockets.add(socket);
      socket.once("close", () => this.sockets.delete(socket));
      // Peers reset connections during teardown and rejected handshakes; closing the pair is enough.
      socket.on("error", () => {
        downstream.destroy();
        upstream.destroy();
      });
    }
    downstream.pipe(upstream);
    upstream.pipe(downstream);
  }
}

interface RecordedRequest {
  readonly authority: string | undefined;
  readonly serverName: string | false | null | undefined;
}

class Http2AuthorityRecorder {
  public readonly requests: RecordedRequest[] = [];
  private readonly sessions = new Set<ServerHttp2Session>();
  private readonly server: Http2SecureServer;

  private constructor(certificates: TestCertificates) {
    this.server = createSecureHttp2Server({ cert: certificates.certificatePem, key: certificates.privateKeyPem });
    this.server.on("session", (session: ServerHttp2Session) => this.trackSession(session));
    this.server.on("stream", (stream: ServerHttp2Stream, headers: IncomingHttpHeaders) => this.record(stream, headers));
  }

  public static async start(certificates: TestCertificates): Promise<Http2AuthorityRecorder> {
    const recorder = new Http2AuthorityRecorder(certificates);
    await new Promise<void>((resolve) => recorder.server.listen(0, "127.0.0.1", resolve));
    return recorder;
  }

  public get port(): number {
    return (this.server.address() as AddressInfo).port;
  }

  public async close(): Promise<void> {
    const closed = new Promise<void>((resolve) => this.server.close(() => resolve()));
    for (const session of this.sessions) session.destroy();
    await closed;
  }

  private trackSession(session: ServerHttp2Session): void {
    this.sessions.add(session);
    session.once("close", () => this.sessions.delete(session));
  }

  private record(stream: ServerHttp2Stream, headers: IncomingHttpHeaders): void {
    const socket = stream.session?.socket as tls.TLSSocket | undefined;
    const authority = headers[":authority"];
    this.requests.push({ authority: typeof authority === "string" ? authority : undefined, serverName: socket?.servername });
    stream.respond({
      ":status": 200, "content-type": "application/grpc", "grpc-status": String(status.UNIMPLEMENTED),
    }, { endStream: true });
  }
}

class TokenEndpoint {
  public exchangeCount = 0;
  private readonly server: HttpServer;

  private constructor(token: string) {
    this.server = createHttpServer((request, response) => {
      this.exchangeCount += 1;
      request.resume();
      response.setHeader("content-type", "application/json");
      response.end(JSON.stringify({ access_token: token, token_type: "Bearer", expires_in: 3_600 }));
    });
  }

  public static async start(token: string): Promise<TokenEndpoint> {
    const endpoint = new TokenEndpoint(token);
    await new Promise<void>((resolve) => endpoint.server.listen(0, "127.0.0.1", resolve));
    return endpoint;
  }

  public get url(): string {
    return `http://127.0.0.1:${(this.server.address() as AddressInfo).port}/token`;
  }

  public async close(): Promise<void> {
    this.server.closeAllConnections();
    await new Promise<void>((resolve) => this.server.close(() => resolve()));
  }
}

// The listener can accept connections before FlowService serves, so wait for a real call.
async function waitForAuthenticatedServer(blobCache: BlobCache, port: number, tokenFile: string): Promise<void> {
  const client = new Client(new Registry([]), blobCache, {
    serverAddress: `127.0.0.1:${port}`, credential: new FileCredential(tokenFile),
  });
  try {
    const deadline = Date.now() + 60_000;
    for (;;) {
      try {
        await client.searchFlows("", 1);
        return;
      } catch (failure) {
        if (Date.now() > deadline) throw new Error("dex-server-zd did not serve FlowService", { cause: failure });
      }
      await new Promise((resolve) => setTimeout(resolve, 100));
    }
  } finally {
    await client.close();
  }
}

async function availablePort(): Promise<number> {
  const server = createTcpServer();
  await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
  const { port } = server.address() as AddressInfo;
  await new Promise<void>((resolve) => server.close(() => resolve()));
  return port;
}

const flow = new TlsTransportFlow();
const registry = new Registry([flow]);
let fixture: TlsFixture;
const fixtureCleanups: (() => void | Promise<void>)[] = [];

before(async () => {
  const directory = mkdtempSync(join(tmpdir(), "dex-typescript-tls-"));
  fixtureCleanups.push(() => rmSync(directory, { recursive: true, force: true }));
  const token = randomBytes(24).toString("hex");
  const tokenFile = join(directory, "token");
  writeFileSync(tokenFile, `${token}\n`);
  const tokenHashFile = join(directory, "token-hashes.yaml");
  writeFileSync(tokenHashFile, `tokens:\n  - sha256: ${createHash("sha256").update(token).digest("hex")}\n`);
  const configFile = join(directory, "server-zd.yaml");
  writeFileSync(configFile, `auth:\n  authenticators:\n    - kind: staticTokens\n      file: ${JSON.stringify(tokenHashFile)}\n`);
  const certificates = createTestCertificates(directory, "primary");
  const blobCache = openBlobCache({ directory: join(directory, "cache"), maxBytes: 64 * 1_024 * 1_024 });
  fixtureCleanups.push(() => blobCache.close());
  const server = await ServerZdProcess.start(directory, configFile);
  fixtureCleanups.push(() => server.stop());
  await waitForAuthenticatedServer(blobCache, server.port, tokenFile);
  const proxy = await TlsTerminatingProxy.start(certificates, server.port);
  fixtureCleanups.push(() => proxy.close());
  const tokenEndpoint = await TokenEndpoint.start(token);
  fixtureCleanups.push(() => tokenEndpoint.close());
  fixture = {
    directory, token, tokenFile, certificates, caPem: certificates.caPem, server, proxy, tokenEndpoint, blobCache,
  };
});

after(async () => {
  for (const cleanup of fixtureCleanups.reverse()) await cleanup();
});

test("Client and pull Worker use bearer credentials over TLS", async () => {
  const flowServiceTls: FlowServiceTlsOptions = { additionalRootCAsPem: readFileSync(fixture.certificates.caFile) };
  const serverAddress = `localhost:${fixture.proxy.port}`;
  const worker = new Worker(registry, fixture.blobCache, {
    serverAddress,
    flowServiceTls,
    credential: new FileCredential(fixture.tokenFile),
    pull: { appWorkerID: flowId("tls-worker") },
  });
  const client = new Client(registry, fixture.blobCache, {
    serverAddress,
    flowServiceTls,
    credential: new ApiKeyCredential({ tokenUrl: fixture.tokenEndpoint.url, serverId: "dex-test", apiKey: "test-api-key" }),
  });
  try {
    await worker.start();
    await worker.waitPullRegistered({ timeoutMs: 30_000 });
    const id = flowId("tls-transport");
    const value = `tls-value-${id}`;
    await client.startFlow(flow, id, value);
    assert.equal((await client.waitForFlow(id, 30_000)).singleOutput(stringCodec), value);
    assert.equal(await client.invokeRPC(flow.getTransportValue, id), value);
    assert.deepEqual(await client.getAttributes(id, tlsTransportValue), [value]);
  } finally {
    await client.close();
    await worker.close();
  }
  assert.ok(fixture.tokenEndpoint.exchangeCount > 0, "the Client exchanged its API key");
  assert.ok(fixture.proxy.hasObservedHandshake("localhost", "h2"), "the proxy saw SNI localhost with ALPN h2");
});

test("a TLS call without a bearer token is rejected", async () => {
  const client = new Client(registry, fixture.blobCache, {
    serverAddress: `localhost:${fixture.proxy.port}`,
    flowServiceTls: { additionalRootCAsPem: fixture.caPem },
  });
  try {
    await assert.rejects(client.startFlow(flow, flowId("tls-no-token"), "value"), (failure: unknown) =>
      failure instanceof DexServiceError && failure.code === status.UNAUTHENTICATED);
  } finally {
    await client.close();
  }
});

test("a wrong serverName fails certificate verification for the Client and Worker", async () => {
  const flowServiceTls = { additionalRootCAsPem: fixture.caPem, serverName: "wrong.example" };
  const serverAddress = `localhost:${fixture.proxy.port}`;
  const credential = new FileCredential(fixture.tokenFile);
  const client = new Client(registry, fixture.blobCache, { serverAddress, flowServiceTls, credential });
  try {
    await assert.rejects(client.searchFlows("", 1), (failure: unknown) =>
      failure instanceof DexServiceError && failure.code === status.UNAVAILABLE
      && failure.detail.includes("wrong.example"));
  } finally {
    await client.close();
  }
  const worker = new Worker(registry, fixture.blobCache, {
    serverAddress, flowServiceTls, credential, attributeIndexSyncTimeoutMs: 5_000, pull: {},
  });
  try {
    await assert.rejects(worker.start(), (failure: unknown) =>
      failure instanceof Error && failure.message.startsWith("cannot start TypeScript pull Worker")
      && causeChain(failure).includes("wrong.example"));
  } finally {
    await worker.close();
  }
});

test("TLS without the test CA, and plaintext to a TLS endpoint, both fail", async () => {
  const serverAddress = `localhost:${fixture.proxy.port}`;
  const credential = new FileCredential(fixture.tokenFile);
  for (const flowServiceTls of [{}, undefined]) {
    const client = new Client(registry, fixture.blobCache, {
      serverAddress, credential, ...(flowServiceTls === undefined ? {} : { flowServiceTls }),
    });
    try {
      await assert.rejects(client.searchFlows("", 1), (failure: unknown) =>
        failure instanceof DexServiceError && failure.code === status.UNAVAILABLE
        && (flowServiceTls === undefined || failure.detail.includes("verify")));
    } finally {
      await client.close();
    }
  }
});

test("a server without ALPN is accepted and an http/1.1-only server is rejected", async () => {
  const flowServiceTls = { additionalRootCAsPem: fixture.caPem };
  const credential = new FileCredential(fixture.tokenFile);
  const withoutAlpn = await TlsTerminatingProxy.start(fixture.certificates, fixture.server.port, []);
  const httpOneOnly = await TlsTerminatingProxy.start(fixture.certificates, fixture.server.port, ["http/1.1"]);
  try {
    // This pins a documented difference from Go, which rejects servers without ALPN.
    const accepted = new Client(registry, fixture.blobCache, {
      serverAddress: `localhost:${withoutAlpn.port}`, flowServiceTls, credential,
    });
    try {
      await accepted.searchFlows("", 1);
    } finally {
      await accepted.close();
    }
    assert.ok(withoutAlpn.hasObservedHandshake("localhost", false), "the proxy negotiated no ALPN protocol");
    const rejected = new Client(registry, fixture.blobCache, {
      serverAddress: `localhost:${httpOneOnly.port}`, flowServiceTls, credential,
    });
    try {
      await assert.rejects(rejected.searchFlows("", 1), (failure: unknown) =>
        failure instanceof DexServiceError && failure.code === status.UNAVAILABLE
        && /no application protocol/i.test(failure.detail));
    } finally {
      await rejected.close();
    }
  } finally {
    await withoutAlpn.close();
    await httpOneOnly.close();
  }
});

test("serverName sets SNI and :authority for an IP address target", async () => {
  const recorder = await Http2AuthorityRecorder.start(fixture.certificates);
  try {
    for (const [serverAddress, serverName, expectedAuthority] of [
      [`127.0.0.1:${recorder.port}`, "localhost", "localhost"],
      [`localhost:${recorder.port}`, undefined, `localhost:${recorder.port}`],
    ] as const) {
      const client = new Client(registry, fixture.blobCache, {
        serverAddress,
        flowServiceTls: { additionalRootCAsPem: fixture.caPem, ...(serverName === undefined ? {} : { serverName }) },
      });
      try {
        await assert.rejects(client.searchFlows("", 1), (failure: unknown) =>
          failure instanceof DexServiceError && failure.code === status.UNIMPLEMENTED);
      } finally {
        await client.close();
      }
      assert.deepEqual(recorder.requests.pop(), { authority: expectedAuthority, serverName: "localhost" }, serverAddress);
    }
  } finally {
    await recorder.close();
  }
});
