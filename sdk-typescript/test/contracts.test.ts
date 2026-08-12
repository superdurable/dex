// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import assert from "node:assert/strict";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";

import {
  Attribute,
  AttributeMap,
  Channel,
  ChannelMap,
  Client,
  ConditionCombination,
  FlowDefinitionError,
  InvalidStepResultError,
  Registry,
  StepList,
  Timer,
  ValueMappingError,
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
  type FlowConfig,
  type RPCResult,
  type Step,
  type StepDecision,
} from "../src/index.js";
import {
  mapAttributeStoreName,
  mapAttributeStoreSync,
} from "../src/attribute-store-sync.js";
import {
  Context as ProtoContext,
  InvokeExecuteMethodRequest,
  InvokeWaitForMethodRequest,
} from "../src/gen/dex.js";
import { encodeValue, type ValueHydrator } from "../src/value-mapper.js";
import { WorkerDispatcher } from "../src/worker-dispatcher.js";
import { registeredFlowByName } from "../src/flow.js";
import { InvocationContext } from "../src/invocation-context.js";

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

test("attribute store synchronization is opt-in and immutable", () => {
  const plain = new Attribute("plain", stringCodec);
  const synced = plain.syncToAttributeStore();
  const syncedMap = new AttributeMap("map", stringCodec).syncToAttributeStore();
  const absent: FlowConfig = {};
  const named: FlowConfig = { attributeStoreName: "profiles" };
  const disabled: FlowConfig = { attributeStoreName: "" };

  assert.equal(mapAttributeStoreSync(plain), undefined);
  assert.equal(mapAttributeStoreSync(synced)?.enabled, true);
  assert.equal(mapAttributeStoreSync(syncedMap)?.enabled, true);
  assert.equal(mapAttributeStoreName(absent), undefined);
  assert.equal(mapAttributeStoreName(named), "profiles");
  assert.equal(mapAttributeStoreName(disabled), "");
});

test("registry rejects missing durable-name methods at runtime", () => {
  const missingFlowName = { getSteps: () => [] } as unknown as Flow<unknown>;
  assert.throws(() => new Registry([missingFlowName]), FlowDefinitionError);

  const missingStepName = {
    inputCodec: stringCodec,
    execute: () => gracefulComplete(),
  } as unknown as Step<string>;
  const flow: Flow<string> = {
    getFlowType: () => "MissingStepName",
    getSteps: () => StepList.startStep(missingStepName),
  };
  assert.throws(() => new Registry([flow]), FlowDefinitionError);
});

test("canonical codecs enforce wire kinds and int64 range", () => {
  assert.equal(int64Codec.decode(int64Codec.encode(42n)), 42n);
  assert.throws(() => int64Codec.encode(2n ** 63n), RangeError);
  assert.throws(() => int64Codec.decode(stringCodec.encode("42")), TypeError);
});

test("fluent wait factories validate channel bounds", () => {
  const wait = Wait.until(Timer.byDuration(1_000));
  assert.equal(wait.conditions.length, 1);
  assert.throws(() => commands.range(), /requires a bound/);
});

test("registry rejects duplicate definitions", () => {
  assert.throws(() => new Registry([orders, orders]), FlowDefinitionError);
});

test("value mapping failures have a stable error type", () => {
  const invalid = {
    get orderId(): string {
      throw new TypeError("invalid order input");
    },
  };
  assert.throws(
    () => encodeValue(orderInput, invalid),
    ValueMappingError,
  );
});

test("invalid Step results include Flow and Step context", async () => {
  class InvalidStep implements Step<string> {
    public readonly inputCodec = stringCodec;

    public getStepType(): string {
      return "InvalidStep";
    }

    public execute(_context: Context, _input: string): StepDecision {
      return undefined as unknown as StepDecision;
    }
  }

  class InvalidFlow implements Flow<string> {
    public readonly start = new InvalidStep();

    public getFlowType(): string {
      return "InvalidFlow";
    }

    public getSteps(): StepList<string> {
      return StepList.startStep(this.start);
    }
  }

  const flow = new InvalidFlow();
  const hydrator = {
    hydrateAll: async (values: readonly unknown[]) => values,
  } as unknown as ValueHydrator;
  const dispatcher = new WorkerDispatcher(new Registry([flow]), hydrator);
  const invocation = dispatcher.invokeExecute(
    InvokeExecuteMethodRequest.create({
      context: ProtoContext.create(),
      flowType: flow.getFlowType(),
      stepType: flow.start.getStepType(),
      stepInput: encodeValue(stringCodec, "input"),
      attributes: [],
      stepExeLocals: [],
    }),
  );
  await assert.rejects(invocation, (failure: unknown) => {
    assert.ok(failure instanceof InvalidStepResultError);
    assert.equal(failure.flowType, "InvalidFlow");
    assert.equal(failure.stepType, "InvalidStep");
    assert.equal(failure.method, "execute");
    return true;
  });
});

test("Worker maps only user-provided Condition IDs", async () => {
  const conditionChannel = new Channel("condition-commands", stringCodec);
  class ConditionStep implements Step<string> {
    public readonly inputCodec = stringCodec;

    public getStepType(): string {
      return "ConditionStep";
    }

    public waitFor(_context: Context, input: string): Wait {
      if (input === "unnamed") {
        return Wait.anyOf(Timer.byDuration(1_000), conditionChannel.forOne());
      }
      if (input === "missing") {
        return Wait.anyCombinationOf(ConditionCombination.of(conditionChannel.forOne()));
      }
      if (input === "duplicate") {
        return Wait.anyCombinationOf(
          ConditionCombination.of(
            conditionChannel.forOne("same"),
            Timer.byDuration(1_000, "same"),
          ),
        );
      }
      const reused = conditionChannel.forOne("__dex_internal_condition_0");
      return Wait.anyCombinationOf(
        ConditionCombination.of(reused),
        ConditionCombination.of(reused),
      );
    }

    public execute(_context: Context, input: string): StepDecision {
      return gracefulComplete(input);
    }
  }
  class ConditionFlow implements Flow<string> {
    public readonly start = new ConditionStep();

    public getFlowType(): string {
      return "ConditionFlow";
    }

    public getSteps(): StepList<string> {
      return StepList.startStep(this.start);
    }

    public getPersistenceSchema() {
      return { channels: [conditionChannel] };
    }
  }
  const flow = new ConditionFlow();
  const hydrator = {
    hydrateAll: async (values: readonly unknown[]) => values,
  } as unknown as ValueHydrator;
  const dispatcher = new WorkerDispatcher(new Registry([flow]), hydrator);
  const invoke = (input: string) =>
    dispatcher.invokeWaitFor(
      InvokeWaitForMethodRequest.create({
        context: ProtoContext.create(),
        flowType: flow.getFlowType(),
        stepType: flow.start.getStepType(),
        stepInput: encodeValue(stringCodec, input),
      }),
    );
  const unnamed = await invoke("unnamed");
  assert.equal(unnamed.waitingCondition?.timerConditions[0]?.conditionId, "");
  assert.equal(unnamed.waitingCondition?.channelConditions[0]?.conditionId, "");
  const reused = await invoke("reused");
  assert.equal(
    reused.waitingCondition?.channelConditions[0]?.conditionId,
    "__dex_internal_condition_0",
  );
  assert.deepEqual(
    reused.waitingCondition?.conditionCombinations.map((combination) => combination.conditionIds),
    [["__dex_internal_condition_0"], ["__dex_internal_condition_0"]],
  );
  await assert.rejects(invoke("missing"), /requires every Condition/);
  await assert.rejects(invoke("duplicate"), /duplicate Condition ID/);
  assert.throws(() => conditionChannel.forOne(""), /must not be empty/);
});

test("map introspection tracks buffered changes", () => {
  const attributes = new AttributeMap("items", stringCodec);
  const channels = new ChannelMap("messages", stringCodec);
  class MapFlow implements Flow<void> {
    public getFlowType(): string {
      return "MapFlow";
    }

    public getSteps(): StepList<void> {
      return StepList.empty();
    }

    public getPersistenceSchema() {
      return { attributes: [attributes], channels: [channels] };
    }
  }
  const registry = new Registry([new MapFlow()]);
  const special = "special / key";
  const physical = (name: string, instance: string) =>
    `${name}/${encodeURIComponent(instance).replace(/[!'()*]/g, (character) =>
      `%${character.charCodeAt(0).toString(16).toUpperCase()}`,
    )}`;
  const context = new InvocationContext(
    "rpc",
    registeredFlowByName(registry, "MapFlow"),
    ProtoContext.create(),
    [
      { key: physical("items", special), value: encodeValue(stringCodec, "initial") },
      { key: physical("items", "z"), value: encodeValue(stringCodec, "remove") },
    ],
    [],
    undefined,
    {
      [physical("messages", special)]: { size: 1 },
      [physical("messages", "empty")]: { size: 0 },
    },
  );
  assert.deepEqual(attributes.getAllInstanceKeys(context), [special, "z"]);
  attributes.set(context, "a", "added");
  attributes.delete(context, "z");
  assert.deepEqual(attributes.getAllInstanceKeys(context), ["a", special]);
  assert.equal(attributes.getMapSize(context), 2);
  assert.deepEqual(channels.getAllInstanceKeys(context), [special]);
  channels.publish(context, "a", "published");
  assert.deepEqual(channels.getAllInstanceKeys(context), ["a", special]);
  assert.equal(channels.getMapSize(context), 2);
});

test("blob cache contract opens the native DXBC cache", () => {
  const directory = mkdtempSync(join(tmpdir(), "dex-typescript-blob-cache-"));
  const cache = openBlobCache({ directory, maxBytes: 1024, frequencyCounters: 0 });
  try {
    assert.equal(cache.put("blob", Buffer.from("payload")), true);
    assert.deepEqual(cache.get("blob"), new Uint8Array(Buffer.from("payload")));
    cache.delete("blob");
    assert.equal(cache.get("blob"), undefined);
  } finally {
    cache.close();
    rmSync(directory, { recursive: true, force: true });
  }
  assert.throws(() => openBlobCache({ directory: "", maxBytes: 1024 }), TypeError);
});

async function compileStrongTypes(client: Client): Promise<void> {
  const runId: string = await client.startFlow(orders, "order-1", { orderId: "order-1" });
  const output: OrderOutput = await client.invokeRPC(
    orders.getOrder,
    "order-1",
    { orderId: "order-1" },
  );
  await client.waitForAttributeEqual("order-1", status, "ready", 30_000);
  await client.waitForAttributeEqual(
    "order-1",
    new AttributeMap("items", stringCodec),
    "one",
    "ready",
    30_000,
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
