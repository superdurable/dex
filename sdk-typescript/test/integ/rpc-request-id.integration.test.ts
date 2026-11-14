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
import { randomUUID } from "node:crypto";
import test from "node:test";
import { status } from "@grpc/grpc-js";

import { DexServiceError, FlowNotActiveOrNotFoundError } from "../../src/index.js";
import { expectError, flowId, withEnvironment } from "./environment.js";
import { RpcRequestIdFlow } from "./rpc_request_id_flow.js";

// A retried call with the same request ID must apply once and skip the Worker.
test("transactional RPC repeated with one request ID applies once", async () => {
  const flow = new RpcRequestIdFlow();
  await withEnvironment([flow], async ({ client }) => {
    const id = flowId("rpc-request-id-repeat");
    await client.startFlow(flow, id, undefined);
    const options = { requestId: `event-${randomUUID()}` };
    const first = await client.invokeRPCWithOptions(flow.addTransactional, id, 5, options);
    const repeated = await client.invokeRPCWithOptions(flow.addTransactional, id, 5, options);
    assert.equal(first, 5);
    assert.equal(repeated, 5);
    assert.equal(await client.invokeRPC(flow.getTotal, id), 5);
    assert.equal(flow.transactionalHandlerCalls, 1);
    await client.stopFlow(id);
  });
});

// Reusing a request ID for different input must fail instead of returning a stale reply.
test("request ID reused with different input fails with INVALID_ARGUMENT", async () => {
  const flow = new RpcRequestIdFlow();
  await withEnvironment([flow], async ({ client }) => {
    const id = flowId("rpc-request-id-conflict");
    await client.startFlow(flow, id, undefined);
    const requestId = `event-${randomUUID()}`;
    assert.equal(await client.invokeRPCWithOptions(flow.addTransactional, id, 1, { requestId }), 1);
    const failure = await expectError(
      client.invokeRPCWithOptions(flow.addTransactional, id, 2, { requestId }),
      DexServiceError,
    );
    assert.equal(failure.constructor, DexServiceError);
    assert.equal(failure.code, status.INVALID_ARGUMENT);
    assert.match(failure.detail, /already used for a different request/);
    assert.equal(failure.operation, "invokeRPC");
    assert.equal(await client.invokeRPC(flow.getTotal, id), 1);
    assert.equal(flow.transactionalHandlerCalls, 1);
    await client.stopFlow(id);
  });
});

// Omitting the request ID keeps the existing behavior: every call is a new request.
test("RPC calls without a request ID apply every time", async () => {
  const flow = new RpcRequestIdFlow();
  await withEnvironment([flow], async ({ client }) => {
    const id = flowId("rpc-request-id-absent");
    await client.startFlow(flow, id, undefined);
    assert.equal(await client.invokeRPCWithOptions(flow.addTransactional, id, 3, {}), 3);
    assert.equal(await client.invokeRPCWithOptions(flow.addTransactional, id, 3, {}), 6);
    assert.equal(await client.invokeRPC(flow.addTransactional, id, 3), 9);
    assert.equal(flow.transactionalHandlerCalls, 3);
    const failure = await expectError(
      client.invokeRPCWithOptions(flow.addTransactional, id, 3, { requestId: "" }),
      DexServiceError,
    );
    assert.equal(failure.code, status.INVALID_ARGUMENT);
    assert.equal(await client.invokeRPC(flow.getTotal, id), 9);
    await client.stopFlow(id);
  });
});

// A non-transactional RPC reruns its handler, but Dex keeps the first committed result.
test("non-transactional RPC repeated with one request ID reruns the handler and applies once", async () => {
  const flow = new RpcRequestIdFlow();
  await withEnvironment([flow], async ({ client }) => {
    const id = flowId("rpc-request-id-non-transactional");
    await client.startFlow(flow, id, undefined);
    const options = { requestId: `event-${randomUUID()}` };
    assert.equal(await client.invokeRPCWithOptions(flow.addNonTransactional, id, 4, options), 4);
    assert.equal(await client.invokeRPCWithOptions(flow.addNonTransactional, id, 4, options), 4);
    assert.equal(await client.invokeRPC(flow.getTotal, id), 4);
    assert.equal(flow.nonTransactionalHandlerCalls, 2);
    await client.stopFlow(id);
  });
});

// A non-transactional retry that returns no Flow changes gets its new output, not the recorded one.
test("zero-effect retry returns the recorded reply only for a transactional RPC", async () => {
  const flow = new RpcRequestIdFlow();
  await withEnvironment([flow], async ({ client }) => {
    const nonTransactionalId = flowId("rpc-request-id-zero-effect");
    await client.startFlow(flow, nonTransactionalId, undefined);
    const nonTransactionalOptions = { requestId: `event-${randomUUID()}` };
    assert.equal(
      await client.invokeRPCWithOptions(
        flow.markProcessedNonTransactional,
        nonTransactionalId,
        nonTransactionalOptions,
      ),
      "accepted",
    );
    assert.equal(
      await client.invokeRPCWithOptions(
        flow.markProcessedNonTransactional,
        nonTransactionalId,
        nonTransactionalOptions,
      ),
      "alreadyProcessed",
    );
    await client.stopFlow(nonTransactionalId);

    const transactionalId = flowId("rpc-request-id-zero-effect-transactional");
    await client.startFlow(flow, transactionalId, undefined);
    const transactionalOptions = { requestId: `event-${randomUUID()}` };
    for (let attempt = 0; attempt < 2; attempt++) {
      assert.equal(
        await client.invokeRPCWithOptions(
          flow.markProcessedTransactional,
          transactionalId,
          transactionalOptions,
        ),
        "accepted",
      );
    }
    await client.stopFlow(transactionalId);
  });
});

// A delivery retried after the Flow closed must still get its earlier reply.
test("transactional RPC repeated after the Flow closes returns the recorded reply", async () => {
  const flow = new RpcRequestIdFlow();
  await withEnvironment([flow], async ({ client }) => {
    const id = flowId("rpc-request-id-closed");
    await client.startFlow(flow, id, undefined);
    const options = { requestId: `event-${randomUUID()}` };
    assert.equal(await client.invokeRPCWithOptions(flow.addTransactional, id, 7, options), 7);
    await client.stopFlow(id);
    await client.waitForFlow(id, 30_000);
    assert.equal(await client.invokeRPCWithOptions(flow.addTransactional, id, 7, options), 7);
    assert.equal(flow.transactionalHandlerCalls, 1);
    await expectError(
      client.invokeRPCWithOptions(flow.addTransactional, id, 7, {
        requestId: `event-${randomUUID()}`,
      }),
      FlowNotActiveOrNotFoundError,
    );
    assert.equal(flow.transactionalHandlerCalls, 1);
  });
});
