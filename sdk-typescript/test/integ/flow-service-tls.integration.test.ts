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
import { execFileSync, spawn, type ChildProcess } from "node:child_process";
import { createHash, randomBytes } from "node:crypto";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { createServer as createHttpServer, type Server as HttpServer } from "node:http";
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

interface TestCertificates {
  readonly caPem: string;
  readonly certificatePem: string;
  readonly privateKeyPem: string;
}

// Node.js cannot issue X.509 certificates, so the test uses the openssl CLI.
function createTestCertificates(directory: string): TestCertificates {
  const file = (name: string) => join(directory, name);
  writeFileSync(file("extensions.cnf"), [
    "[dex_test_ca]",
    "basicConstraints = critical, CA:TRUE",
    "keyUsage = critical, keyCertSign",
    "[dex_test_server]",
    "basicConstraints = CA:FALSE",
    "keyUsage = critical, digitalSignature",
    "extendedKeyUsage = serverAuth",
    "subjectAltName = DNS:localhost, IP:127.0.0.1, IP:::1",
    "",
  ].join("\n"));
  const openssl = (...arguments_: string[]) => execFileSync("openssl", arguments_, { stdio: "pipe" });
  openssl("ecparam", "-name", "prime256v1", "-genkey", "-noout", "-out", file("ca.key"));
  openssl("req", "-new", "-key", file("ca.key"), "-subj", "/CN=Dex test CA", "-out", file("ca.csr"));
  openssl("x509", "-req", "-in", file("ca.csr"), "-signkey", file("ca.key"), "-days", "1",
    "-extfile", file("extensions.cnf"), "-extensions", "dex_test_ca", "-out", file("ca.pem"));
  openssl("ecparam", "-name", "prime256v1", "-genkey", "-noout", "-out", file("server.key"));
  openssl("req", "-new", "-key", file("server.key"), "-subj", "/CN=localhost", "-out", file("server.csr"));
  openssl("x509", "-req", "-in", file("server.csr"), "-CA", file("ca.pem"), "-CAkey", file("ca.key"),
    "-set_serial", "2", "-days", "1", "-extfile", file("extensions.cnf"), "-extensions", "dex_test_server",
    "-out", file("server.pem"));
  return {
    caPem: readFileSync(file("ca.pem"), "utf8"),
    certificatePem: readFileSync(file("server.pem"), "utf8"),
    privateKeyPem: readFileSync(file("server.key"), "utf8"),
  };
}

class ServerZdProcess {
  private constructor(
    private readonly child: ChildProcess,
    private readonly exited: Promise<void>,
    public readonly port: number,
  ) {}

  public static async start(directory: string, configFile: string): Promise<ServerZdProcess> {
    const binary = process.env.DEX_SERVER_ZD_BINARY;
    if (binary === undefined || binary === "") {
      throw new Error("DEX_SERVER_ZD_BINARY must name a dex-server-zd binary; run ./run-integration-tests.sh");
    }
    const port = await availablePort();
    const child = spawn(binary, [
      "dev", "--data-dir", join(directory, "zd"), "--listen", `127.0.0.1:${port}`, "--config", configFile,
    ], { stdio: ["ignore", "pipe", "pipe"] });
    const exited = new Promise<void>((resolve) => child.once("exit", () => resolve()));
    let output = "";
    const ready = new Promise<void>((resolve, reject) => {
      const timer = setTimeout(() => reject(new Error(`dex-server-zd did not become ready:\n${output}`)), 60_000);
      const observe = (chunk: Buffer) => {
        output += chunk.toString("utf8");
        if (output.includes("dex-server-zd serving")) {
          clearTimeout(timer);
          resolve();
        }
      };
      child.stdout?.on("data", observe);
      child.stderr?.on("data", observe);
      void exited.then(() => {
        clearTimeout(timer);
        reject(new Error(`dex-server-zd exited before readiness:\n${output}`));
      });
    });
    const server = new ServerZdProcess(child, exited, port);
    try {
      await ready;
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
}

class TlsTerminatingProxy {
  private readonly sockets = new Set<Socket>();
  private readonly handshakes: { serverName: string | false | null; alpnProtocol: string | false | null }[] = [];
  private readonly server: tls.Server;

  private constructor(certificates: TestCertificates, private readonly upstreamPort: number) {
    this.server = tls.createServer({
      cert: certificates.certificatePem,
      key: certificates.privateKeyPem,
      ALPNProtocols: ["h2"],
      minVersion: "TLSv1.2",
    }, (downstream) => this.forward(downstream));
  }

  public static async start(certificates: TestCertificates, upstreamPort: number): Promise<TlsTerminatingProxy> {
    const proxy = new TlsTerminatingProxy(certificates, upstreamPort);
    await new Promise<void>((resolve) => proxy.server.listen(0, "127.0.0.1", resolve));
    return proxy;
  }

  public get port(): number {
    return (this.server.address() as AddressInfo).port;
  }

  public hasObservedHandshake(serverName: string, alpnProtocol: string): boolean {
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
  const certificates = createTestCertificates(directory);
  const blobCache = openBlobCache({ directory: join(directory, "cache"), maxBytes: 64 * 1_024 * 1_024 });
  fixtureCleanups.push(() => blobCache.close());
  const server = await ServerZdProcess.start(directory, configFile);
  fixtureCleanups.push(() => server.stop());
  await waitForAuthenticatedServer(blobCache, server.port, tokenFile);
  const proxy = await TlsTerminatingProxy.start(certificates, server.port);
  fixtureCleanups.push(() => proxy.close());
  const tokenEndpoint = await TokenEndpoint.start(token);
  fixtureCleanups.push(() => tokenEndpoint.close());
  fixture = { directory, token, tokenFile, caPem: certificates.caPem, server, proxy, tokenEndpoint, blobCache };
});

after(async () => {
  for (const cleanup of fixtureCleanups.reverse()) await cleanup();
});

test("Client and pull Worker use bearer credentials over TLS", async () => {
  const flowServiceTls: FlowServiceTlsOptions = { additionalRootCAsPem: readFileSync(join(fixture.directory, "ca.pem")) };
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
