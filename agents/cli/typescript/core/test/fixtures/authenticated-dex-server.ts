// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { execFileSync, spawn, type ChildProcess } from "node:child_process";
import { createHash, randomBytes } from "node:crypto";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { createServer as createHttpServer, type Server as HttpServer } from "node:http";
import { connect, createServer as createTcpServer, type AddressInfo, type Socket } from "node:net";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import * as tls from "node:tls";

import { Client, FileCredential, Registry, openBlobCache } from "@superdurable/dex";

const readyLine = "dex-server-zd serving";
const startTimeoutMs = 60_000;

/** The API key and server ID the token endpoint accepts. */
export const acceptedApiKey = "dex_test_key_for_agents_cli";
export const acceptedServerId = "dex-agents-cli-test";

/**
 * A `dex-server-zd dev` that accepts one static bearer token, a TLS proxy in front of it with a test CA, and a token
 * endpoint that exchanges {@link acceptedApiKey} for that token.
 */
export class AuthenticatedDexServer {
  private constructor(
    /** The directory of the server's state, the CA, and the token file. */
    public readonly directory: string,
    /** The bearer token the server accepts. */
    public readonly token: string,
    /** A file that holds {@link token}. */
    public readonly tokenFile: string,
    /** The test CA's PEM file. */
    public readonly caFile: string,
    private readonly server: ChildProcess,
    private readonly serverPort: number,
    private readonly proxy: tls.Server,
    private readonly proxySockets: Set<Socket>,
    private readonly tokenEndpoint: HttpServer,
    private readonly exchanges: { count: number },
  ) {}

  /**
   * Starts the server, the TLS proxy, and the token endpoint.
   * @returns The running fixture.
   */
  public static async start(): Promise<AuthenticatedDexServer> {
    const binary = process.env.DEX_SERVER_ZD_BINARY;
    if (binary === undefined || binary === "") {
      throw new Error("DEX_SERVER_ZD_BINARY must name a dex-server-zd binary");
    }
    const directory = mkdtempSync(join(tmpdir(), "dex-agents-cli-auth-"));
    const token = randomBytes(24).toString("hex");
    const tokenFile = join(directory, "dex.token");
    writeFileSync(tokenFile, `${token}\n`);
    const hashFile = join(directory, "token-hashes.yaml");
    writeFileSync(hashFile, `tokens:\n  - sha256: ${createHash("sha256").update(token).digest("hex")}\n`);
    const configFile = join(directory, "server-zd.yaml");
    writeFileSync(
      configFile,
      `auth:\n  authenticators:\n    - kind: staticTokens\n      file: ${JSON.stringify(hashFile)}\n` +
        "dev:\n  durabilityMode: processCrashSafe\n",
    );
    const serverPort = await availablePort();
    const server = spawn(
      binary,
      ["dev", "--data-dir", join(directory, "zd"), "--listen", `127.0.0.1:${serverPort}`, "--config", configFile],
      { stdio: ["ignore", "pipe", "pipe"] },
    );
    await waitForReadyLine(server);
    await waitForFlowService(directory, serverPort, tokenFile);
    const certificates = createTestCertificates(directory);
    const proxySockets = new Set<Socket>();
    const proxy = tls.createServer(
      { cert: certificates.certificatePem, key: certificates.privateKeyPem, minVersion: "TLSv1.2", ALPNProtocols: ["h2"] },
      (downstream) => forward(downstream, serverPort, proxySockets),
    );
    await new Promise<void>((resolve) => proxy.listen(0, "127.0.0.1", resolve));
    const exchanges = { count: 0 };
    const tokenEndpoint = createHttpServer((request, response) => {
      let body = "";
      request.on("data", (chunk: Buffer) => {
        body += chunk.toString("utf8");
      });
      request.on("end", () => {
        exchanges.count += 1;
        const fields = JSON.parse(body) as { api_key?: unknown; server_id?: unknown };
        response.setHeader("content-type", "application/json");
        if (fields.api_key !== acceptedApiKey || fields.server_id !== acceptedServerId) {
          response.statusCode = 401;
          response.end(JSON.stringify({ error: "invalid_api_key" }));
          return;
        }
        response.end(JSON.stringify({ access_token: token, token_type: "Bearer", expires_in: 3_600 }));
      });
    });
    await new Promise<void>((resolve) => tokenEndpoint.listen(0, "127.0.0.1", resolve));
    return new AuthenticatedDexServer(
      directory,
      token,
      tokenFile,
      certificates.caFile,
      server,
      serverPort,
      proxy,
      proxySockets,
      tokenEndpoint,
      exchanges,
    );
  }

  /** The plaintext server URL for `dex.server`. */
  public get plaintextUrl(): string {
    return `http://127.0.0.1:${this.serverPort}`;
  }

  /** The TLS proxy's URL for `dex.server`; its certificate names localhost and 127.0.0.1. */
  public get tlsUrl(): string {
    return `https://localhost:${(this.proxy.address() as AddressInfo).port}`;
  }

  /** The token endpoint's loopback http URL. */
  public get tokenUrl(): string {
    return `http://127.0.0.1:${(this.tokenEndpoint.address() as AddressInfo).port}/token`;
  }

  /** How many exchange requests the token endpoint answered. */
  public get exchangeCount(): number {
    return this.exchanges.count;
  }

  /** Stops everything and removes the directory. */
  public async stop(): Promise<void> {
    this.tokenEndpoint.closeAllConnections();
    await new Promise<void>((resolve) => this.tokenEndpoint.close(() => resolve()));
    const proxyClosed = new Promise<void>((resolve) => this.proxy.close(() => resolve()));
    for (const socket of this.proxySockets) {
      socket.destroy();
    }
    await proxyClosed;
    if (this.server.exitCode === null && this.server.signalCode === null) {
      const exited = new Promise<void>((resolve) => this.server.once("exit", () => resolve()));
      this.server.kill("SIGTERM");
      const killTimer = setTimeout(() => this.server.kill("SIGKILL"), 15_000);
      await exited;
      clearTimeout(killTimer);
    }
    rmSync(this.directory, { recursive: true, force: true });
  }
}

async function waitForReadyLine(server: ChildProcess): Promise<void> {
  let output = "";
  const collect = (chunk: Buffer): void => {
    output += chunk.toString("utf8");
  };
  server.stdout?.on("data", collect);
  server.stderr?.on("data", collect);
  const deadline = Date.now() + startTimeoutMs;
  while (!output.includes(readyLine)) {
    if (server.exitCode !== null || Date.now() >= deadline) {
      server.kill("SIGKILL");
      throw new Error(`dex-server-zd did not become ready:\n${output}`);
    }
    await delay(100);
  }
}

/** The listener can accept connections before FlowService serves, so wait for a real call. */
async function waitForFlowService(directory: string, port: number, tokenFile: string): Promise<void> {
  const blobCache = openBlobCache({ directory: join(directory, "probe-cache"), maxBytes: 1024 * 1024 });
  const client = new Client(new Registry([]), blobCache, {
    serverAddress: `127.0.0.1:${port}`,
    credential: new FileCredential(tokenFile),
  });
  try {
    const deadline = Date.now() + startTimeoutMs;
    for (;;) {
      try {
        await client.searchFlows("", 1);
        return;
      } catch (error: unknown) {
        if (Date.now() >= deadline) {
          throw new Error("dex-server-zd did not serve FlowService", { cause: error });
        }
      }
      await delay(100);
    }
  } finally {
    await client.close();
    blobCache.close();
  }
}

function forward(downstream: tls.TLSSocket, upstreamPort: number, sockets: Set<Socket>): void {
  const upstream = connect(upstreamPort, "127.0.0.1");
  for (const socket of [downstream, upstream]) {
    sockets.add(socket);
    socket.once("close", () => sockets.delete(socket));
    // Peers reset connections during teardown; closing the pair is enough.
    socket.on("error", () => {
      downstream.destroy();
      upstream.destroy();
    });
  }
  downstream.pipe(upstream);
  upstream.pipe(downstream);
}

async function availablePort(): Promise<number> {
  const server = createTcpServer();
  await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
  const { port } = server.address() as AddressInfo;
  await new Promise<void>((resolve) => server.close(() => resolve()));
  return port;
}

/** Node.js cannot issue X.509 certificates, so the fixture uses the openssl CLI. */
function createTestCertificates(directory: string): {
  readonly caFile: string;
  readonly certificatePem: string;
  readonly privateKeyPem: string;
} {
  const file = (name: string): string => join(directory, name);
  writeFileSync(
    file("extensions.cnf"),
    [
      "[dex_test_ca]",
      "basicConstraints = critical, CA:TRUE",
      "keyUsage = critical, keyCertSign",
      "[dex_test_server]",
      "basicConstraints = CA:FALSE",
      "keyUsage = critical, digitalSignature",
      "extendedKeyUsage = serverAuth",
      "subjectAltName = DNS:localhost, IP:127.0.0.1",
      "",
    ].join("\n"),
  );
  const openssl = (...arguments_: string[]): void => {
    execFileSync("openssl", arguments_, { stdio: "pipe" });
  };
  openssl("ecparam", "-name", "prime256v1", "-genkey", "-noout", "-out", file("ca.key"));
  openssl("req", "-new", "-key", file("ca.key"), "-subj", "/CN=Dex agents CLI test CA", "-out", file("ca.csr"));
  openssl("x509", "-req", "-in", file("ca.csr"), "-signkey", file("ca.key"), "-days", "1",
    "-extfile", file("extensions.cnf"), "-extensions", "dex_test_ca", "-out", file("ca.pem"));
  openssl("ecparam", "-name", "prime256v1", "-genkey", "-noout", "-out", file("server.key"));
  openssl("req", "-new", "-key", file("server.key"), "-subj", "/CN=localhost", "-out", file("server.csr"));
  openssl("x509", "-req", "-in", file("server.csr"), "-CA", file("ca.pem"), "-CAkey", file("ca.key"),
    "-set_serial", "2", "-days", "1", "-extfile", file("extensions.cnf"), "-extensions", "dex_test_server",
    "-out", file("server.pem"));
  return {
    caFile: file("ca.pem"),
    certificatePem: readFileSync(file("server.pem"), "utf8"),
    privateKeyPem: readFileSync(file("server.key"), "utf8"),
  };
}
