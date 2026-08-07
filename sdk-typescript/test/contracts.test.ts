// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import assert from "node:assert/strict";
import test from "node:test";

import {
  Attribute,
  Channel,
  Client,
  PhaseNotImplementedError,
  Registry,
  StepList,
  Timer,
  Wait,
  gracefulComplete,
  int64Codec,
  jsonCodec,
  openBlobCache,
  rpc,
  stringCodec,
  type BlobCache,
  type Context,
  type Flow,
  type RPCResult,
  type Step,
  type StepDecision,
} from "../src/index.js";

interface OrderInput {
  readonly orderId: string;
}

interface OrderOutput {
  readonly accepted: boolean;
}

const orderInput = jsonCodec<OrderInput>({
  typeName: "OrderInput",
  decode: (value) => {
    if (typeof value !== "object" || value === null || !("orderId" in value)) {
      throw new TypeError("invalid OrderInput");
    }
    const orderId = (value as { orderId: unknown }).orderId;
    if (typeof orderId !== "string") {
      throw new TypeError("invalid orderId");
    }
    return { orderId };
  },
});

const orderOutput = jsonCodec<OrderOutput>({
  typeName: "OrderOutput",
  decode: (value) => {
    if (typeof value !== "object" || value === null || !("accepted" in value)) {
      throw new TypeError("invalid OrderOutput");
    }
    return { accepted: Boolean((value as { accepted: unknown }).accepted) };
  },
});

const status = new Attribute("status", stringCodec);
const commands = new Channel("commands", orderInput);

class ApproveOrder implements Step<OrderInput> {
  public readonly inputCodec = orderInput;

  public getStepType(): string {
    return "ApproveOrder";
  }

  public waitFor(_context: Context, _input: OrderInput): Wait {
    return Wait.anyOf(
      commands.forOne(),
      Timer.byDuration(10_000, "approval-timeout"),
    );
  }

  public execute(_context: Context, input: OrderInput): StepDecision {
    return gracefulComplete(input.orderId);
  }
}

class ArchiveOrder implements Step<string> {
  public readonly inputCodec = stringCodec;

  public getStepType(): string {
    return "ArchiveOrder";
  }

  public execute(_context: Context, input: string): StepDecision {
    return gracefulComplete(input);
  }
}

class Orders implements Flow<OrderInput> {
  public readonly approve = new ApproveOrder();
  public readonly archive = new ArchiveOrder();

  public getFlowType(): string {
    return "Orders";
  }

  public getSteps() {
    return StepList.startStep(this.approve).otherSteps(this.archive);
  }

  public getPersistenceSchema() {
    return { attributes: [status], channels: [commands] };
  }

  @rpc({
    name: "GetOrder",
    inputCodec: orderInput,
    outputCodec: orderOutput,
    lockAttributes: [status.lock()],
  })
  public getOrder(_context: Context, _input: OrderInput): RPCResult<OrderOutput> {
    return { output: { accepted: true } };
  }
}

const orders = new Orders();
const blobCache = {} as BlobCache;

test("typed definitions construct without runtime", () => {
  const registry = new Registry([orders]);
  const definitions = [...orders.getSteps()];
  assert.equal(registry.flows[0]?.getFlowType(), "Orders");
  assert.equal(definitions[0]?.step, orders.approve);
  assert.equal(definitions[0]?.isStartStep, true);
  assert.equal(definitions[1]?.step, orders.archive);
  assert.equal(definitions[1]?.isStartStep, false);
});

test("registry rejects missing durable-name methods at runtime", () => {
  const missingFlowName = { getSteps: () => [] } as unknown as Flow<unknown>;
  assert.throws(() => new Registry([missingFlowName]), /must implement getFlowType/);

  const missingStepName = {
    inputCodec: stringCodec,
    execute: () => gracefulComplete(),
  } as unknown as Step<string>;
  const flow: Flow<string> = {
    getFlowType: () => "MissingStepName",
    getSteps: () => StepList.startStep(missingStepName),
  };
  assert.throws(() => new Registry([flow]), /must implement getStepType/);
});

test("canonical codecs enforce wire kinds and int64 range", () => {
  assert.equal(int64Codec.decode(int64Codec.encode(42n)), 42n);
  assert.throws(() => int64Codec.encode(2n ** 63n), RangeError);
  assert.throws(() => int64Codec.decode(stringCodec.encode("42")), TypeError);
});

test("fluent wait factories validate channel bounds", () => {
  const wait = Wait.allOf(Timer.byDuration(1_000));
  assert.equal(wait.conditions.length, 1);
  assert.throws(() => commands.range(), /requires a bound/);
});

test("registry rejects duplicate definitions", () => {
  assert.throws(() => new Registry([orders, orders]), /duplicate Flow Orders/);
});

test("blob cache contract validates config before the native phase", () => {
  assert.throws(
    () => openBlobCache({ directory: "contract-cache", maxBytes: 1024 }),
    PhaseNotImplementedError,
  );
  assert.throws(() => openBlobCache({ directory: "", maxBytes: 1024 }), TypeError);
});

test("runtime boundary fails explicitly", async () => {
  const client = new Client(new Registry([orders]), blobCache);
  await assert.rejects(
    client.startFlow(orders, "order-1", { orderId: "order-1" }),
    PhaseNotImplementedError,
  );
});

async function compileStrongTypes(client: Client): Promise<void> {
  const runId: string = await client.startFlow(orders, "order-1", { orderId: "order-1" });
  const output: OrderOutput = await client.invokeRPC(
    orders.getOrder,
    "order-1",
    { orderId: "order-1" },
  );
  void runId;
  void output;

  // @ts-expect-error wrong Flow input
  await client.startFlow(orders, "order-1", { accepted: true });
  // @ts-expect-error wrong RPC input
  await client.invokeRPC(orders.getOrder, "order-1", { accepted: true });
  // @ts-expect-error start Step input must match Flow input
  const mismatchedSteps: StepList<OrderInput> = StepList.startStep(orders.archive);
  void mismatchedSteps;
}

void compileStrongTypes;
