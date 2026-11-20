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

import assert from "node:assert/strict";
import { randomBytes } from "node:crypto";
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";

import {
  Client,
  PayloadEncryptionError,
  Registry,
  openBlobCache,
  stringCodec,
} from "@superdurable/dex";

import {
  SupportTicketFlow,
  startEncryptedSupportTicket,
  stopEncryptedSupportTicket,
  ticketMessage,
  ticketStatus,
} from "../../src/production/payload-encryption.js";
import {
  acquireIntegEnvironment,
  releaseIntegEnvironment,
} from "./environment.js";

test.before(async () => {
  await acquireIntegEnvironment();
});

test.after(async () => {
  await releaseIntegEnvironment();
});

/** Writes a key file in the dexcli codec-server keygen format. */
async function writePayloadKeyFile(directory: string): Promise<string> {
  const path = join(directory, "payload-keys.json");
  const keys = { k1: randomBytes(32).toString("base64") };
  await writeFile(path, JSON.stringify({ current_key_id: "k1", keys }), { mode: 0o600 });
  return path;
}

test("payload encryption stores the ticket message encrypted", async () => {
  const environment = await acquireIntegEnvironment();
  const directory = await mkdtemp(join(tmpdir(), "dex-payload-encryption-"));
  const cache = openBlobCache({ directory: join(directory, "cache"), maxBytes: 64 * 1024 * 1024 });
  const serverAddress = process.env.DEX_FLOW_SERVICE_ADDRESS ?? "127.0.0.1:8801";
  const keyFilePath = await writePayloadKeyFile(directory);
  const { flow, worker, client } = await startEncryptedSupportTicket(cache, serverAddress, keyFilePath);
  const clientWithoutCodec = new Client(new Registry([new SupportTicketFlow()]), cache, { serverAddress });
  try {
    const flowId = environment.newFlowId("support-ticket");
    await client.startFlow(flow, flowId, "My card was charged twice.", environment.startOptions());
    const result = await client.waitForFlow(flowId, 45_000);
    assert.equal(result.singleOutput(stringCodec), "received");
    assert.equal(await client.invokeRPC(flow.getTicketMessage, flowId), "My card was charged twice.");

    const [status] = await clientWithoutCodec.getAttributes(flowId, ticketStatus);
    assert.equal(status, "received");
    await assert.rejects(
      clientWithoutCodec.getAttributes(flowId, ticketMessage),
      (failure: unknown) => failure instanceof PayloadEncryptionError
        && /configure a PayloadCodec/.test(failure.message),
    );
  } finally {
    await clientWithoutCodec.close();
    await stopEncryptedSupportTicket(worker, client);
    cache.close();
    await rm(directory, { recursive: true, force: true });
    await releaseIntegEnvironment();
  }
});
