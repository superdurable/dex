// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { connect, createServer, type Server, type Socket } from "node:net";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";

import { ChildProcessGroup } from "../../src/testing/child-process-group.js";
import { findFreeLoopbackPorts, pollUntil } from "../../src/testing/index.js";

// Short enough to keep a takeover near this long, long enough to survive a busy CI host.
const leaseDuration = "3s";
const readyTimeoutMs = 60_000;
const objectStoreReadyLine = "embedded S3 emulator serving";
const nodeServingLine = "dex-server-zd serving";
const standbyReadyLine = "another process holds the node lease";
const hotTakeoverLine = "is_serving_hot_standby_store=true";
const processCrashSafeConfig = "dev:\n  durabilityMode: processCrashSafe\n";

/** The two server failures the tests inject while a Worker keeps running. */
export type ServerFailureKind = "restart" | "takeover";

/** A Dex Server whose failure the test controls. Its address survives the failure. */
export interface CrashableServer {
  readonly serverAddress: string;
  readonly stateDirectory: string;
  /** Stops serving with SIGKILL. Running Workers lose their connections. */
  crash(): Promise<void>;
  /** Serves again from the state the crash left. */
  recover(): Promise<void>;
  /** Kills every process and deletes the state directory. */
  stop(): Promise<void>;
}

/** Starts a server for one kind of failure. */
export function startCrashableServer(kind: ServerFailureKind): Promise<CrashableServer> {
  return kind === "restart" ? RestartedServer.start() : TakenOverServer.start();
}

function serverZdPath(): string {
  const configured = process.env.DEX_SERVER_ZD_BINARY;
  if (configured !== undefined && configured !== "") {
    return configured;
  }
  const dexcli = process.env.DEXCLI;
  if (dexcli === undefined) {
    throw new Error("set DEX_SERVER_ZD_BINARY to the dex-server-zd executable");
  }
  return join(dirname(dexcli), "dex-server-zd");
}

function launchServerZd(
  description: string,
  logPath: string,
  commandArguments: readonly string[],
  readyLine: string,
): Promise<ChildProcessGroup> {
  return ChildProcessGroup.start({
    description,
    command: serverZdPath(),
    commandArguments,
    environment: process.env,
    logPath,
    readyLine,
    readyTimeoutMs,
  });
}

/** A single node that restarts on its data directory after SIGKILL. */
class RestartedServer implements CrashableServer {
  private constructor(
    public readonly serverAddress: string,
    public readonly stateDirectory: string,
    private readonly configPath: string,
    private node: ChildProcessGroup,
  ) {}

  public static async start(): Promise<RestartedServer> {
    const stateDirectory = mkdtempSync(join(tmpdir(), "dex-agents-restart-"));
    const configPath = join(stateDirectory, "server.yaml");
    writeFileSync(configPath, processCrashSafeConfig);
    const [port] = await findFreeLoopbackPorts(1);
    const serverAddress = `127.0.0.1:${port}`;
    const node = await RestartedServer.launch(stateDirectory, serverAddress, configPath);
    return new RestartedServer(serverAddress, stateDirectory, configPath, node);
  }

  private static launch(
    stateDirectory: string,
    serverAddress: string,
    configPath: string,
  ): Promise<ChildProcessGroup> {
    return launchServerZd(
      "restarted node",
      join(stateDirectory, "node.log"),
      ["dev", "--data-dir", join(stateDirectory, "data"), "--listen", serverAddress, "--config", configPath],
      nodeServingLine,
    );
  }

  public crash(): Promise<void> {
    return this.node.kill();
  }

  public async recover(): Promise<void> {
    this.node = await RestartedServer.launch(this.stateDirectory, this.serverAddress, this.configPath);
  }

  public async stop(): Promise<void> {
    await this.node.kill();
    rmSync(this.stateDirectory, { recursive: true, force: true });
  }
}

/** Forwards TCP connections to the serving node, as a load balancer does. */
class TcpRelay {
  private readonly connections = new Set<Socket>();

  private constructor(
    private readonly server: Server,
    public target: string,
  ) {}

  public static async start(target: string): Promise<TcpRelay> {
    const server = createServer();
    const relay = new TcpRelay(server, target);
    server.on("connection", (client) => relay.forward(client));
    await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
    return relay;
  }

  public get address(): string {
    const address = this.server.address();
    assert(address !== null && typeof address !== "string");
    return `127.0.0.1:${address.port}`;
  }

  public dropConnections(): void {
    for (const connection of this.connections) {
      connection.destroy();
    }
  }

  public close(): void {
    this.dropConnections();
    this.server.close();
  }

  private forward(client: Socket): void {
    const [host, port] = this.target.split(":");
    const upstream = connect(Number(port), host);
    for (const socket of [client, upstream]) {
      this.connections.add(socket);
      socket.on("error", () => undefined);
      socket.on("close", () => {
        this.connections.delete(socket);
        client.destroy();
        upstream.destroy();
      });
    }
    client.pipe(upstream);
    upstream.pipe(client);
  }
}

/** An active node and a hot standby sharing one S3 emulator, behind a TCP relay. */
class TakenOverServer implements CrashableServer {
  private constructor(
    public readonly stateDirectory: string,
    private readonly relay: TcpRelay,
    private readonly objectStore: ChildProcessGroup,
    private readonly active: ChildProcessGroup,
    private readonly standby: ChildProcessGroup,
    private readonly standbyAddress: string,
  ) {}

  public static async start(): Promise<TakenOverServer> {
    const stateDirectory = mkdtempSync(join(tmpdir(), "dex-agents-takeover-"));
    const [storePort, activePort, standbyPort] = await findFreeLoopbackPorts(3);
    const storeConfigPath = join(stateDirectory, "object-store.yaml");
    writeFileSync(storeConfigPath, processCrashSafeConfig);
    const objectStore = await launchServerZd(
      "object store",
      join(stateDirectory, "object-store.log"),
      [
        "dev",
        "--data-dir",
        join(stateDirectory, "object-store"),
        "--listen",
        `127.0.0.1:${storePort}`,
        "--config",
        storeConfigPath,
      ],
      objectStoreReadyLine,
    );
    const storeAddress = readObjectStoreAddress(objectStore.readLog());
    const launchNode = (name: string, address: string, readyLine: string): Promise<ChildProcessGroup> => {
      const configPath = join(stateDirectory, `${name}.yaml`);
      writeFileSync(configPath, nodeConfig(stateDirectory, name, storeAddress, address));
      return launchServerZd(`${name} node`, join(stateDirectory, `${name}.log`), ["serve", "--config", configPath], readyLine);
    };
    const activeAddress = `127.0.0.1:${activePort}`;
    const standbyAddress = `127.0.0.1:${standbyPort}`;
    const active = await launchNode("active", activeAddress, nodeServingLine);
    const standby = await launchNode("standby", standbyAddress, standbyReadyLine);
    const relay = await TcpRelay.start(activeAddress);
    return new TakenOverServer(stateDirectory, relay, objectStore, active, standby, standbyAddress);
  }

  public get serverAddress(): string {
    return this.relay.address;
  }

  public async crash(): Promise<void> {
    await this.active.kill();
    this.relay.dropConnections();
  }

  public async recover(): Promise<void> {
    await pollUntil(() => (this.standby.readLog().includes(nodeServingLine) ? true : undefined), {
      description: "the hot standby to serve",
      timeoutMs: 30_000,
    });
    assert.ok(this.standby.readLog().includes(hotTakeoverLine));
    this.relay.target = this.standbyAddress;
    this.relay.dropConnections();
  }

  public async stop(): Promise<void> {
    this.relay.close();
    await Promise.all([this.active.kill(), this.standby.kill(), this.objectStore.kill()]);
    rmSync(this.stateDirectory, { recursive: true, force: true });
  }
}

function readObjectStoreAddress(log: string): string {
  const line = log
    .split("\n")
    .find((candidate) => candidate.includes(objectStoreReadyLine));
  const address = line?.match(/address=(127\.0\.0\.1:\d+)/)?.[1];
  assert(address !== undefined, `the object store logged no address:\n${log}`);
  return address;
}

function nodeConfig(stateDirectory: string, name: string, objectStore: string, listen: string): string {
  return `cluster:
  nodeId: agent-test-node
  partitionCount: 4
objectStore:
  endpoint: http://${objectStore}
  region: us-east-1
  bucket: dex-dev
  prefix: takeover
  forcePathStyle: true
  credentialsSource: anonymous
localStorage:
  directory: ${join(stateDirectory, `${name}-store`)}
  blockCacheBytes: 16777216
lease:
  duration: ${leaseDuration}
  failIfHeld: false
standby:
  mode: hot
api:
  listen: "${listen}"
streamStore:
  backend: memory
search:
  backend: builtin
`;
}
