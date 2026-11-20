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

// The TypeScript Client decrypts what each SDK's Worker encrypts, and each Worker
// decrypts the TypeScript Client's input. Runs only from
// examples/run-payload-encryption-interop-tests.sh.

import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";

import { Client, PayloadEncryptionError, openBlobCache, stringCodec } from "@superdurable/dex";

import {
  expectedFailureText,
  expectedOutput,
  expectedSecretText,
  expectedStatusText,
  failInputPrefix,
  newClientFlows,
  newClientRegistry,
  newFilePayloadCodec,
  payloadInteropOwners,
  requiredEnv,
  secretText,
  statusText,
} from "./payload-encryption-interop-flow.js";

const waitTimeoutMs = 45_000;

test("TypeScript Client reads every Worker's payloads", async (context) => {
  const serverAddress = requiredEnv("DEX_FLOW_SERVICE_ADDRESS");
  const payloadCodec = await newFilePayloadCodec(requiredEnv("DEX_PAYLOAD_INTEROP_KEY_FILE"));
  const directory = await mkdtemp(join(tmpdir(), "dex-payload-interop-"));
  const cache = openBlobCache({ directory: join(directory, "cache"), maxBytes: 64 * 1024 * 1024 });
  const flows = newClientFlows();
  const client = new Client(newClientRegistry(flows), cache, { serverAddress, payloadCodec });
  const clientWithoutCodec = new Client(newClientRegistry(flows), cache, { serverAddress });
  try {
    for (const owner of payloadInteropOwners) {
      const flow = flows.get(owner);
      assert.ok(flow !== undefined);

      await context.test(`${owner} Worker output and encrypted Attribute`, async () => {
        const input = `TypeScript Client input for ${owner}: naïve café ✓`;
        const flowId = `payload-interop-${owner}-${randomUUID()}`;
        await client.startFlow(flow, flowId, input);
        const result = await client.waitForFlow(flowId, waitTimeoutMs);
        assert.equal(result.status, "completed", result.errorMessage);
        assert.equal(result.singleOutput(stringCodec), expectedOutput(owner, input));
        const [secret] = await client.getAttributes(flowId, secretText);
        assert.equal(secret, expectedSecretText(owner, input));

        const [status] = await clientWithoutCodec.getAttributes(flowId, statusText);
        assert.equal(status, expectedStatusText(owner));
        await assert.rejects(
          clientWithoutCodec.getAttributes(flowId, secretText),
          (failure: unknown) => failure instanceof PayloadEncryptionError
            && /configure a PayloadCodec/.test(failure.message),
        );
      });

      await context.test(`${owner} Worker error text`, async () => {
        const failureInput = "TypeScript Client failure input ✓";
        const flowId = `payload-interop-failure-${owner}-${randomUUID()}`;
        await client.startFlow(flow, flowId, failInputPrefix + failureInput);
        const result = await client.waitForFlow(flowId, waitTimeoutMs);
        assert.equal(result.status, "failed");
        assert.ok(result.errorMessage?.includes(expectedFailureText(owner, failureInput)),
          `unexpected error text: ${result.errorMessage}`);
      });
    }
  } finally {
    await clientWithoutCodec.close();
    await client.close();
    cache.close();
    await rm(directory, { recursive: true, force: true });
  }
});
