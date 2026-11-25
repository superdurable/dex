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
import test from "node:test";

import {
  Attribute,
  AttributeMap,
  Channel,
  ChannelMap,
  FlowDefinitionError,
  InitialAttribute,
  Registry,
  RpcLockConflictError,
  WorkerInvocationError,
  booleanCodec,
  doubleCodec,
  stringCodec,
  type Client,
  type PersistenceSchema,
} from "../../src/index.js";
import {
  AttributeLockRequirementInvalidFlow,
  AttributeLockRequirementRPCFlow,
  AttributeLockRequirementStepFlow,
  AttributeLockRequirementTimeoutFlow,
  LockSnapshotMovementFlow,
  LockSnapshotStartFlow,
  lockReqFree,
  lockReqItems,
  lockReqShared,
  lockReqSpacedInstance,
  lockReqTotal,
  lockSnapshotGate,
  lockSnapshotRPCLocks,
  resetLockSnapshotGate,
} from "./attribute_lock_requirement_flow.js";
import { expectError, flowId, withEnvironment } from "./environment.js";

const missingLockErrorType = "MissingAttributeLockError";

type WrittenKey =
  | { readonly attribute: Attribute<number> }
  | { readonly attributeMap: AttributeMap<number>; readonly instance: string };

async function isSet(client: Client, id: string, key: WrittenKey): Promise<boolean> {
  const [value] = "attribute" in key
    ? await client.getAttributes(id, key.attribute)
    : await client.getAttributes(id, key.attributeMap.instance(key.instance));
  return value !== undefined;
}

async function rejectsWithMissingLock(operation: Promise<unknown>, fragment: string): Promise<void> {
  await assert.rejects(operation, (failure: unknown) => {
    assert.ok(failure instanceof WorkerInvocationError, String(failure));
    assert.equal(failure.workerErrorType, missingLockErrorType);
    assert.ok(
      failure.workerErrorDetail.includes(fragment),
      `${failure.workerErrorDetail} does not contain ${fragment}`,
    );
    return true;
  });
}

test("declared Attribute writes from Steps need a registered lock", async () => {
  const stepFlow = new AttributeLockRequirementStepFlow();
  const cases: readonly {
    readonly stepCase: string;
    // The expected error detail fragment, or undefined when the Step succeeds.
    readonly missing?: string;
    // Every key the Step writes; a failed Step leaves all of them unset.
    readonly written: readonly WrittenKey[];
  }[] = [
    {
      stepCase: "unlocked-total",
      missing: `Step "LockReqUnlockedTotalStep" execute wrote Attribute "lock-req-total" `
        + `without the required lock of Attribute "lock-req-total"`,
      written: [{ attribute: lockReqTotal }],
    },
    {
      stepCase: "movement-lock-total",
      missing: `wrote Attribute "lock-req-total"`,
      written: [{ attribute: lockReqTotal }],
    },
    { stepCase: "locked-total", written: [{ attribute: lockReqTotal }] },
    {
      stepCase: "locked-item",
      written: [{ attributeMap: lockReqItems, instance: lockReqSpacedInstance }],
    },
    {
      stepCase: "other-item",
      missing: `wrote AttributeMap "lock-req-items" instance "item b" without the required lock of `
        + `AttributeMap "lock-req-items" instance "item b"`,
      written: [
        { attributeMap: lockReqItems, instance: lockReqSpacedInstance },
        { attributeMap: lockReqItems, instance: "item b" },
      ],
    },
    { stepCase: "locked-shared", written: [{ attributeMap: lockReqShared, instance: "x" }] },
    {
      stepCase: "unlocked-shared",
      missing: `wrote AttributeMap "lock-req-shared" instance "x" without the required lock of `
        + `Attribute "lock-req-coordination"`,
      written: [{ attributeMap: lockReqShared, instance: "x" }],
    },
    { stepCase: "undeclared", written: [{ attribute: lockReqFree }] },
  ];
  await withEnvironment([stepFlow], async ({ client }) => {
    for (const testCase of cases) {
      const id = flowId(`lock-req-${testCase.stepCase}`);
      await client.startFlow(stepFlow, id, testCase.stepCase);
      const result = await client.waitForFlow(id, 30_000);
      assert.equal(result.status, "completed", testCase.stepCase);
      const output = result.singleOutput(stringCodec);
      if (testCase.missing === undefined) {
        assert.equal(output, "written", testCase.stepCase);
      } else {
        assert.ok(output.startsWith(`${missingLockErrorType}: `), output);
        assert.ok(output.includes(testCase.missing), output);
      }
      for (const [index, key] of testCase.written.entries()) {
        assert.equal(
          await isSet(client, id, key),
          testCase.missing === undefined,
          `key ${index} written by ${testCase.stepCase}`,
        );
      }
    }

    const waitForId = flowId("lock-req-wait-for-total");
    await client.startFlow(stepFlow, waitForId, "wait-for-total");
    const waitForOutput = (await client.waitForFlow(waitForId, 30_000)).singleOutput(stringCodec);
    assert.ok(waitForOutput.startsWith(`${missingLockErrorType}: `), waitForOutput);
    assert.ok(
      waitForOutput.includes(`Step "LockReqWaitForTotalStep" waitFor wrote Attribute "lock-req-total"`),
      waitForOutput,
    );
    assert.equal(await isSet(client, waitForId, { attribute: lockReqTotal }), false,
      "the failed waitFor write is discarded");

    const lockedWaitForId = flowId("lock-req-locked-wait-for-total");
    await client.startFlow(stepFlow, lockedWaitForId, "locked-wait-for-total");
    assert.equal((await client.waitForFlow(lockedWaitForId, 30_000)).singleOutput(stringCodec), "written");
    assert.equal(await isSet(client, lockedWaitForId, { attribute: lockReqTotal }), true);
  });
});

test("a Flow timeout handler is not checked", async () => {
  const timeoutFlow = new AttributeLockRequirementTimeoutFlow();
  await withEnvironment([timeoutFlow], async ({ client }) => {
    const id = flowId("lock-req-timeout");
    await client.startFlow(timeoutFlow, id, undefined, { timeoutMs: 1_000 });
    const result = await client.waitForFlow(id, 30_000);
    assert.equal(result.status, "completed");
    assert.equal(result.singleOutput(stringCodec), "expired");
    const [total] = await client.getAttributes(id, lockReqTotal);
    assert.equal(total, 5);
  });
});

test("declared RPC writes and publishes need a registered lock", async () => {
  const rpcFlow = new AttributeLockRequirementRPCFlow();
  await withEnvironment([rpcFlow], async ({ client }) => {
    const publishId = flowId("lock-req-publish");
    await client.startFlow(rpcFlow, publishId, undefined);
    await client.invokeRPC(rpcFlow.publishReplyLocked, publishId, "hi");
    assert.equal(await client.invokeRPC(rpcFlow.countReplies, publishId), 1, "the locked publish commits");
    await rejectsWithMissingLock(
      client.invokeRPC(rpcFlow.publishReplyUnlocked, publishId, "hi"),
      `RPC "publishReplyUnlocked" published to Channel "lock-req-replies" `
        + `without the required lock of Attribute "lock-req-coordination"`,
    );
    assert.equal(await client.invokeRPC(rpcFlow.countReplies, publishId), 1, "the rejected publish is discarded");
    assert.equal(await isSet(client, publishId, { attribute: lockReqFree }), false,
      "the other buffered write is discarded");

    const unlockedId = flowId("lock-req-write-unlocked");
    await client.startFlow(rpcFlow, unlockedId, undefined);
    await rejectsWithMissingLock(
      client.invokeRPC(rpcFlow.setItem, unlockedId, lockReqSpacedInstance),
      `wrote AttributeMap "lock-req-items" instance "item a"`,
    );
    assert.equal(
      await isSet(client, unlockedId, { attributeMap: lockReqItems, instance: lockReqSpacedInstance }),
      false,
    );

    const callTimeId = flowId("lock-req-write-call-time-lock");
    await client.startFlow(rpcFlow, callTimeId, undefined);
    await rejectsWithMissingLock(
      client.invokeRPCWithOptions(rpcFlow.setItem, callTimeId, lockReqSpacedInstance, {
        lockAttributeMapInstances: [lockReqItems.lock(lockReqSpacedInstance)],
      }),
      `wrote AttributeMap "lock-req-items" instance "item a"`,
    );
    assert.equal(
      await isSet(client, callTimeId, { attributeMap: lockReqItems, instance: lockReqSpacedInstance }),
      false,
      "a call-time lock does not satisfy the declared requirement",
    );

    const registeredId = flowId("lock-req-write-registered-lock");
    await client.startFlow(rpcFlow, registeredId, undefined);
    await client.invokeRPC(rpcFlow.setItemLocked, registeredId, lockReqSpacedInstance);
    assert.equal(
      await isSet(client, registeredId, { attributeMap: lockReqItems, instance: lockReqSpacedInstance }),
      true,
    );

    const eventsId = flowId("lock-req-channel-map-publish");
    await client.startFlow(rpcFlow, eventsId, undefined);
    await rejectsWithMissingLock(
      client.invokeRPC(rpcFlow.publishEventUnlocked, eventsId, lockReqSpacedInstance),
      `published to ChannelMap "lock-req-events" instance "item a" `
        + `without the required lock of Attribute "lock-req-coordination"`,
    );
    assert.equal(await client.invokeRPC(rpcFlow.countEvents, eventsId, lockReqSpacedInstance), 0,
      "the rejected publish is discarded");
    assert.equal(await isSet(client, eventsId, { attribute: lockReqFree }), false,
      "the other buffered write is discarded");
    await client.invokeRPC(rpcFlow.publishEventLocked, eventsId, lockReqSpacedInstance);
    assert.equal(await client.invokeRPC(rpcFlow.countEvents, eventsId, lockReqSpacedInstance), 1,
      "the locked publish commits");

    const accessesId = flowId("lock-req-several-accesses");
    await client.startFlow(rpcFlow, accessesId, undefined, {
      attributes: [InitialAttribute.of(lockReqTotal, 3)],
    });
    const [initialTotal] = await client.getAttributes(accessesId, lockReqTotal);
    assert.equal(initialTotal, 3, "initial Attribute values are not checked");
    await rejectsWithMissingLock(
      client.invokeRPC(rpcFlow.publishAndWriteUnlocked, accessesId),
      `RPC "publishAndWriteUnlocked" wrote Attribute "lock-req-total" without the required lock of `
        + `Attribute "lock-req-total", published to Channel "lock-req-replies" without the required lock of `
        + `Attribute "lock-req-coordination"`,
    );
    assert.equal(await client.invokeRPC(rpcFlow.countReplies, accessesId), 0);
    const [unchangedTotal] = await client.getAttributes(accessesId, lockReqTotal);
    assert.equal(unchangedTotal, 3);

    const deleteId = flowId("lock-req-delete-unlocked");
    await client.startFlow(rpcFlow, deleteId, undefined);
    await client.invokeRPC(rpcFlow.setTotalLocked, deleteId, 7);
    await rejectsWithMissingLock(
      client.invokeRPC(rpcFlow.deleteTotalUnlocked, deleteId),
      `RPC "deleteTotalUnlocked" wrote Attribute "lock-req-total" `
        + `without the required lock of Attribute "lock-req-total"`,
    );
    const [total] = await client.getAttributes(deleteId, lockReqTotal);
    assert.equal(total, 7, "the rejected delete is discarded");
  });
});

/** Waits for the holding Step to start, failing with a message instead of hanging. */
async function holdingStepStarted(): Promise<void> {
  let timer: NodeJS.Timeout | undefined;
  const timeout = new Promise<never>((_, reject) => {
    timer = setTimeout(() => reject(new Error("the holding Step did not start")), 30_000);
  });
  try {
    await Promise.race([lockSnapshotGate.entered.promise, timeout]);
  } finally {
    clearTimeout(timer);
  }
}

test("Dex acquires the registered locks that the Worker checks", async () => {
  const startFlow = new LockSnapshotStartFlow();
  const movementFlow = new LockSnapshotMovementFlow();
  await withEnvironment([startFlow, movementFlow], async ({ client }) => {
    assert.equal(startFlow.hold.getStepOptionsCalls, 1, "Registry reads the Step options once");
    // Emptying the registered array must not drop the lock the Client sends.
    lockSnapshotRPCLocks.pop();

    resetLockSnapshotGate();
    const startId = flowId("lock-snapshot-start");
    await client.startFlow(startFlow, startId, undefined);
    try {
      await holdingStepStarted();
      // The Step holds the total lock, so an RPC that sends the same lock conflicts.
      await expectError(client.invokeRPC(startFlow.setTotalLocked, startId, 7), RpcLockConflictError);
      await expectError(client.invokeRPC(startFlow.setTotalWithRegisteredArray, startId, 7), RpcLockConflictError);
    } finally {
      lockSnapshotGate.release.resolve();
    }
    assert.equal((await client.waitForFlow(startId, 30_000)).singleOutput(stringCodec), "written");
    assert.deepEqual(await client.getAttributes(startId, lockReqTotal), [1]);

    resetLockSnapshotGate();
    const movementId = flowId("lock-snapshot-movement");
    await client.startFlow(movementFlow, movementId, undefined);
    try {
      await holdingStepStarted();
      await expectError(client.invokeRPC(movementFlow.setMovedTotalLocked, movementId, 7), RpcLockConflictError);
    } finally {
      lockSnapshotGate.release.resolve();
    }
    assert.equal((await client.waitForFlow(movementId, 30_000)).singleOutput(stringCodec), "written");
    assert.deepEqual(await client.getAttributes(movementId, lockReqTotal), [1]);
  });
});

test("registration rejects an invalid lock Attribute", () => {
  const missingLock = new Attribute("lock-req-missing", booleanCodec);
  // An AttributeMap registered under the lock Attribute's name.
  const mapLock = new Attribute("lock-req-map-lock", booleanCodec);
  const mapLockCollision = new AttributeMap("lock-req-map-lock", booleanCodec);
  // A copy with the registered lock Attribute's name.
  const registeredLock = new Attribute("lock-req-registered", booleanCodec);
  const lockCopy = new Attribute("lock-req-registered", booleanCodec);
  const emptyLock = Object.assign(Object.create(Attribute.prototype) as Attribute<unknown>, { name: "" });
  const cases: readonly { readonly name: string; readonly schema: PersistenceSchema; readonly want: string }[] = [
    {
      name: "attribute-missing-lock",
      schema: { attributes: [new Attribute("lock-req-a", doubleCodec).writesRequireAttributeLock(missingLock)] },
      want: "Attribute lock-req-a write lock: lock Attribute lock-req-missing is not in the PersistenceSchema",
    },
    {
      name: "channel-missing-lock",
      schema: { channels: [new ChannelMap("lock-req-c", stringCodec).publishesRequireAttributeLock(missingLock)] },
      want: "ChannelMap lock-req-c publish lock: lock Attribute lock-req-missing is not in the PersistenceSchema",
    },
    {
      name: "attribute-map-lock",
      schema: {
        attributes: [mapLockCollision],
        channels: [new Channel("lock-req-c", stringCodec).publishesRequireAttributeLock(mapLock)],
      },
      want: "lock Attribute lock-req-map-lock is an AttributeMap; use a singleton Attribute",
    },
    {
      name: "attribute-copy-lock",
      schema: {
        attributes: [registeredLock, new AttributeMap("lock-req-a", doubleCodec).writesRequireAttributeLock(lockCopy)],
      },
      want: "AttributeMap lock-req-a write lock: lock Attribute lock-req-registered differs from the definition "
        + "in the PersistenceSchema",
    },
    {
      name: "attribute-empty-lock",
      schema: { attributes: [new AttributeMap("lock-req-a", doubleCodec).writesRequireAttributeLock(emptyLock)] },
      want: "AttributeMap lock-req-a write lock: lock Attribute has an empty name",
    },
    {
      name: "channel-empty-lock",
      schema: { channels: [new Channel("lock-req-c", stringCodec).publishesRequireAttributeLock(emptyLock)] },
      want: "Channel lock-req-c publish lock: lock Attribute has an empty name",
    },
  ];
  for (const testCase of cases) {
    assert.throws(
      () => new Registry([new AttributeLockRequirementInvalidFlow(testCase.schema)]),
      (failure: unknown) => {
        assert.ok(failure instanceof FlowDefinitionError, testCase.name);
        assert.ok(failure.message.includes(testCase.want), `${testCase.name}: ${failure.message}`);
        return true;
      },
    );
  }

  const notAnAttribute = new AttributeMap("lock-req-map", booleanCodec) as unknown as Attribute<unknown>;
  assert.throws(() => new Attribute("lock-req-a", doubleCodec).writesRequireAttributeLock(notAnAttribute), TypeError);
  assert.throws(() => new AttributeMap("lock-req-a", doubleCodec).writesRequireAttributeLock(notAnAttribute), TypeError);
  assert.throws(() => new Channel("lock-req-c", stringCodec).publishesRequireAttributeLock(notAnAttribute), TypeError);
  assert.throws(() => new ChannelMap("lock-req-c", stringCodec).publishesRequireAttributeLock(notAnAttribute), TypeError);
});
