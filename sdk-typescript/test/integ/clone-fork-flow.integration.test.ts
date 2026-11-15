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
import { setTimeout as delay } from "node:timers/promises";
import { credentials } from "@grpc/grpc-js";

import {
  InitialAttribute,
  SubFlowResetPolicy,
  TimeTravelType,
  stringCodec,
} from "../../src/index.js";
import {
  FlowServiceClient,
  GetHistoryEventsRequest,
  type GetHistoryEventsResponse,
} from "../../src/gen/dex.js";
import { CloneForkSessionFlow } from "./clone_fork_flow.js";
import { flowId, withEnvironment } from "./environment.js";

test("cloneForkFlow copies a running source at the current and an event ID point", async () => {
  const flow = new CloneForkSessionFlow();
  await withEnvironment([flow], async ({ client }) => {
    const sourceId = flowId("clone-fork-source");
    const sourceRunId = await client.startFlow(flow, sourceId, "source");
    const waitEventId = await awaitWaitForCompletedEventId(sourceId, sourceRunId);

    const currentCloneId = flowId("clone-fork-current");
    const currentClone = await client.cloneForkFlow(flow, sourceId, currentCloneId, {
      attributes: [InitialAttribute.of(flow.label, "current")],
      reason: "branch at the current point",
    });
    assert.notEqual(currentClone.runId, "");
    assert.equal(currentClone.sourceRunId, sourceRunId);
    assert.equal(currentClone.sourceRunAttempt, 1);
    assert.ok(currentClone.sourceEventId >= waitEventId);

    const eventCloneId = flowId("clone-fork-event");
    const eventClone = await client.cloneForkFlow(flow, sourceId, eventCloneId, {
      sourceRunId,
      type: TimeTravelType.EVENT_ID,
      eventId: waitEventId,
    });
    assert.equal(eventClone.sourceRunId, sourceRunId);
    assert.equal(eventClone.sourceEventId, waitEventId);

    await client.invokeRPC(flow.releaseSession, currentCloneId, "a");
    await client.invokeRPC(flow.releaseSession, eventCloneId, "b");
    assert.equal(await client.waitForFlow(currentCloneId, 30_000).then((result) => result.singleOutput(stringCodec)), "current|a");
    assert.equal(await client.waitForFlow(eventCloneId, 30_000).then((result) => result.singleOutput(stringCodec)), "source|b");

    const source = await client.describeFlow(sourceId);
    assert.equal(source.status, "running");
    assert.equal(source.runId, sourceRunId);
    await client.stopFlow(sourceId, { type: "terminate" });
  });
});

test("timeTravel by event ID returns to the waiting point", async () => {
  const flow = new CloneForkSessionFlow();
  await withEnvironment([flow], async ({ client }) => {
    const id = flowId("time-travel-event-id");
    const runId = await client.startFlow(flow, id, "first");
    const waitEventId = await awaitWaitForCompletedEventId(id, runId);
    await client.invokeRPC(flow.releaseSession, id, "a");
    await client.waitForFlow(id, 30_000);

    await client.timeTravel(id, {
      type: TimeTravelType.EVENT_ID,
      eventId: waitEventId,
      skipWritesReapply: true,
      subFlowResetPolicy: SubFlowResetPolicy.ATTACH_ELSE_REJECT,
      reason: "answer again",
    });
    await client.invokeRPC(flow.releaseSession, id, "b");
    assert.equal(await client.waitForFlow(id, 30_000).then((result) => result.singleOutput(stringCodec)), "first|b");
  });
});

async function awaitWaitForCompletedEventId(flowId: string, runId: string): Promise<number> {
  const service = new FlowServiceClient(
    process.env.DEX_SERVER_ADDRESS ?? "127.0.0.1:8801",
    credentials.createInsecure(),
  );
  try {
    for (let attempt = 0; attempt < 1_500; attempt += 1) {
      const response = await new Promise<GetHistoryEventsResponse>((resolve, reject) => {
        service.getHistoryEvents(
          GetHistoryEventsRequest.create({ flowId, runId, estimatePageSize: 100 }),
          (error, value) => (error === null ? resolve(value) : reject(error)),
        );
      });
      const event = response.events.find((candidate) => candidate.payload?.$case === "stepWaitForCompleted");
      if (event !== undefined) {
        return Number(event.eventId);
      }
      await delay(20);
    }
    throw new Error(`Flow ${flowId} recorded no StepWaitForCompleted event`);
  } finally {
    service.close();
  }
}
