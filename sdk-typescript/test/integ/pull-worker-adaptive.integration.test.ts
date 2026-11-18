// Portions of this file are derived from indeedeng/iwf-java-sdk.
// Those portions are licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
//
// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications are licensed under the Sustainable Use License 1.0.
// Third-Party Materials remain under the Apache License, Version 2.0.
// See LICENSE and LEGACY_NOTICES.md.

// Real-server tests of adaptive pull Worker slot streams; each test runs its own dex-server-zd.
import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import { availableParallelism } from "node:os";
import test from "node:test";

import { status } from "@grpc/grpc-js";

import { PullRegistrationError, type Client, type PullWorkerOptions } from "../../src/index.js";
import type { PullWorker } from "../../src/pull-worker.js";
import {
  ADAPTIVE_SLOT_MODE_BLOCK,
  ADAPTIVE_SLOT_MODE_QUICK,
  ADAPTIVE_SLOT_MODE_STUBBORN,
  AdaptiveSlotHandlers,
  AdaptiveSlotTestServer,
  PullSlotCompetitor,
  PullSlotProxy,
  RPC_ONLY,
  RegisterSendBarrier,
  STEP_AND_RPC,
  STUBBORN_RPC_KEY,
  SlotUsageSampler,
  TestScope,
  adaptiveSlotWindowsToFloor,
  awaitSettledWithin,
  captureConsole,
  countRegisterBatches,
  deferred,
  delay,
  eventually,
  hasMessage,
  never,
  requireFlowsCompleted,
  requireNoPermanentRegistrationError,
  requireRegisterBatchesPerWindow,
  requireRegisteredStreams,
  requireSlotState,
  startAdaptiveSlotFlows,
  startAdaptiveSlotFlowsConcurrently,
  startAdaptiveSlotTrickle,
  startAdaptiveSlotWorker,
  startParkedFlow,
  stepRegisters,
  type AdaptiveSlotWorkerRun,
} from "./pull-slot-harness.js";

const HOUR_MS = 3_600_000;

// Design Tests 2, 3, 4, 5, 9, and 11.
test("adaptive Worker grows and shrinks with a real server", { timeout: 300_000 }, async (t) => {
  const scope = new TestScope(t);
  const server = await AdaptiveSlotTestServer.start(scope);
  const proxy = await PullSlotProxy.start(scope, server.address);
  const handlers = new AdaptiveSlotHandlers();
  const appWorkerID = workerID("adaptive-grow");
  const run = startAdaptiveSlotWorker(scope, proxy.address, handlers, { maxConcurrentTasks: 16, minOpenSlots: 3, appWorkerID }, { shrinkWindowMs: 1_000 });
  await run.worker.waitPullRegistered({ timeoutMs: 60_000 });

  // Test 2: an idle adaptive Worker holds exactly L + R streams.
  await requireRegisteredStreams(proxy, appWorkerID, 2, 1);
  let state = run.state();
  assert.equal(state.isAdaptive, true);
  assert.equal(state.open, 2);
  assert.equal(state.rpcOnlyOpen, 1);
  assert.equal(state.floor, 2);
  assert.equal(state.effectiveCeiling, 15);
  assert.equal(state.totalCapacity, 16);
  assert.equal(state.rpcOnlySlots, 1);

  // Test 3: a burst of N long Steps with L < N <= E grows to at least N and at most min(E, 2N).
  const client = server.newClient(run.registry);
  const flowIds = await startAdaptiveSlotFlows(client, run.flow, ADAPTIVE_SLOT_MODE_BLOCK, 5);
  const entries = await handlers.awaitEntries(5);
  await requireSlotState(run, "five busy Steps", (current) => current.busy === 5 && current.registering === 0);
  state = run.state();
  assert.ok(state.open >= 5 && state.open <= 10, JSON.stringify(state));

  // Test 9: growth up to E keeps the RPC-only stream at R, and an RPC runs while every Step slot is busy.
  flowIds.push(...await startAdaptiveSlotFlows(client, run.flow, ADAPTIVE_SLOT_MODE_BLOCK, 10));
  entries.push(...await handlers.awaitEntries(10));
  await requireSlotState(run, "fifteen busy Steps", (current) => current.busy === 15 && current.open === 15 && current.registering === 0);
  await requireRegisteredStreams(proxy, appWorkerID, 15, 1);
  assert.equal(run.state().rpcOnlyOpen, 1);
  assert.equal(await client.invokeRPC(run.flow.getSlotStatus, flowIds[0]!), "ok");
  handlers.release();
  await requireFlowsCompleted(client, flowIds);

  // Tests 4 and 5: streams return to the floor while short Steps keep arriving, and no Step spends a retry.
  const stopTrickle = deferred();
  const trickle = startAdaptiveSlotTrickle(client, run.flow, stopTrickle.promise);
  await eventually(() => run.state().open === 2, (adaptiveSlotWindowsToFloor(15, 2) + 5) * 1_000,
    () => `streams did not return to the floor: ${JSON.stringify(run.state())}`);
  stopTrickle.resolve();
  const shortFlowIds = await trickle;
  assert.ok(shortFlowIds.length > 0);
  await requireFlowsCompleted(client, shortFlowIds);
  entries.push(...handlers.drainEntries());
  for (const entry of entries) assert.equal(entry.attempt, 1, `a Step spent a retry: ${JSON.stringify(entry)}`);
  state = run.state();
  assert.ok(state.streamsGrown >= 13, JSON.stringify(state));
  assert.ok(state.streamsShrunk >= 13, JSON.stringify(state));
  t.diagnostic(`shrink race drops: ${state.shrinkRaceDrops}`);

  // Test 11 and invariant 5: the count stays at the floor, and no stream closed on purpose
  // reopens, for longer than two maximum reconnect backoffs (2 x 7.5 s).
  await requireRegisteredStreams(proxy, appWorkerID, 2, 1);
  const sinceMs = Date.now();
  await never(() => run.state().open !== 2 || proxy.registersSince(appWorkerID, sinceMs).length !== 0, 16_000,
    "streams rose above the floor at rest, or a stream closed on purpose registered again");
});

// The RPC variant of design Test 3, where S + R serve the burst.
test("adaptive Worker serves an RPC burst with Step-and-RPC and RPC-only streams", { timeout: 120_000 }, async (t) => {
  const scope = new TestScope(t);
  const server = await AdaptiveSlotTestServer.start(scope);
  const handlers = new AdaptiveSlotHandlers();
  const run = startAdaptiveSlotWorker(scope, server.address, handlers, { maxConcurrentTasks: 8, minOpenSlots: 2, appWorkerID: workerID("adaptive-rpc") }, { shrinkWindowMs: HOUR_MS });
  await run.worker.waitPullRegistered({ timeoutMs: 60_000 });
  const client = server.newClient(run.registry);
  const parkedId = await startParkedFlow(client, run.parked);
  const rpcCount = 6;
  const rpcResults = Array.from({ length: rpcCount }, () => client.invokeRPC(run.parked.blockUntilReleased, parkedId));
  await handlers.awaitEntries(rpcCount);
  // E = 7 and R = 1: S + R >= min(N, E + R) and S <= min(E, 2N).
  const state = run.state();
  assert.ok(state.open + state.rpcOnlyOpen >= rpcCount, JSON.stringify(state));
  assert.ok(state.open <= 7, JSON.stringify(state));
  assert.equal(state.rpcOnlyOpen, 1);
  handlers.release();
  for (const result of rpcResults) assert.equal(await result, "released");
});

// Design Tests 6 and 18.
test("adaptive Worker treats the node stream limit as a soft growth limit", { timeout: 180_000 }, async (t) => {
  const scope = new TestScope(t);
  const server = await AdaptiveSlotTestServer.start(scope, { maxConcurrentTasksCap: 20, maxStreams: 20 });
  const proxy = await PullSlotProxy.start(scope, server.address);
  const fixed = startAdaptiveSlotWorker(scope, server.address, new AdaptiveSlotHandlers(), { maxConcurrentTasks: 14, appWorkerID: workerID("fixed-filler") },
    { shrinkWindowMs: HOUR_MS, isParkedOnly: true });
  await fixed.worker.waitPullRegistered({ timeoutMs: 60_000 });
  const handlers = new AdaptiveSlotHandlers();
  const appWorkerID = workerID("adaptive-limit");
  const windowMs = 2_000;
  const run = startAdaptiveSlotWorker(scope, proxy.address, handlers, { maxConcurrentTasks: 12, minOpenSlots: 3, appWorkerID }, { shrinkWindowMs: windowMs });
  await run.worker.waitPullRegistered({ timeoutMs: 60_000 });
  await requireRegisteredStreams(proxy, appWorkerID, 2, 1);
  const client = server.newClient(run.registry);
  const flowIds = await startAdaptiveSlotFlows(client, run.flow, ADAPTIVE_SLOT_MODE_BLOCK, 10);

  // 14 + 3 streams leave room for 3: growth reaches S = 5 and a partly rejected batch clamps G to S + H.
  await requireSlotState(run, "growth clamped at the node limit", (state) =>
    state.open === 5 && state.busy === 5 && state.registering === 0 && state.growthTarget === 5 && state.growRejectedAtCap > 0);
  const state = run.state();
  assert.equal(state.effectiveCeiling, 11);
  assert.equal(state.growthTarget, state.open + state.orphanedHandlers);
  requireNoPermanentRegistrationError(run);

  // At most one growth batch per window while rejections continue: a batch is min(E - S, S) = 5.
  const sinceMs = Date.now();
  await eventually(() => countRegisterBatches(proxy.registersSince(appWorkerID, sinceMs), windowMs) >= 3, 6 * windowMs,
    () => "the Worker did not retry growth once per window");
  requireRegisterBatchesPerWindow(proxy.registersSince(appWorkerID, sinceMs), windowMs, 5);
  requireNoPermanentRegistrationError(run);

  // Room returns: growth resumes after the next window, and G returns to E.
  await fixed.worker.close();
  await requireSlotState(run, "growth after the node limit cleared", (current) =>
    current.busy === 10 && current.registering === 0 && current.growthTarget === current.effectiveCeiling);
  handlers.release();
  await requireFlowsCompleted(client, flowIds);
  for (const entry of handlers.drainEntries()) assert.equal(entry.attempt, 1);
});

// Design Test 19.
test("adaptive Worker retries a soft-rejected floor stream at most once per window", { timeout: 180_000 }, async (t) => {
  const scope = new TestScope(t);
  const server = await AdaptiveSlotTestServer.start(scope, { maxConcurrentTasksCap: 20, maxStreams: 20 });
  const proxy = await PullSlotProxy.start(scope, server.address);
  const appWorkerID = workerID("adaptive-latch");
  const windowMs = 1_000;
  const run = startAdaptiveSlotWorker(scope, proxy.address, new AdaptiveSlotHandlers(),
    { maxConcurrentTasks: 12, reservedRpcTasks: 1, minOpenSlots: 3, appWorkerID }, { shrinkWindowMs: windowMs });
  await run.worker.waitPullRegistered({ timeoutMs: 60_000 });
  await requireRegisteredStreams(proxy, appWorkerID, 2, 1);
  const competitor = PullSlotCompetitor.start(scope, server.address, workerID("competitor"), 18);
  await eventually(() => competitor.heldStreams() === 17, 20_000, () => `the competitor holds ${competitor.heldStreams()} streams`);

  // Resetting a floor stream frees a node slot; the competitor takes it, so the reconnect is rejected softly.
  const sinceMs = Date.now();
  assert.equal(proxy.resetRegisteredStream(appWorkerID, STEP_AND_RPC), true);
  await requireSlotState(run, "the reset stream ended", (state) => state.open - state.registering === 1, 5_000);
  // One reconnect after the reset, then one retry per window: wait for three window retries.
  let violation = "";
  await eventually(() => {
    const state = run.state();
    if (state.open - state.registering > 1 || !run.worker.pullRegistrationState().isReady) {
      violation = `a stream registered at the node limit, or readiness dropped: ${JSON.stringify(state)}`;
      return true;
    }
    return stepRegisters(proxy.registersSince(appWorkerID, sinceMs)).length >= 4;
  }, 6 * windowMs, () => "the Worker did not retry at each window");
  assert.equal(violation, "");
  const registers = stepRegisters(proxy.registersSince(appWorkerID, sinceMs));
  for (let index = 1; index + 1 < registers.length; index++) {
    assert.ok(registers[index + 1]!.atMs - registers[index]!.atMs >= windowMs * 0.8, "a Register followed a rejected stream's end at once");
  }
  assert.equal(proxy.registeredStreams(appWorkerID, STEP_AND_RPC), 1);
  assert.ok(run.state().growRejectedAtCap >= 2, JSON.stringify(run.state()));
  requireNoPermanentRegistrationError(run);

  // A competitor stream that ends does not clear this Worker's latch.
  const beforeClose = stepRegisters(proxy.registersSince(appWorkerID, sinceMs));
  const lastRegisterBeforeClose = beforeClose[beforeClose.length - 1]!;
  const competitorClosedAtMs = Date.now();
  competitor.closeOneStream();
  await eventually(() => stepRegisters(proxy.registersSince(appWorkerID, competitorClosedAtMs)).length >= 1, 3 * windowMs,
    () => "the Worker stopped retrying at the window");
  const nextRegister = stepRegisters(proxy.registersSince(appWorkerID, competitorClosedAtMs))[0]!;
  assert.ok(nextRegister.atMs - lastRegisterBeforeClose.atMs >= windowMs * 0.8, "the competitor's close triggered a Register");

  // When the competitor leaves, the floor comes back within one window.
  await competitor.stop();
  await eventually(() => {
    const state = run.state();
    return proxy.registeredStreams(appWorkerID, STEP_AND_RPC) === 2 && state.open === 2 && state.registering === 0;
  }, 2 * windowMs + 3_000, () => `the floor did not come back: ${JSON.stringify(run.state())}`);
  assert.equal(run.worker.pullRegistrationState().isReady, true);
});

// Design Test 14.
test("a Server cap of one probes, clamps, or fails start as configured", { timeout: 300_000 }, async (t) => {
  const cpuCount = availableParallelism();
  await t.test("fixed with an unset ceiling probes and clamps", async (subtest) => {
    const scope = new TestScope(subtest);
    const warnings = captureConsole(subtest, "warn");
    const server = await AdaptiveSlotTestServer.start(scope, { maxConcurrentTasksCap: 1 });
    const proxy = await PullSlotProxy.start(scope, server.address);
    const releaseAcknowledgements = proxy.holdAcknowledgements();
    const appWorkerID = workerID("fixed-probe");
    const run = startAdaptiveSlotWorker(scope, proxy.address, new AdaptiveSlotHandlers(), { appWorkerID }, { shrinkWindowMs: HOUR_MS });
    // F4: only the probe stream registers before the first acknowledgement.
    await eventually(() => proxy.registersSince(appWorkerID, 0).length === 1, 10_000, () => "the probe stream did not register");
    await never(() => proxy.registersSince(appWorkerID, 0).length > 1, 300, "a stream registered before the probe's acknowledgement");
    releaseAcknowledgements();
    await requireCapOfOneWorkerServes(server, proxy, run, appWorkerID);
    if (cpuCount > 1) assert.ok(hasMessage(warnings, "clamped maxConcurrentTasks", `${cpuCount}`, "1"), warnings.join("\n"));
    assert.equal(run.state().isAdaptive, false);
  });
  for (const configuredCeiling of [4, 0]) {
    await t.test(`adaptive floor of one with ceiling ${configuredCeiling}`, async (subtest) => {
      const maxConcurrentTasks = configuredCeiling === 0 && cpuCount < 2 ? 2 : configuredCeiling;
      const scope = new TestScope(subtest);
      const warnings = captureConsole(subtest, "warn");
      const server = await AdaptiveSlotTestServer.start(scope, { maxConcurrentTasksCap: 1 });
      const proxy = await PullSlotProxy.start(scope, server.address);
      const appWorkerID = workerID("adaptive-one");
      const run = startAdaptiveSlotWorker(scope, proxy.address, new AdaptiveSlotHandlers(), { maxConcurrentTasks, minOpenSlots: 1, appWorkerID }, { shrinkWindowMs: HOUR_MS });
      await requireCapOfOneWorkerServes(server, proxy, run, appWorkerID);
      assert.equal(run.state().isAdaptive, true);
      assert.ok(hasMessage(warnings, "clamped maxConcurrentTasks", "1"), warnings.join("\n"));
      for (const register of proxy.registersSince(appWorkerID, 0)) assert.equal(register.slotKind, STEP_AND_RPC);
    });
  }
  const optionsByMode: Record<string, PullWorkerOptions> = {
    fixed: cpuCount < 2 ? { maxConcurrentTasks: 2, minOpenSlots: 2, reservedRpcTasks: 1 } : { reservedRpcTasks: 1 },
    adaptive: { maxConcurrentTasks: 4, minOpenSlots: 2, reservedRpcTasks: 1 },
  };
  for (const [mode, options] of Object.entries(optionsByMode)) {
    await t.test(`explicit reserved RPC slot fails start, ${mode}`, async (subtest) => {
      const scope = new TestScope(subtest);
      captureConsole(subtest, "warn");
      const server = await AdaptiveSlotTestServer.start(scope, { maxConcurrentTasksCap: 1 });
      const run = startAdaptiveSlotWorker(scope, server.address, new AdaptiveSlotHandlers(), { ...options, appWorkerID: workerID("reserved-one") }, { shrinkWindowMs: HOUR_MS });
      const message = String(await run.awaitRunResult(30_000));
      if (mode === "fixed" && options.maxConcurrentTasks === 2) {
        assert.match(message, /maxConcurrentTasks 2 exceeds Server matching.maxConcurrentTasksCap 1/);
        return;
      }
      assert.match(message, /reservedRpcTasks 1 must be less than the slot ceiling 1/);
      assert.match(message, /matching.maxConcurrentTasksCap 1/);
    });
  }
  await t.test("fixed with an explicit ceiling above the cap fails start", async (subtest) => {
    const scope = new TestScope(subtest);
    const server = await AdaptiveSlotTestServer.start(scope, { maxConcurrentTasksCap: 1 });
    const run = startAdaptiveSlotWorker(scope, server.address, new AdaptiveSlotHandlers(), { maxConcurrentTasks: 2, appWorkerID: workerID("fixed-two") }, { shrinkWindowMs: HOUR_MS });
    assert.match(String(await run.awaitRunResult(30_000)), /maxConcurrentTasks 2 exceeds Server matching.maxConcurrentTasksCap 1/);
  });
});

// Design Tests 20a and 20b.
test("a ceiling above the Server cap clamps in adaptive mode and fails in fixed mode", { timeout: 300_000 }, async (t) => {
  await t.test("adaptive clamps", async (subtest) => {
    const scope = new TestScope(subtest);
    const warnings = captureConsole(subtest, "warn");
    const server = await AdaptiveSlotTestServer.start(scope);
    const handlers = new AdaptiveSlotHandlers();
    const run = startAdaptiveSlotWorker(scope, server.address, handlers, { maxConcurrentTasks: 300, minOpenSlots: 8, appWorkerID: workerID("adaptive-300") }, { shrinkWindowMs: HOUR_MS });
    await run.worker.waitPullRegistered({ timeoutMs: 120_000 });
    assert.ok(hasMessage(warnings, "clamped maxConcurrentTasks", "300", "256"), warnings.join("\n"));
    assert.equal(warnings.filter((warning) => warning.includes("clamped maxConcurrentTasks")).length, 1);
    assert.equal(run.state().totalCapacity, 256);
    const sampler = new SlotUsageSampler(scope, run);
    const client = server.newClient(run.registry);
    const flowIds = await startAdaptiveSlotFlowsConcurrently(client, run.flow, ADAPTIVE_SLOT_MODE_BLOCK, 400);
    await requireSlotState(run, "every slot busy at the clamped ceiling", (state) => state.busy === 255 && state.open + state.rpcOnlyOpen === 256, 90_000);
    assert.equal(run.state().growRejectedAtCap, 0);
    handlers.release();
    await requireFlowsCompleted(client, flowIds);
    sampler.requireWithinCapacity();
    for (const entry of handlers.drainEntries()) assert.equal(entry.attempt, 1);
  });
  await t.test("fixed fails start", async (subtest) => {
    const scope = new TestScope(subtest);
    const server = await AdaptiveSlotTestServer.start(scope);
    const run = startAdaptiveSlotWorker(scope, server.address, new AdaptiveSlotHandlers(), { maxConcurrentTasks: 300, appWorkerID: workerID("fixed-300") }, { shrinkWindowMs: HOUR_MS });
    assert.match(String(await run.awaitRunResult(30_000)), /maxConcurrentTasks 300 exceeds Server matching.maxConcurrentTasksCap 256/);
  });
});

// Design Test 17a.
test("adaptive Worker converges to one stream after the cap drops to one", { timeout: 240_000 }, async (t) => {
  const scope = new TestScope(t);
  const server = await AdaptiveSlotTestServer.start(scope, { maxConcurrentTasksCap: 4 });
  const proxy = await PullSlotProxy.start(scope, server.address);
  const handlers = new AdaptiveSlotHandlers();
  handlers.isRpcStubborn = true;
  const appWorkerID = workerID("adaptive-drop");
  const run = startAdaptiveSlotWorker(scope, proxy.address, handlers, {
    maxConcurrentTasks: 4, minOpenSlots: 2, appWorkerID, stopOnPermanentStreamRejection: true,
  }, { shrinkWindowMs: 1_000 });
  await run.worker.waitPullRegistered({ timeoutMs: 60_000 });
  const client = server.newClient(run.registry);
  const parkedId = await startParkedFlow(client, run.parked);
  const { flowIds, rpcResult } = await occupyEverySlot(run, client, handlers, parkedId);

  // The restart lowers the cap to 1; the proxy lets the RPC-only stream register first.
  await server.kill();
  proxy.holdStepRegistersUntilRpcOnlyAcknowledged();
  await server.restart({ maxConcurrentTasksCap: 1 });
  proxy.setUpstream(server.address);
  handlers.releaseStubborn(...flowIds, STUBBORN_RPC_KEY);
  await awaitSettledWithin(rpcResult, 30_000, "the RPC interrupted by the restart");

  await requireSlotState(run, "one Step-and-RPC stream at cap 1", (state) =>
    state.totalCapacity === 1 && state.rpcOnlySlots === 0 && state.rpcOnlyOpen === 0 && state.open === 1 && state.registering === 0
    && state.orphanedHandlers === 0 && state.orphanedRpcOnlyHandlers === 0);
  await eventually(() => proxy.registeredStreams(appWorkerID, STEP_AND_RPC) === 1 && proxy.registeredStreams(appWorkerID, RPC_ONLY) === 0, 10_000,
    () => proxy.describe(appWorkerID));
  assert.ok(run.state().growRejectedAtCap >= 1, "the Step-and-RPC Register was not rejected at the cap");
  requireNoPermanentRegistrationError(run);
  assert.equal(run.worker.pullRegistrationState().isReady, true);

  // Steps and RPCs keep running on the one stream.
  const restartedClient = server.newClient(run.registry);
  await requireFlowsCompleted(restartedClient, flowIds);
  await requireFlowsCompleted(restartedClient, await startAdaptiveSlotFlows(restartedClient, run.flow, ADAPTIVE_SLOT_MODE_QUICK, 2));
  assert.equal(await restartedClient.invokeRPC(run.parked.getSlotStatus, parkedId), "ok");
});

// Design Test 17b: fixed mode keeps today's behavior.
test("fixed Worker keeps the permanent rejection after the cap drops to one", { timeout: 240_000 }, async (t) => {
  for (const shouldStop of [true, false]) {
    await t.test(`stopOnPermanentStreamRejection=${shouldStop}`, async (subtest) => {
      const scope = new TestScope(subtest);
      captureConsole(subtest, "warn");
      const server = await AdaptiveSlotTestServer.start(scope, { maxConcurrentTasksCap: 4 });
      const proxy = await PullSlotProxy.start(scope, server.address);
      const appWorkerID = workerID("fixed-drop");
      const run = startAdaptiveSlotWorker(scope, proxy.address, new AdaptiveSlotHandlers(), {
        maxConcurrentTasks: 4, appWorkerID, stopOnPermanentStreamRejection: shouldStop,
      }, { shrinkWindowMs: 1_000 });
      await run.worker.waitPullRegistered({ timeoutMs: 60_000 });
      await requireRegisteredStreams(proxy, appWorkerID, 3, 1);
      await server.kill();
      proxy.holdStepRegistersUntilRpcOnlyAcknowledged();
      await server.restart({ maxConcurrentTasksCap: 1 });
      proxy.setUpstream(server.address);
      if (shouldStop) {
        const failure = await run.awaitRunResult(45_000);
        assert.ok(failure instanceof PullRegistrationError, String(failure));
        assert.equal(failure.queue, "STEP_AND_RPC");
        assert.equal((failure.cause as { code?: number }).code, status.RESOURCE_EXHAUSTED);
        return;
      }
      await eventually(() => {
        const queue = run.worker.pullRegistrationState().queues[0]!;
        return !queue.hasRegisteredStream && queue.lastPermanentError?.code === status.RESOURCE_EXHAUSTED;
      }, 45_000, () => JSON.stringify(run.worker.pullRegistrationState()));
      assert.equal(run.worker.pullRegistrationState().isReady, false);
      // Today's permanent-error backoff is one minute, so no Step-and-RPC stream registers again soon.
      const rejectedAtMs = Date.now();
      await never(() => stepRegisters(proxy.registersSince(appWorkerID, rejectedAtMs)).length !== 0, 5_000,
        "a Step-and-RPC stream retried before the permanent-error backoff");
      assert.equal(run.state().isAdaptive, false);
      assert.equal(run.state().growRejectedAtCap, 0);
    });
  }
});

// Design Tests 15 and 8.
test("adaptive Worker counts orphaned handlers across a Server restart", { timeout: 300_000 }, async (t) => {
  await t.test("same cap", async (subtest) => {
    const scenario = await startOrphanedHandlerScenario(subtest);
    const { server, proxy, run, handlers, appWorkerID, flowIds } = scenario;
    const killedAtMs = Date.now();
    await server.kill();
    await server.restart({ maxConcurrentTasksCap: 4 });
    proxy.setUpstream(server.address);
    // No early registration: every slot still has a running handler.
    await requireSlotState(run, "every handler orphaned", (state) =>
      state.open === 0 && state.rpcOnlyOpen === 0 && state.orphanedHandlers === 3 && state.orphanedRpcOnlyHandlers === 1, 10_000);
    await never(() => proxy.registersSince(appWorkerID, killedAtMs).length !== 0 || !run.worker.pullRegistrationState().isReady, 1_000,
      "a stream registered while orphaned handlers filled the capacity, or readiness dropped");

    // Freed capacity goes to one Step-and-RPC stream first, then to the RPC-only stream.
    handlers.releaseStubborn(flowIds[0]!);
    await eventually(() => proxy.registeredStreams(appWorkerID, STEP_AND_RPC) === 1, 10_000, () => proxy.describe(appWorkerID));
    handlers.releaseStubborn(STUBBORN_RPC_KEY);
    await eventually(() => proxy.registeredStreams(appWorkerID, RPC_ONLY) === 1, 10_000, () => proxy.describe(appWorkerID));
    const registers = proxy.registersSince(appWorkerID, killedAtMs);
    assert.equal(registers[0]!.slotKind, STEP_AND_RPC);
    assert.equal(registers[registers.length - 1]!.slotKind, RPC_ONLY);
    // Test 8: the Worker re-registers at most L + R streams first.
    assert.ok(registers.length <= 2, proxy.describe(appWorkerID));
    handlers.releaseStubborn(...flowIds.slice(1));
    await awaitSettledWithin(scenario.rpcResult, 30_000, "the RPC interrupted by the restart");
    await requireFlowsCompleted(server.newClient(run.registry), flowIds);
    assert.equal(run.worker.pullRegistrationState().isReady, true);
    // With the cap unchanged, S + H + Sr + Hr <= C held at every sample.
    scenario.sampler.requireWithinCapacity();
  });
  await t.test("cap lowered below the orphaned handlers", async (subtest) => {
    const scenario = await startOrphanedHandlerScenario(subtest);
    const { server, proxy, run, handlers, appWorkerID, flowIds } = scenario;
    await server.kill();
    await server.restart({ maxConcurrentTasksCap: 2 });
    proxy.setUpstream(server.address);
    await requireSlotState(run, "every handler orphaned", (state) => state.orphanedHandlers === 3 && state.orphanedRpcOnlyHandlers === 1, 10_000);
    const releasedAtMs = Date.now();
    handlers.releaseStubborn(flowIds[0]!);
    // The first acknowledgement reveals cap 2 while usage is 4: the Worker registers nothing more until usage drops below 2.
    await eventually(() => proxy.acknowledgementsSince(appWorkerID, releasedAtMs).length === 1, 10_000, () => proxy.describe(appWorkerID));
    const acknowledgedAtMs = proxy.acknowledgementsSince(appWorkerID, releasedAtMs)[0]!.atMs;
    await requireSlotState(run, "capacity lowered to 2", (state) => state.totalCapacity === 2 && state.open === 0 && state.registering === 0, 10_000);
    handlers.releaseStubborn(flowIds[1]!);
    await requireSlotState(run, "the second handler returned", (state) => state.orphanedHandlers === 1, 10_000);
    // Usage is still 2 of 2 with one Step and one RPC-only handler running.
    await never(() => proxy.registersSince(appWorkerID, acknowledgedAtMs + 1).length !== 0, 1_000, "a Register went out while usage reached the new cap");
    handlers.releaseStubborn(flowIds[2]!, STUBBORN_RPC_KEY);
    await requireSlotState(run, "usage converged to the new cap", (state) =>
      state.open === 1 && state.rpcOnlyOpen === 1 && state.registering === 0 && state.orphanedHandlers === 0 && state.orphanedRpcOnlyHandlers === 0, 20_000);
    const state = run.state();
    assert.ok(state.open + state.rpcOnlyOpen <= state.totalCapacity, JSON.stringify(state));
    await requireRegisteredStreams(proxy, appWorkerID, 1, 1);
    // F5 allows the transient excess, so this case asserts convergence instead of a bound at every sample.
    subtest.diagnostic(`samples above the new cap while old handlers ran: ${scenario.sampler.stopSampling().length}`);
    await awaitSettledWithin(scenario.rpcResult, 30_000, "the RPC interrupted by the restart");
    await requireFlowsCompleted(server.newClient(run.registry), flowIds);
  });
  await t.test("failover while a growth batch registers", async (subtest) => {
    const scope = new TestScope(subtest);
    const server = await AdaptiveSlotTestServer.start(scope, { maxConcurrentTasksCap: 8 });
    const proxy = await PullSlotProxy.start(scope, server.address);
    const handlers = new AdaptiveSlotHandlers();
    const appWorkerID = workerID("adaptive-growth-failover");
    const run = startAdaptiveSlotWorker(scope, proxy.address, handlers, { maxConcurrentTasks: 8, minOpenSlots: 3, appWorkerID }, { shrinkWindowMs: HOUR_MS });
    await run.worker.waitPullRegistered({ timeoutMs: 60_000 });
    await requireRegisteredStreams(proxy, appWorkerID, 2, 1);
    const releaseGrowth = proxy.holdStepRegisters();
    const client = server.newClient(run.registry);
    const flowIds = await startAdaptiveSlotFlows(client, run.flow, ADAPTIVE_SLOT_MODE_STUBBORN, 2);
    // Both floor streams are busy, so a growth batch of 2 waits at the proxy.
    await eventually(() => proxy.heldStepRegisters() === 2, 20_000, () => proxy.describe(appWorkerID));
    await requireSlotState(run, "a growth batch registering", (state) => state.busy === 2 && state.registering === 2);
    const sampler = new SlotUsageSampler(scope, run);
    const killedAtMs = Date.now();
    await server.kill();
    await server.restart({ maxConcurrentTasksCap: 8 });
    proxy.setUpstream(server.address);
    await requireSlotState(run, "the busy streams orphaned", (state) =>
      state.orphanedHandlers === 2 && state.registering === 2 && state.rpcOnlyOpen === 1, 10_000);
    await requireRegisteredStreams(proxy, appWorkerID, 0, 1);
    // The in-flight batch already holds the floor, so no other Step-and-RPC stream opens.
    assert.deepEqual(stepRegisters(proxy.registersSince(appWorkerID, killedAtMs)), []);
    const releasedAtMs = Date.now();
    releaseGrowth();
    // The held batch registers on the new server; retried Steps may then grow the Worker further.
    await eventually(() => stepRegisters(proxy.acknowledgementsSince(appWorkerID, releasedAtMs)).length >= 2
      && proxy.registeredStreams(appWorkerID, STEP_AND_RPC) >= 2, 20_000, () => "the growth batch did not register after the failover");
    handlers.releaseStubborn(...flowIds);
    await requireFlowsCompleted(server.newClient(run.registry), flowIds);
    await requireSlotState(run, "no orphaned handlers", (state) => state.orphanedHandlers === 0 && state.registering === 0);
    sampler.requireWithinCapacity();
    requireNoPermanentRegistrationError(run);
  });
  await t.test("failover while a shrink half-close is in flight", async (subtest) => {
    const scope = new TestScope(subtest);
    const server = await AdaptiveSlotTestServer.start(scope, { maxConcurrentTasksCap: 8 });
    const proxy = await PullSlotProxy.start(scope, server.address);
    const handlers = new AdaptiveSlotHandlers();
    const appWorkerID = workerID("adaptive-shrink-failover");
    const run = startAdaptiveSlotWorker(scope, proxy.address, handlers, { maxConcurrentTasks: 8, minOpenSlots: 3, appWorkerID }, { shrinkWindowMs: 1_000 });
    await run.worker.waitPullRegistered({ timeoutMs: 60_000 });
    const client = server.newClient(run.registry);
    const flowIds = await startAdaptiveSlotFlows(client, run.flow, ADAPTIVE_SLOT_MODE_BLOCK, 3);
    // One handler ignores cancellation, so it outlives its stream in the crash.
    const stubbornIds = await startAdaptiveSlotFlows(client, run.flow, ADAPTIVE_SLOT_MODE_STUBBORN, 1);
    await requireSlotState(run, "four busy Steps", (state) => state.busy === 4 && state.registering === 0);
    const halfCloses = proxy.holdHalfCloses();
    handlers.release();
    await requireFlowsCompleted(client, flowIds);
    await eventually(() => halfCloses.heldCount() > 0, 20_000, () => "the Worker never shrank");
    assert.equal(run.state().busy, 1);
    // The server still holds the half-closing stream when it crashes, and the stubborn handler still runs.
    const sampler = new SlotUsageSampler(scope, run);
    await server.kill();
    await requireSlotState(run, "the stubborn handler orphaned", (state) => state.orphanedHandlers === 1 && state.busy === 0, 10_000);
    await server.restart({ maxConcurrentTasksCap: 8 });
    // Registers that reached the dead server were retries of the same runners, so count from the switch.
    const switchedAtMs = Date.now();
    proxy.setUpstream(server.address);
    halfCloses.release();
    await requireRegisteredStreams(proxy, appWorkerID, 2, 1);
    const isShrunkStreamReopened = () => stepRegisters(proxy.registersSince(appWorkerID, switchedAtMs)).length > 2
      || proxy.registeredStreams(appWorkerID, STEP_AND_RPC) > 2;
    await never(isShrunkStreamReopened, 3_000, "a stream closed by shrink reopened after the failover");
    const state = run.state();
    assert.equal(state.orphanedHandlers, 1, `the orphaned handler left the capacity count early: ${JSON.stringify(state)}`);
    assert.equal(state.open, 2);
    // The returning handler frees a slot, but the floor is already open, so nothing reopens.
    handlers.releaseStubborn(...stubbornIds);
    await requireSlotState(run, "the orphaned handler returned", (current) =>
      current.orphanedHandlers === 0 && current.open === 2 && current.rpcOnlyOpen === 1 && current.registering === 0);
    await never(isShrunkStreamReopened, 2_000, "a stream reopened when the orphaned handler returned");
    await requireFlowsCompleted(server.newClient(run.registry), stubbornIds);
    sampler.requireWithinCapacity();
    requireNoPermanentRegistrationError(run);
  });
});

// Design Tests 1 and 16.
test("fixed Worker keeps parallel startup and a constant stream count", { timeout: 120_000 }, async (t) => {
  const scope = new TestScope(t);
  const server = await AdaptiveSlotTestServer.start(scope);
  const proxy = await PullSlotProxy.start(scope, server.address);
  const releaseAcknowledgements = proxy.holdAcknowledgements();
  const handlers = new AdaptiveSlotHandlers();
  const appWorkerID = workerID("fixed-parallel");
  const run = startAdaptiveSlotWorker(scope, proxy.address, handlers, { maxConcurrentTasks: 4, appWorkerID }, { shrinkWindowMs: 1_000 });
  // Test 16: every stream opens before the first acknowledgement arrives.
  await eventually(() => proxy.registersSince(appWorkerID, 0).length === 4, 10_000, () => proxy.describe(appWorkerID));
  releaseAcknowledgements();
  await run.worker.waitPullRegistered({ timeoutMs: 60_000 });
  for (const register of proxy.registersSince(appWorkerID, 0)) assert.ok(register.atMs <= proxy.firstAcknowledgementAt());
  // Test 1: the count stays at the ceiling through load and idle periods.
  await requireRegisteredStreams(proxy, appWorkerID, 3, 1);
  const client = server.newClient(run.registry);
  const flowIds = await startAdaptiveSlotFlows(client, run.flow, ADAPTIVE_SLOT_MODE_BLOCK, 3);
  await handlers.awaitEntries(3);
  assert.equal(run.state().open, 3);
  handlers.release();
  await requireFlowsCompleted(client, flowIds);
  const sinceMs = Date.now();
  await never(() => run.state().open !== 3 || proxy.registersSince(appWorkerID, sinceMs).length !== 0, 3_000,
    "a fixed Worker changed its stream count at rest");
  const state = run.state();
  assert.equal(state.isAdaptive, false);
  assert.equal(state.open, 3);
  assert.equal(state.rpcOnlyOpen, 1);
  assert.equal(state.streamsGrown, 0);
  assert.equal(state.streamsShrunk, 0);
  assert.deepEqual(proxy.registersSince(appWorkerID, sinceMs), []);
  await requireRegisteredStreams(proxy, appWorkerID, 3, 1);
});

// F5 for an attempt granted under the old cap: when another stream reports a lower cap during its
// backoff, it sends no Register above that cap.
test("adaptive Worker revokes a reconnect grant after the cap drops", { timeout: 120_000 }, async (t) => {
  const scope = new TestScope(t);
  captureConsole(t, "warn");
  const server = await AdaptiveSlotTestServer.start(scope, { maxConcurrentTasksCap: 8 });
  const proxy = await PullSlotProxy.start(scope, server.address);
  const appWorkerID = workerID("adaptive-revoke");
  const run = startAdaptiveSlotWorker(scope, proxy.address, new AdaptiveSlotHandlers(), {
    maxConcurrentTasks: 8, minOpenSlots: 3, reservedRpcTasks: 0, appWorkerID,
  }, { shrinkWindowMs: HOUR_MS });
  await run.worker.waitPullRegistered({ timeoutMs: 60_000 });
  await requireRegisteredStreams(proxy, appWorkerID, 3, 0);

  // Every floor stream breaks and gets a reconnect grant under the last known cap of 8.
  await server.kill();
  proxy.failStepRegistersFromNow();
  await server.restart({ maxConcurrentTasksCap: 2 });
  proxy.setUpstream(server.address);
  // One runner registers and learns cap 2 while the other two wait in their backoff.
  proxy.passNextStepRegister();
  await requireSlotState(run, "the lower cap is known", (state) => state.totalCapacity === 2, 30_000);
  proxy.stopFailingStepRegisters();
  const capKnownAtMs = Date.now();
  // One registered stream leaves room for exactly one more; the other old grant must be revoked.
  await never(() => stepRegisters(proxy.registersSince(appWorkerID, capKnownAtMs)).length > 1
    || proxy.rejectionsSince(appWorkerID, capKnownAtMs).length !== 0, 8_000, "a Register granted under the old cap went out above the new cap");
  await requireSlotState(run, "two streams at cap 2", (state) => state.open === 2 && state.registering === 0);
  await requireRegisteredStreams(proxy, appWorkerID, 2, 0);
  requireNoPermanentRegistrationError(run);
});

// Admission is re-checked before the Register is written; a cap drop after it gets a Server rejection, soft in adaptive mode.
test("adaptive Worker re-checks admission between the backoff and the Register", { timeout: 120_000 }, async (t) => {
  const scope = new TestScope(t);
  captureConsole(t, "warn");
  const server = await AdaptiveSlotTestServer.start(scope, { maxConcurrentTasksCap: 8 });
  const proxy = await PullSlotProxy.start(scope, server.address);
  const barrier = new RegisterSendBarrier();
  scope.defer(() => barrier.release());
  const appWorkerID = workerID("adaptive-barrier");
  const run = startAdaptiveSlotWorker(scope, proxy.address, new AdaptiveSlotHandlers(), {
    maxConcurrentTasks: 8, minOpenSlots: 3, reservedRpcTasks: 0, appWorkerID,
  }, { shrinkWindowMs: 1_000, beforeRegisterSendForTest: barrier.hold });
  await run.worker.waitPullRegistered({ timeoutMs: 60_000 });
  await requireRegisteredStreams(proxy, appWorkerID, 3, 0);

  // Every floor stream breaks; each runner is admitted under the old cap of 8 and waits before its Register.
  barrier.isArmed = true;
  await server.kill();
  await eventually(() => barrier.held === 3, 30_000, () => `only ${barrier.held} runners reached the Register barrier`);
  await server.restart({ maxConcurrentTasksCap: 2 });
  proxy.setUpstream(server.address);
  // One runner sends, registers, and reports cap 2 while the other two stay admitted but unsent.
  barrier.pass();
  await requireSlotState(run, "the lower cap is known", (state) => state.totalCapacity === 2, 30_000);
  const capKnownAtMs = Date.now();
  barrier.release();
  // The two held attempts are re-checked after cap 2 is known: one is revoked and sends nothing.
  // A Register that passed its re-check just before the cap became known may still meet the cap once;
  // that rejection is soft, and the latch retries it at the next window.
  await never(() => {
    const rejections = proxy.rejectionsSince(appWorkerID, capKnownAtMs).length;
    return rejections > 1 || stepRegisters(proxy.registersSince(appWorkerID, capKnownAtMs)).length > 1 + rejections
      || proxy.registeredStreams(appWorkerID, STEP_AND_RPC) > 2;
  }, 3_000, "a revoked attempt sent a Register, or Registers exceeded the new cap");
  await requireRegisteredStreams(proxy, appWorkerID, 2, 0);
  await requireSlotState(run, "two streams at cap 2", (state) => state.open === 2 && state.registering === 0);
  const rejections = proxy.rejectionsSince(appWorkerID, capKnownAtMs).length;
  if (rejections > 0) assert.ok(run.state().growRejectedAtCap >= rejections, "the rejection was not handled as soft");
  requireNoPermanentRegistrationError(run);
});

// A Register that never reaches the Server must not stop cap updates, handlers on other streams, or close.
test("adaptive Worker keeps working and closes while one Register is blocked", { timeout: 120_000 }, async (t) => {
  const scope = new TestScope(t);
  const server = await AdaptiveSlotTestServer.start(scope, { maxConcurrentTasksCap: 4 });
  const proxy = await PullSlotProxy.start(scope, server.address);
  const releaseHeldRegister = proxy.holdStepRegisters(1);
  scope.defer(releaseHeldRegister);
  const appWorkerID = workerID("adaptive-blocked-send");
  const run = startAdaptiveSlotWorker(scope, proxy.address, new AdaptiveSlotHandlers(), {
    maxConcurrentTasks: 8, minOpenSlots: 3, stopDrainTimeoutMs: 1_000, appWorkerID,
  }, { shrinkWindowMs: HOUR_MS });

  // The first Step-and-RPC Register is held; the second registers and reports cap 4.
  await eventually(() => proxy.heldStepRegisters() === 1, 20_000, () => proxy.describe(appWorkerID));
  await requireSlotState(run, "a cap update on another stream", (state) => state.totalCapacity === 4 && state.idle >= 1, 20_000);
  const client = server.newClient(run.registry);
  await requireFlowsCompleted(client, await startAdaptiveSlotFlows(client, run.flow, ADAPTIVE_SLOT_MODE_QUICK, 2));
  assert.equal(proxy.heldStepRegisters(), 1, "the held Register reached the Server on its own");

  // Close finishes within its drain timeout although one Register never reaches the Server.
  const closeStartedAtMs = Date.now();
  const hasClosed = await Promise.race([run.worker.close().then(() => true), delay(15_000).then(() => false)]);
  assert.ok(hasClosed, "close did not finish while a Register was blocked");
  assert.ok(Date.now() - closeStartedAtMs < 10_000, `close took ${Date.now() - closeStartedAtMs} ms`);
  assert.equal(await run.awaitRunResult(10_000), undefined);
});

// Design Test 7 for a cap above and below the floor.
test("adaptive Worker converges to a lower cap without orphaned handlers", { timeout: 240_000 }, async (t) => {
  await t.test("new cap above the floor", async (subtest) => {
    const scope = new TestScope(subtest);
    captureConsole(subtest, "warn");
    const server = await AdaptiveSlotTestServer.start(scope, { maxConcurrentTasksCap: 8 });
    const proxy = await PullSlotProxy.start(scope, server.address);
    const handlers = new AdaptiveSlotHandlers();
    const appWorkerID = workerID("adaptive-drop-above");
    const run = startAdaptiveSlotWorker(scope, proxy.address, handlers, { maxConcurrentTasks: 8, minOpenSlots: 3, appWorkerID }, { shrinkWindowMs: 1_000 });
    await run.worker.waitPullRegistered({ timeoutMs: 60_000 });
    const client = server.newClient(run.registry);
    const flowIds = await startAdaptiveSlotFlows(client, run.flow, ADAPTIVE_SLOT_MODE_BLOCK, 6);
    await requireSlotState(run, "six busy Steps", (state) => state.busy === 6);
    const sampler = new SlotUsageSampler(scope, run);
    await server.kill();
    const restartedAtMs = Date.now();
    await server.restart({ maxConcurrentTasksCap: 4 });
    proxy.setUpstream(server.address);
    // C = 4 and R = 1 leave E = 3; the retried Steps grow the Worker back to E.
    await requireSlotState(run, "growth up to the lower cap", (state) =>
      state.totalCapacity === 4 && state.effectiveCeiling === 3 && state.busy === 3 && state.open === 3 && state.rpcOnlyOpen === 1 && state.registering === 0);
    await requireRegisteredStreams(proxy, appWorkerID, 3, 1);
    assert.deepEqual(proxy.rejectionsSince(appWorkerID, restartedAtMs), []);
    assert.deepEqual(proxy.workerResetsSince(appWorkerID, restartedAtMs), [], "cap convergence reset a stream");
    requireNoPermanentRegistrationError(run);
    handlers.release();
    await requireFlowsCompleted(server.newClient(run.registry), flowIds);
    sampler.requireWithinCapacity();
    assert.deepEqual(proxy.workerResetsSince(appWorkerID, restartedAtMs), [], "cap convergence reset a stream");
  });
  await t.test("new cap below the floor", async (subtest) => {
    const scope = new TestScope(subtest);
    captureConsole(subtest, "warn");
    const server = await AdaptiveSlotTestServer.start(scope, { maxConcurrentTasksCap: 8 });
    const proxy = await PullSlotProxy.start(scope, server.address);
    const handlers = new AdaptiveSlotHandlers();
    const appWorkerID = workerID("adaptive-drop-below");
    const run = startAdaptiveSlotWorker(scope, proxy.address, handlers, {
      maxConcurrentTasks: 8, minOpenSlots: 5, appWorkerID, stopOnPermanentStreamRejection: true,
    }, { shrinkWindowMs: 1_000 });
    await run.worker.waitPullRegistered({ timeoutMs: 60_000 });
    await requireRegisteredStreams(proxy, appWorkerID, 4, 1);
    await server.kill();
    const restartedAtMs = Date.now();
    await server.restart({ maxConcurrentTasksCap: 3 });
    proxy.setUpstream(server.address);
    // C = 3 and R = 1 leave E = 2, so the floor of 4 becomes L_eff = 2.
    await requireSlotState(run, "the floor limited by the lower cap", (state) =>
      state.totalCapacity === 3 && state.floor === 2 && state.open === 2 && state.rpcOnlyOpen === 1 && state.registering === 0);
    await requireRegisteredStreams(proxy, appWorkerID, 2, 1);
    assert.deepEqual(proxy.workerResetsSince(appWorkerID, restartedAtMs), [], "cap convergence reset a stream");
    requireNoPermanentRegistrationError(run);
    const restartedClient = server.newClient(run.registry);
    await requireFlowsCompleted(restartedClient, await startAdaptiveSlotFlows(restartedClient, run.flow, ADAPTIVE_SLOT_MODE_QUICK, 3));
  });
});

// Design Test 12.
test("adaptive Worker drains a graceful stop during growth without spending retries", { timeout: 180_000 }, async (t) => {
  const scope = new TestScope(t);
  const server = await AdaptiveSlotTestServer.start(scope);
  const handlers = new AdaptiveSlotHandlers();
  const run = startAdaptiveSlotWorker(scope, server.address, handlers, { maxConcurrentTasks: 32, minOpenSlots: 3, appWorkerID: workerID("adaptive-stop") }, { shrinkWindowMs: 1_000 });
  await run.worker.waitPullRegistered({ timeoutMs: 60_000 });
  const client = server.newClient(run.registry);
  const flowIds = await startAdaptiveSlotFlowsConcurrently(client, run.flow, ADAPTIVE_SLOT_MODE_BLOCK, 20);
  await handlers.awaitEntries(4);
  const pullWorker = Reflect.get(run.worker, "pullWorker") as PullWorker;
  const stopping = run.worker.close();
  await eventually(() => Reflect.get(pullWorker, "isStopping") === true, 10_000, () => "close did not begin");
  // Busy handlers finish during the drain; tasks that never started return without spending a retry.
  handlers.release();
  await stopping;
  assert.equal(await run.awaitRunResult(10_000), undefined);
  const replacement = startAdaptiveSlotWorker(scope, server.address, handlers, { maxConcurrentTasks: 8, appWorkerID: workerID("fixed-replacement") }, { shrinkWindowMs: HOUR_MS });
  await replacement.worker.waitPullRegistered({ timeoutMs: 60_000 });
  await requireFlowsCompleted(client, flowIds);
  for (const entry of handlers.drainEntries()) assert.equal(entry.attempt, 1, `a Step spent a retry: ${JSON.stringify(entry)}`);
});

// Design Test 13 against one server.
test("adaptive Worker takes over the load of a fixed Worker that leaves", { timeout: 180_000 }, async (t) => {
  const scope = new TestScope(t);
  const server = await AdaptiveSlotTestServer.start(scope);
  const handlers = new AdaptiveSlotHandlers();
  const fixed = startAdaptiveSlotWorker(scope, server.address, handlers, { maxConcurrentTasks: 4, appWorkerID: workerID("fixed-fleet") }, { shrinkWindowMs: HOUR_MS });
  const adaptive = startAdaptiveSlotWorker(scope, server.address, handlers, { maxConcurrentTasks: 16, minOpenSlots: 2, appWorkerID: workerID("adaptive-fleet") }, { shrinkWindowMs: 1_000 });
  await fixed.worker.waitPullRegistered({ timeoutMs: 60_000 });
  await adaptive.worker.waitPullRegistered({ timeoutMs: 60_000 });
  const client = server.newClient(adaptive.registry);
  await requireFlowsCompleted(client, await startAdaptiveSlotFlows(client, adaptive.flow, ADAPTIVE_SLOT_MODE_QUICK, 10));
  const quickEntries = handlers.drainEntries();
  // The fixed Worker leaves; its share moves to the adaptive Worker, which grows from its floor.
  await fixed.worker.close();
  const flowIds = await startAdaptiveSlotFlows(client, adaptive.flow, ADAPTIVE_SLOT_MODE_BLOCK, 8);
  await handlers.awaitEntries(8);
  await requireSlotState(adaptive, "the adaptive Worker grew to the load", (state) => state.busy === 8 && state.open >= 8 && state.open <= 15);
  handlers.release();
  await requireFlowsCompleted(client, flowIds);
  for (const entry of [...quickEntries, ...handlers.drainEntries()]) {
    assert.equal(entry.attempt, 1, `a Step spent a retry: ${JSON.stringify(entry)}`);
  }
});

interface OrphanedHandlerScenario {
  readonly server: AdaptiveSlotTestServer;
  readonly proxy: PullSlotProxy;
  readonly run: AdaptiveSlotWorkerRun;
  readonly handlers: AdaptiveSlotHandlers;
  readonly appWorkerID: string;
  readonly flowIds: readonly string[];
  readonly rpcResult: Promise<unknown>;
  readonly sampler: SlotUsageSampler;
}

// Fills every slot of an adaptive Worker with handlers that ignore cancellation.
async function startOrphanedHandlerScenario(context: Parameters<typeof captureConsole>[0]): Promise<OrphanedHandlerScenario> {
  const scope = new TestScope(context);
  captureConsole(context, "warn");
  const server = await AdaptiveSlotTestServer.start(scope, { maxConcurrentTasksCap: 4 });
  const proxy = await PullSlotProxy.start(scope, server.address);
  const handlers = new AdaptiveSlotHandlers();
  handlers.isRpcStubborn = true;
  const appWorkerID = workerID("adaptive-orphan");
  const run = startAdaptiveSlotWorker(scope, proxy.address, handlers, { maxConcurrentTasks: 4, minOpenSlots: 2, appWorkerID }, { shrinkWindowMs: HOUR_MS });
  await run.worker.waitPullRegistered({ timeoutMs: 60_000 });
  const client = server.newClient(run.registry);
  const parkedId = await startParkedFlow(client, run.parked);
  const { flowIds, rpcResult } = await occupyEverySlot(run, client, handlers, parkedId);
  return { server, proxy, run, handlers, appWorkerID, flowIds, rpcResult, sampler: new SlotUsageSampler(scope, run) };
}

// Starts E stubborn Steps, then one stubborn RPC that can only land on the RPC-only stream.
async function occupyEverySlot(
  run: AdaptiveSlotWorkerRun,
  client: Client,
  handlers: AdaptiveSlotHandlers,
  parkedId: string,
): Promise<{ flowIds: string[]; rpcResult: Promise<unknown> }> {
  const ceiling = run.state().effectiveCeiling;
  const flowIds = await startAdaptiveSlotFlows(client, run.flow, ADAPTIVE_SLOT_MODE_STUBBORN, ceiling);
  await handlers.awaitEntries(ceiling);
  await requireSlotState(run, "every Step-and-RPC slot busy", (state) => state.busy === ceiling && state.open === ceiling);
  const rpcResult = client.invokeRPC(run.parked.blockUntilReleased, parkedId);
  rpcResult.catch(() => {});
  const [entry] = await handlers.awaitEntries(1);
  assert.equal(entry!.kind, "rpc");
  return { flowIds, rpcResult };
}

async function requireCapOfOneWorkerServes(server: AdaptiveSlotTestServer, proxy: PullSlotProxy, run: AdaptiveSlotWorkerRun, appWorkerID: string): Promise<void> {
  await run.worker.waitPullRegistered({ timeoutMs: 60_000 });
  await requireSlotState(run, "one Step-and-RPC stream at cap 1", (state) =>
    state.totalCapacity === 1 && state.rpcOnlySlots === 0 && state.open === 1 && state.rpcOnlyOpen === 0 && state.registering === 0);
  await requireRegisteredStreams(proxy, appWorkerID, 1, 0);
  const registration = run.worker.pullRegistrationState();
  assert.equal(registration.isReady, true);
  assert.equal(registration.queues.length, 1);
  const client = server.newClient(run.registry);
  await requireFlowsCompleted(client, await startAdaptiveSlotFlows(client, run.flow, ADAPTIVE_SLOT_MODE_QUICK, 2));
  const parkedId = await startParkedFlow(client, run.parked);
  assert.equal(await client.invokeRPC(run.parked.getSlotStatus, parkedId), "ok");
}

function workerID(prefix: string): string {
  return `${prefix}-${randomUUID().slice(0, 8)}`;
}
