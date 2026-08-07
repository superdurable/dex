/*
 * Copyright (c) 2022-2026 Super Durable, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

import { pathToFileURL } from "node:url";

import express from "express";

import { Client, Worker, openBlobCache } from "@superdurable/dex";

import { setClient } from "./client-holder.js";
import { startCronSchedule } from "./config/cron-schedule-starter.js";
import { loadEnv } from "./config/env.js";
import { createEngagementRouter } from "./controller/engagement-workflow-controller.js";
import { createJobPostRouter } from "./controller/job-post-controller.js";
import { createMicroserviceRouter } from "./controller/microservice-workflow-controller.js";
import { createMoneyTransferRouter } from "./controller/money-transfer-workflow-controller.js";
import { createPollingRouter } from "./controller/polling-controller.js";
import { createShortlistCandidatesRouter } from "./controller/shortlist-candidates-controller.js";
import { createSignupRouter } from "./controller/signup-workflow-controller.js";
import { createSubscriptionRouter } from "./controller/subscription-workflow-controller.js";
import { registerDesignPatternRoutes } from "./patterns/controller/design-pattern-controller.js";
import {
  backoffPollingFlow,
  createExampleRegistry,
  drainInternalChannelsFlow,
  drainSignalChannelsFlow,
  failureRecoveryFlow,
  flowGracefulTimeout,
  interruptibleExecutionFlow,
  manualInterventionFlow,
  parallelStatesWithAwaitFlow,
  parentFlowV2,
  reminderFlow,
  requestReceiverFlow,
  resettableTimerFlow,
  simpleParallelStatesFlow,
  simplePollingFlow,
  storageFlow,
  waitForStateCompletionFlow,
} from "./registry.js";

export interface SampleServer {
  readonly client: Client;
  readonly worker: Worker;
  readonly syncWorker: Worker;
  readonly httpServer: ReturnType<express.Express["listen"]>;
  close(): Promise<void>;
}

export async function startSampleServer(): Promise<SampleServer> {
  const env = loadEnv();
  const registry = createExampleRegistry();
  const blobCache = openBlobCache({
    directory: env.blobCacheDir,
    maxBytes: 1 << 30,
  });
  const syncBlobCache = openBlobCache({
    directory: `${env.blobCacheDir}/sync-worker`,
    maxBytes: 1 << 30,
  });
  const worker = new Worker(registry, blobCache, {
    bindAddress: env.workerBindAddress,
    serverAddress: env.serverAddress,
    ...(env.workerTarget !== undefined
      ? { workerTarget: { address: env.workerTarget } }
      : {}),
  });
  const syncWorker = new Worker(registry, syncBlobCache, {
    bindAddress: env.syncWorkerBindAddress,
    serverAddress: env.serverAddress,
  });
  await worker.start();
  await syncWorker.start();
  const client = new Client(registry, blobCache, {
    serverAddress: env.serverAddress,
    workerTarget: worker.workerTarget,
  });
  setClient(client, syncWorker.workerTarget);

  const app = express();
  app.use(express.json());
  app.use("/moneytransfer", createMoneyTransferRouter(client));
  app.use("/microservice", createMicroserviceRouter(client));
  app.use("/engagement", createEngagementRouter(client));
  app.use("/subscription", createSubscriptionRouter(client));
  app.use("/polling", createPollingRouter(client));
  app.use("/signup", createSignupRouter(client));
  app.use("/jobpost", createJobPostRouter(client));
  app.use("/shortlist_candidates", createShortlistCandidatesRouter(client));
  registerDesignPatternRoutes(app, client, {
    simplePollingFlow,
    backoffPollingFlow,
    interruptibleExecutionFlow,
    reminderFlow,
    storageFlow,
    manualInterventionFlow,
    resettableTimerFlow,
    simpleParallelStatesFlow,
    parallelStatesWithAwaitFlow,
    failureRecoveryFlow,
    requestReceiverFlow,
    parentFlowV2,
    drainInternalChannelsFlow,
    drainSignalChannelsFlow,
    waitForStateCompletionFlow,
    flowGracefulTimeout,
  });
  app.use(
    (
      error: unknown,
      _request: express.Request,
      response: express.Response,
      _next: express.NextFunction,
    ) => {
      const message = error instanceof Error ? error.message : String(error);
      response.status(500).type("text/plain").send(message);
    },
  );
  await startCronSchedule(client);

  const httpServer = await new Promise<ReturnType<express.Express["listen"]>>(
    (resolve, reject) => {
      const { host, port } = parseHttpAddress(env.httpAddress);
      const server = app.listen(port, host, () => {
        resolve(server);
      });
      server.on("error", reject);
    },
  );

  return {
    client,
    worker,
    syncWorker,
    httpServer,
    async close() {
      await new Promise<void>((resolve, reject) => {
        httpServer.close((error) => {
          if (error !== undefined) {
            reject(error);
            return;
          }
          resolve();
        });
      });
      await Promise.race([
        (async () => {
          await client.close();
          await worker.close();
          await syncWorker.close();
          blobCache.close();
          syncBlobCache.close();
        })(),
        new Promise<void>((resolve) => {
          setTimeout(resolve, 3_000);
        }),
      ]);
    },
  };
}

function parseHttpAddress(address: string): { host: string; port: number } {
  const [host, portText] = address.includes(":")
    ? (address.split(":") as [string, string])
    : ["127.0.0.1", address];
  return { host, port: Number(portText) };
}

async function main(): Promise<void> {
  const server = await startSampleServer();
  const env = loadEnv();
  console.log(
    `Dex TypeScript examples listening on http://${env.httpAddress} (worker ${env.workerBindAddress}, sync ${env.syncWorkerBindAddress})`,
  );
  const shutdown = async () => {
    await server.close();
    process.exit(0);
  };
  process.on("SIGINT", () => {
    void shutdown();
  });
  process.on("SIGTERM", () => {
    void shutdown();
  });
}

if (import.meta.url === pathToFileURL(process.argv[1] ?? "").href) {
  main().catch((error) => {
    console.error(error);
    process.exit(1);
  });
}
