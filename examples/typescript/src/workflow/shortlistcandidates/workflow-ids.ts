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

import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { MessageChannel, Worker, receiveMessageOnPort } from "node:worker_threads";

import { DexError, ErrorSubStatus, type Client } from "@superdurable/dex";

import { getWorkerTarget } from "../../client-holder.js";
import { loadEnv } from "../../config/env.js";
import type { EmployerOptInFlow } from "./employer-opt-in-flow.js";

export function employerOptIn(employerId: string): string {
  return `shortlist_candidates_opt_in_${employerId}`;
}

export function shortlist(employerId: string, candidateId: string): string {
  return `shortlist_candidates_shortlist_${employerId}_${candidateId}`;
}

export async function isOptedIn(
  client: Client,
  employerOptInFlow: EmployerOptInFlow,
  employerId: string,
): Promise<boolean> {
  try {
    return Boolean(
      await client.invokeRPC(employerOptInFlow.isOptedIn, employerOptIn(employerId)),
    );
  } catch (failure) {
    if (failure instanceof DexError && failure.subStatus === ErrorSubStatus.FLOW_NOT_EXISTS) {
      return false;
    }
    throw failure;
  }
}

export function isOptedInSync(
  client: Client,
  _employerOptInFlow: EmployerOptInFlow,
  employerId: string,
): boolean {
  const serverAddress = client.options.serverAddress ?? loadEnv().serverAddress;
  const cacheDirectory = mkdtempSync(join(tmpdir(), "dex-typescript-examples-optin-"));
  const workerTargetAddress = getWorkerTarget()?.address;
  const { port1, port2 } = new MessageChannel();
  const lock = new Int32Array(new SharedArrayBuffer(4));
  const worker = new Worker(new URL("./is-opted-in-worker.js", import.meta.url), {
    workerData: {
      employerId,
      serverAddress,
      port: port2,
      lock,
      cacheDirectory,
      workerTargetAddress,
    },
    transferList: [port2],
  });
  worker.unref();
  Atomics.wait(lock, 0, 0);
  const message = receiveMessageOnPort(port1) as
    | { ok: true; result: boolean }
    | { ok: false; error: string }
    | undefined;
  port1.close();
  void worker.terminate();
  if (message === undefined) {
    throw new Error("isOptedIn worker returned no message");
  }
  if (!message.ok) {
    throw new Error(message.error);
  }
  return message.result;
}
