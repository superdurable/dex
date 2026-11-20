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
import { randomBytes } from "node:crypto";
import { chmodSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { inspect } from "node:util";
import test, { after } from "node:test";
import { credentials } from "@grpc/grpc-js";

import {
  AesGcmPayloadCodec,
  AttributeMatch,
  Client,
  FilePayloadEncryptionKeyProvider,
  InitialAttribute,
  PayloadEncryptionError,
  PayloadEnvelope,
  Registry,
  TimeTravelType,
  StopType,
  openBlobCache,
  stringCodec,
  type Flow,
  type PayloadCodec,
  type PayloadCodecDecodeOptions,
  type PayloadContext,
  type PayloadEncryptionKeyProvider,
  type SearchFlowEntry,
} from "../../src/index.js";
import {
  FlowServiceClient,
  GetFlowStateResponse,
  GetHistoryEventsResponse,
  type FlowResult as ProtoFlowResult,
  type KV,
  type ReadStreamResponse,
  type Value,
} from "../../src/gen/dex.js";
import { flowId, startEnvironmentWithOptions, withEnvironment, type TestEnvironment } from "./environment.js";
import {
  ENCRYPTED_INDEXED_STATUS_KEY,
  ENCRYPTED_SEARCH_KEY,
  EncryptedActionConditionFlow,
  EncryptedChildFlow,
  EncryptedHeartbeatFlow,
  EncryptedMatchFlow,
  EncryptedRecoveryFlow,
  EncryptedRoundTripFlow,
  LARGE_NOTE,
  ThrowingFlow,
  roundTripOutputCodec,
} from "./payload_encryption_flow.js";

const keyDirectory = mkdtempSync(join(tmpdir(), "dex-payload-encryption-integ-"));
after(() => rmSync(keyDirectory, { recursive: true, force: true }));
const keys = { k1: randomBytes(32).toString("base64"), k2: randomBytes(32).toString("base64") };

async function fileProvider(currentKeyId: "k1" | "k2"): Promise<FilePayloadEncryptionKeyProvider> {
  const path = join(keyDirectory, `keys-${currentKeyId}.json`);
  writeFileSync(path, JSON.stringify({ current_key_id: currentKeyId, keys }));
  chmodSync(path, 0o600);
  return FilePayloadEncryptionKeyProvider.fromFile(path);
}

interface CodecCall {
  readonly operation: "encode" | "decode";
  readonly context: PayloadContext;
}

/** Records every codec call so tests can check the PayloadContext the SDK passed. */
class RecordingPayloadCodec implements PayloadCodec {
  public readonly calls: CodecCall[] = [];

  public constructor(private readonly inner: PayloadCodec) {}

  public encode(context: PayloadContext, plaintexts: readonly Uint8Array[]): Promise<PayloadEnvelope[]> {
    this.calls.push({ operation: "encode", context });
    return this.inner.encode(context, plaintexts);
  }

  public decode(context: PayloadContext, envelopes: readonly PayloadEnvelope[]): Promise<Uint8Array[]> {
    this.calls.push({ operation: "decode", context });
    return this.inner.decode(context, envelopes);
  }
}

function rawFlowService(): InstanceType<typeof FlowServiceClient> {
  return new FlowServiceClient(process.env.DEX_SERVER_ADDRESS ?? "127.0.0.1:8801", credentials.createInsecure());
}

function rawCall<Response>(
  invoke: (client: InstanceType<typeof FlowServiceClient>, callback: (error: Error | null, response: Response) => void) => void,
): Promise<Response> {
  const client = rawFlowService();
  return new Promise<Response>((resolve, reject) => {
    invoke(client, (error, response) => {
      if (error !== null && error !== undefined) {
        reject(error);
        return;
      }
      resolve(response);
    });
  }).finally(() => client.close());
}

function rawAttributes(id: string, keys: readonly string[]): Promise<Map<string, Value | undefined>> {
  return rawCall<{ attributes: KV[] }>((client, callback) =>
    client.getAttributes({ flowId: id, runId: "", keys: [...keys], allKeys: false }, callback))
    .then((response) => new Map(response.attributes.map((entry) => [entry.key, entry.value])));
}

async function rawLoadedAttribute(id: string, key: string): Promise<Value> {
  const value = (await rawAttributes(id, [key])).get(key);
  assert.ok(value !== undefined, `raw Attribute ${key} is missing`);
  return rawLoadedValue(id, value);
}

/** Loads a blob-backed raw Value through LoadBlobs, or returns an inline Value unchanged. */
async function rawLoadedValue(id: string, value: Value): Promise<Value> {
  const kind = value.kind;
  if (kind?.$case !== "internalBlobIdForObjValue" && kind?.$case !== "internalBlobIdForStringValue") {
    return value;
  }
  const loaded = await rawCall<{ values: { [key: string]: Value } }>((client, callback) =>
    client.loadBlobs({ entries: [{ flowId: id, blobValue: value }] }, callback));
  return loaded.values[kind.value]!;
}

/** Reads every history page of the current run attempt and returns the serialized responses. */
async function rawHistoryBytes(id: string): Promise<Buffer> {
  const pages: Buffer[] = [];
  let nextPageToken: Uint8Array = new Uint8Array();
  for (let page = 0; page < 100; page += 1) {
    const response = await rawCall<GetHistoryEventsResponse>((service, callback) => service.getHistoryEvents({
      flowId: id, runId: "", startInternalEventId: 0n, estimatePageSize: 1000, nextPageToken, runAttempt: 0,
    }, callback));
    pages.push(Buffer.from(GetHistoryEventsResponse.encode(response).finish()));
    if (response.nextPageToken.length === 0) {
      break;
    }
    nextPageToken = response.nextPageToken;
  }
  return Buffer.concat(pages);
}

async function assertHistoryHidesMarker(id: string, marker: string, label: string): Promise<void> {
  const history = await rawHistoryBytes(id);
  assert.ok(history.length > 0, `${label}: history is empty`);
  assert.equal(history.includes(Buffer.from(marker)), false, `${label}: history contains plaintext ${marker}`);
}

function plaintextMarker(prefix: string): string {
  return `${prefix}${randomBytes(8).toString("hex")}`;
}

function assertEnvelope(value: Value | undefined, label: string, keyId?: string): void {
  assert.equal(value?.kind?.$case, "objValue", `${label} must be stored as an envelope`);
  const object = (value!.kind as { value: { encoding: string; payload: Uint8Array } }).value;
  assert.equal(object.encoding, "dex/1", label);
  if (keyId !== undefined) {
    assert.equal(PayloadEnvelope.decode(object.payload).keyId, keyId, `${label} key ID`);
  }
}

function assertPlaintextString(value: Value | undefined, expected: string, label: string): void {
  assert.deepEqual(value?.kind, { $case: "stringValue", value: expected }, label);
}

async function waitUntil<T>(read: () => Promise<T | undefined>, label: string, timeoutMs = 30_000): Promise<T> {
  const deadline = Date.now() + timeoutMs;
  let lastError: unknown;
  while (Date.now() < deadline) {
    try {
      const value = await read();
      if (value !== undefined) {
        return value;
      }
    } catch (failure) {
      lastError = failure;
    }
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  throw new Error(`${label} did not happen: ${String(lastError)}`);
}

async function encryptedEnvironment(
  flows: readonly Flow<any>[],
  codec: PayloadCodec,
): Promise<TestEnvironment> {
  return startEnvironmentWithOptions(flows, {
    client: { payloadCodec: codec },
    worker: { payloadCodec: codec },
  });
}

async function withEncryptedEnvironment(
  flows: readonly Flow<any>[],
  codec: PayloadCodec,
  run: (environment: TestEnvironment) => Promise<void>,
): Promise<void> {
  const environment = await encryptedEnvironment(flows, codec);
  try {
    await run(environment);
  } finally {
    await environment.close();
  }
}

test("encrypted Values round trip on every path while Dex stores only ciphertext", async () => {
  const child = new EncryptedChildFlow();
  const flow = new EncryptedRoundTripFlow(child);
  const codec = new RecordingPayloadCodec(new AesGcmPayloadCodec(await fileProvider("k1")));
  await withEncryptedEnvironment([flow, child], codec, async ({ client }) => {
    const id = flowId("encrypted-round-trip");
    const searchValue = `encrypted-search-${randomBytes(6).toString("hex")}`;
    const text = plaintextMarker("valuemarker");
    const rpcInput = plaintextMarker("rpcmarker");
    const initialNote = plaintextMarker("initialmarker");
    await client.startFlow(flow, id, { text, searchValue, failChild: false }, {
      attributes: [InitialAttribute.mapValue(flow.notes, "initial", { text: initialNote })],
    });

    await waitUntil(async () => (await client.getAttributes(id, flow.secretNote))[0], "Step Attribute write");
    const raw = await rawAttributes(id, [
      flow.secretNote.name,
      flow.secretCount.name,
      flow.largeNote.name,
      flow.searchStatus.name,
      flow.stage.name,
      "encrypted-notes/first",
      "encrypted-notes/initial",
    ]);
    assertEnvelope(raw.get(flow.secretNote.name), "string Attribute", "k1");
    assertEnvelope(raw.get(flow.secretCount.name), "int Attribute");
    assertEnvelope(raw.get("encrypted-notes/first"), "AttributeMap instance");
    assertEnvelope(raw.get("encrypted-notes/initial"), "initial AttributeMap instance");
    assertPlaintextString(raw.get(flow.searchStatus.name), searchValue, "indexed Attribute");
    assertPlaintextString(raw.get(flow.stage.name), "waiting", "Plaintext Attribute");
    const rawLarge = raw.get(flow.largeNote.name);
    assert.equal(rawLarge?.kind?.$case, "internalBlobIdForObjValue", "large ciphertext is offloaded to a blob");
    assertEnvelope(await rawLoadedAttribute(id, flow.largeNote.name), "blob-loaded Attribute");

    for (let read = 0; read < 2; read += 1) {
      const [note, count, large, status, stage, first, initial] = await client.getAttributes(
        id,
        flow.secretNote,
        flow.secretCount,
        flow.largeNote,
        flow.searchStatus,
        flow.stage,
        flow.notes.instance("first"),
        flow.notes.instance("initial"),
      );
      assert.equal(note, `secret-${text}`);
      assert.equal(count, 42n);
      assert.equal(large, LARGE_NOTE, `blob read ${read} (the second read hits the BlobCache)`);
      assert.equal(status, searchValue);
      assert.equal(stage, "waiting");
      assert.deepEqual(first, { text: `map-${text}` });
      assert.deepEqual(initial, { text: initialNote });
    }

    const message = await client.readStream(id, flow.progress, "", 30_000);
    assert.equal(message.value, `progress-${text}`);
    const page = await client.listStreamMessages(id, flow.progress, 10);
    assert.deepEqual(page.messages.map((entry) => entry.value), [`progress-${text}`]);
    const rawStream = await rawCall<ReadStreamResponse>((service, callback) => service.readStream({
      flowId: id, flowType: flow.getFlowType(), streamName: flow.progress.name, resumeToken: "", waitTimeSeconds: 0,
    }, callback));
    assertEnvelope(rawStream.message?.value, "Stream message");

    assert.equal(await client.invokeRPC(flow.deliver, id, rpcInput), `delivered-${rpcInput}|secret-${text}`);

    const result = await client.waitForFlow(id, 60_000);
    assert.equal(result.status, "completed");
    const output = result.singleOutput(roundTripOutputCodec);
    assert.equal(output.collected, `message-${rpcInput}|local-${text}|secret-${text}|${LARGE_NOTE.length}|42`);
    assert.equal(output.childStatus, "completed");
    assert.equal(output.childOutput?.echo, text);
    assert.equal(output.childOutput?.childNote, `child-note-${text}`);
    for (const [historyId, label] of [[id, "parent"], [output.childFlowId, "SubFlow"]] as const) {
      for (const marker of [text, rpcInput, initialNote, "large encrypted note"]) {
        await assertHistoryHidesMarker(historyId, marker, label);
      }
    }
    assert.ok(
      (await rawHistoryBytes(id)).includes(Buffer.from(searchValue)),
      "the history scan sees the indexed plaintext value, so it would see a missed plaintext write",
    );
    assert.equal(output.childOutput?.childFlowId, output.childFlowId);
    assert.equal(output.childFlowId.startsWith(`SubFlow:${id}-`), true);

    const rawResult = await rawCall<ProtoFlowResult>((service, callback) =>
      service.waitForFlow({ flowId: id, runId: "", needsResults: true, waitTimeSeconds: 1 }, callback));
    const rawOutput = rawResult.results[0]?.completedStepOutput;
    assert.ok(
      rawOutput?.kind?.$case === "internalBlobIdForObjValue" || rawOutput?.kind?.$case === "objValue",
      "the Step output is stored encrypted",
    );
    if (rawOutput?.kind?.$case === "objValue") {
      assertEnvelope(rawOutput, "Step output");
    }

    const childRaw = await rawAttributes(output.childFlowId, [child.childNote.name]);
    assertEnvelope(childRaw.get(child.childNote.name), "SubFlow Attribute");
    const childContexts = codec.calls
      .filter((call) => call.context.flowId === output.childFlowId)
      .map((call) => `${call.operation}:${call.context.flowType}`);
    assert.ok(childContexts.includes("encode:EncryptedChildFlow"), `SubFlow input encryption context: ${childContexts}`);
    assert.ok(childContexts.includes("decode:EncryptedChildFlow"), `SubFlow input decryption context: ${childContexts}`);
    for (const call of codec.calls) {
      if (call.context.flowId === id) {
        assert.ok(
          call.context.flowType === flow.getFlowType() || call.context.flowType === "",
          `a Flow ID-only call passes an empty Flow type, never another type: ${call.context.flowType}`,
        );
      }
    }
    assert.ok(
      codec.calls.some((call) => call.context.flowId === id && call.context.flowType === ""),
      "getAttributes and waitForFlow name only the Flow ID, so their context has an empty Flow type",
    );
    assert.ok(
      codec.calls.some((call) => call.context.flowId === output.childFlowId
        && call.operation === "decode" && call.context.flowType === ""),
      "the parent decrypts the SubFlow result with the child Flow ID and an empty Flow type",
    );

    const entry = await waitUntil<SearchFlowEntry>(async () => {
      const found = await client.searchFlows(`${ENCRYPTED_SEARCH_KEY} = '${searchValue}'`, 10);
      return found.flows.find((candidate) => candidate.flowId === id);
    }, "search of the indexed Attribute");
    assert.equal(entry.indexedAttributes.get(ENCRYPTED_SEARCH_KEY), searchValue);
  });
});

test("encrypted error text decodes at every read point and the SubFlow error decodes in the parent", async () => {
  const child = new EncryptedChildFlow();
  const flow = new EncryptedRoundTripFlow(child);
  const heartbeat = new EncryptedHeartbeatFlow();
  const recovery = new EncryptedRecoveryFlow();
  const codec = new AesGcmPayloadCodec(await fileProvider("k1"));
  await withEncryptedEnvironment([flow, child, heartbeat, recovery], codec, async ({ client }) => {
    const heartbeatId = flowId("encrypted-heartbeat");
    await client.startFlow(heartbeat, heartbeatId, "gamma");
    const failure = await waitUntil(async () => {
      const state = await client.getFlowState(heartbeatId);
      return state.activeStepExecutions[0]?.lastFailure;
    }, "first attempt failure");
    assert.equal(failure.detail, "first attempt declined card 4242 for gamma");
    assert.ok(failure.stackTrace.includes("first attempt declined card 4242"));
    const rawState = await rawCall<GetFlowStateResponse>((service, callback) =>
      service.getFlowState({ flowId: heartbeatId, runId: "" }, callback));
    const rawDetails = rawState.activeStepExecutions[0]?.lastFailureInfo?.details;
    assert.ok(rawDetails?.originalWorkerErrorDetail.startsWith("dex-encoded:"), "worker detail is encrypted");
    assert.ok(rawDetails?.originalWorkerErrorStackTrace.startsWith("dex-encoded:"), "stack trace is encrypted");
    assert.equal(rawDetails?.originalWorkerErrorType, "Error", "error type stays in clear");
    const heartbeatResult = await client.waitForFlow(heartbeatId, 60_000);
    assert.equal(heartbeatResult.singleOutput(stringCodec), "heartbeat-gamma");

    const recoveryId = flowId("encrypted-recovery");
    await client.startFlow(recovery, recoveryId, "delta");
    const recoveryResult = await client.waitForFlow(recoveryId, 60_000);
    assert.equal(recoveryResult.status, "failed");
    assert.equal(recoveryResult.errorMessage, "recovered: recover-me delta");
    const rawRecovery = await rawCall<ProtoFlowResult>((service, callback) =>
      service.waitForFlow({ flowId: recoveryId, runId: "", needsResults: true, waitTimeSeconds: 1 }, callback));
    assert.ok(rawRecovery.errorMessage.startsWith("dex-encoded:"), "the ForceFail reason is encrypted");

    const parentId = flowId("encrypted-child-failure");
    await client.startFlow(flow, parentId, { text: "epsilon", searchValue: `s-${parentId}`, failChild: true });
    await waitUntil(async () => (await client.getAttributes(parentId, flow.secretNote))[0], "parent wait");
    await client.invokeRPC(flow.deliver, parentId, "zeta");
    const parentResult = await client.waitForFlow(parentId, 60_000);
    const output = parentResult.singleOutput(roundTripOutputCodec);
    assert.equal(output.childStatus, "failed");
    assert.equal(output.childError, "child declined card 4242");
  });
});

test("reasons are encrypted, and clone, fork, and time travel copies still decrypt", async () => {
  const child = new EncryptedChildFlow();
  const flow = new EncryptedRoundTripFlow(child);
  const codec = new AesGcmPayloadCodec(await fileProvider("k1"));
  await withEncryptedEnvironment([flow, child], codec, async ({ client }) => {
    const cloneReason = plaintextMarker("clonereason");
    const resetReason = plaintextMarker("resetreason");
    const stopReason = plaintextMarker("stopreason");
    const sourceId = flowId("encrypted-clone-source");
    await client.startFlow(flow, sourceId, { text: "eta", searchValue: `s-${sourceId}`, failChild: false });
    await waitUntil(async () => (await client.getAttributes(sourceId, flow.secretNote))[0], "source wait");

    const cloneId = flowId("encrypted-clone");
    await client.cloneForkFlow(flow, sourceId, cloneId, {
      attributes: [InitialAttribute.of(flow.stage, "cloned")],
      reason: cloneReason,
    });
    const [cloneNote, cloneStage] = await client.getAttributes(cloneId, flow.secretNote, flow.stage);
    assert.equal(cloneNote, "secret-eta");
    assert.equal(cloneStage, "cloned");
    await client.invokeRPC(flow.deliver, cloneId, "clone");
    assert.equal((await client.waitForFlow(cloneId, 60_000)).status, "completed");

    await client.timeTravel(sourceId, { type: TimeTravelType.BEGINNING, reason: resetReason });
    await waitUntil(async () => (await client.getAttributes(sourceId, flow.secretNote))[0], "reset wait");
    const [resetNote] = await client.getAttributes(sourceId, flow.secretNote);
    assert.equal(resetNote, "secret-eta");

    await client.stopFlow(sourceId, { type: StopType.TERMINATE, reason: stopReason });
    assert.equal((await client.waitForFlow(sourceId, 60_000)).status, "terminated");
    for (const marker of [cloneReason, resetReason, stopReason]) {
      await assertHistoryHidesMarker(sourceId, marker, "time-travelled source");
      await assertHistoryHidesMarker(cloneId, marker, "clone");
    }
  });
});

test("key rotation keeps old values readable and encrypts new values with the new key", async () => {
  const child = new EncryptedChildFlow();
  const flow = new EncryptedRoundTripFlow(child);
  const oldCodec = new AesGcmPayloadCodec(await fileProvider("k1"));
  const newCodec = new AesGcmPayloadCodec(await fileProvider("k2"));
  const id = flowId("encrypted-rotation");
  await withEncryptedEnvironment([flow, child], oldCodec, async ({ client }) => {
    await client.startFlow(flow, id, { text: "theta", searchValue: `s-${id}`, failChild: false });
    await waitUntil(async () => (await client.getAttributes(id, flow.secretNote))[0], "old-key write");
    assertEnvelope((await rawAttributes(id, [flow.secretNote.name])).get(flow.secretNote.name), "old value", "k1");
  });
  await withEncryptedEnvironment([flow, child], newCodec, async ({ client }) => {
    const [oldNote] = await client.getAttributes(id, flow.secretNote);
    assert.equal(oldNote, "secret-theta");
    assert.equal(await client.invokeRPC(flow.deliver, id, "iota"), "delivered-iota|secret-theta");
    await client.waitForFlow(id, 60_000);
    const [notes] = await client.getAttributes(id, flow.notes.instance("first"));
    assert.deepEqual(notes, { text: "map-theta" });
    const rawEvent = await rawCall<ProtoFlowResult>((service, callback) =>
      service.waitForFlow({ flowId: id, runId: "", needsResults: true, waitTimeSeconds: 1 }, callback));
    const output = rawEvent.results[0]?.completedStepOutput;
    assert.ok(output !== undefined, "the rotated run has a Step output");
    assertEnvelope(await rawLoadedValue(id, output), "new output", "k2");
  });
});

test("one Worker serves plaintext and encrypted Flows, and server-written values stay readable", async () => {
  const flow = new EncryptedMatchFlow();
  const codec = new AesGcmPayloadCodec(await fileProvider("k1"));
  await withEncryptedEnvironment([flow], codec, async ({ client }) => {
    const plaintextClient = new Client(new Registry([flow]), openBlobCache({
      directory: mkdtempSync(join(keyDirectory, "plain-cache-")),
      maxBytes: 1 << 20,
    }), { serverAddress: process.env.DEX_SERVER_ADDRESS ?? "127.0.0.1:8801" });
    try {
      const plainId = flowId("plaintext-flow");
      await plaintextClient.startFlow(flow, plainId, "plain", {
        attributes: [InitialAttribute.of(flow.encryptedStatus, "plain-status")],
      });
      assertPlaintextString(
        (await rawAttributes(plainId, [flow.encryptedStatus.name])).get(flow.encryptedStatus.name),
        "plain-status",
        "a codec-less Client writes plaintext",
      );
      const [plainStatus] = await client.getAttributes(plainId, flow.encryptedStatus);
      assert.equal(plainStatus, "plain-status", "the codec Client passes plaintext through");

      const encryptedId = flowId("encrypted-flow");
      const indexedValue = `indexed-${randomBytes(6).toString("hex")}`;
      await client.startFlow(flow, encryptedId, "encrypted", {
        attributes: [
          InitialAttribute.of(flow.plaintextStatus, "ready"),
          InitialAttribute.of(flow.indexedStatus, indexedValue),
          InitialAttribute.of(flow.encryptedStatus, "secret-status"),
        ],
      });
      const raw = await rawAttributes(encryptedId, ["DexWorkQueuePermissions", flow.encryptedStatus.name]);
      assert.equal(raw.get("DexWorkQueuePermissions")?.kind?.$case, "objValue");
      assert.equal(
        (raw.get("DexWorkQueuePermissions")!.kind as { value: { encoding: string } }).value.encoding,
        "json",
        "the work-queue permissions stay plaintext JSON",
      );
      assertEnvelope(raw.get(flow.encryptedStatus.name), "encrypted initial Attribute");
      const entry = await waitUntil<SearchFlowEntry>(async () => {
        const found = await client.searchFlows(`${ENCRYPTED_INDEXED_STATUS_KEY} = '${indexedValue}'`, 10);
        return found.flows.find((candidate) => candidate.flowId === encryptedId);
      }, "search");
      assert.deepEqual(entry.indexedAttributes.get("DexWorkQueuePermissions"), ["encrypted.approve"]);

      const noCodecFailure = await plaintextClient.getAttributes(encryptedId, flow.encryptedStatus).then(
        () => assert.fail("a codec-less Client must not read an encrypted value"),
        (failure: unknown) => failure,
      );
      assert.ok(noCodecFailure instanceof Error);
      assert.match(noCodecFailure.message, /value is encrypted; configure a PayloadCodec/);
      assert.ok(!noCodecFailure.message.includes("secret-status"));

      await client.invokeRPC(flow.setStatuses, encryptedId, "ready");
      assert.equal(
        await client.waitForAttributeMatch(encryptedId, flow.plaintextStatus, AttributeMatch.equalTo("ready"), {
          requestTimeoutMs: 30_000,
        }),
        "ready",
      );
      assert.equal(
        await client.waitForAttributeMatch(encryptedId, flow.indexedStatus, AttributeMatch.equalTo("ready"), {
          requestTimeoutMs: 30_000,
        }),
        "ready",
      );
      await assert.rejects(
        client.waitForAttributeMatch(encryptedId, flow.encryptedStatus, AttributeMatch.equalTo("ready"), {
          requestTimeoutMs: 30_000,
        }),
        (failure: unknown) => failure instanceof TypeError && /plaintext\(\)/.test(failure.message),
      );
      await client.invokeRPC(flow.approve, encryptedId);
      assert.equal((await client.waitForFlow(encryptedId, 60_000)).singleOutput(stringCodec), "approved");
      await assert.rejects(
        plaintextClient.waitForFlow(encryptedId, 30_000),
        (failure: unknown) => failure instanceof PayloadEncryptionError
          && failure.category === "codec_not_configured"
          && !failure.message.includes("approved"),
        "a codec-less Client must not return a FlowResult that holds an encrypted output",
      );
      await client.invokeRPC(flow.approve, plainId).catch(() => undefined);
    } finally {
      await plaintextClient.close();
    }
  });
});

test("an Action condition on an encrypted Attribute fails the handler before Dex sees it", async () => {
  const flow = new EncryptedActionConditionFlow();
  const codec = new AesGcmPayloadCodec(await fileProvider("k1"));
  await withEncryptedEnvironment([flow], codec, async ({ client }) => {
    const id = flowId("encrypted-action-condition");
    await client.startFlow(flow, id, "start");
    const failure = await client.invokeRPC(flow.writeCondition, id, "ready").then(
      () => assert.fail("the RPC must fail"),
      (error: unknown) => error,
    );
    assert.ok(failure instanceof Error);
    assert.match(failure.message, /would be encrypted/);
    await client.stopFlow(id, { type: StopType.TERMINATE });
  });
});

test("a tenant key provider refuses another tenant's ciphertext", async () => {
  const flow = new EncryptedMatchFlow();
  const tenantKeys = { "tenant-a-k1": randomBytes(32), "tenant-b-k1": randomBytes(32) };
  const tenantProvider: PayloadEncryptionKeyProvider = {
    async currentKeyId(context: PayloadContext): Promise<string> {
      return `${tenantOf(context.flowId)}-k1`;
    },
    async key(context: PayloadContext, keyId: string): Promise<Uint8Array> {
      if (!keyId.startsWith(`${tenantOf(context.flowId)}-`)) {
        throw new Error(`key ${keyId} does not belong to this tenant`);
      }
      return tenantKeys[keyId as keyof typeof tenantKeys];
    },
  };
  const codec = new AesGcmPayloadCodec(tenantProvider);
  await withEncryptedEnvironment([flow], codec, async ({ client }) => {
    const tenantA = flowId("tenant-a");
    const tenantB = flowId("tenant-b");
    await client.startFlow(flow, tenantB, "b", { attributes: [InitialAttribute.of(flow.encryptedStatus, "tenant-b-secret")] });
    await client.startFlow(flow, tenantA, "a", { attributes: [InitialAttribute.of(flow.encryptedStatus, "tenant-a-secret")] });
    const stolen = (await rawAttributes(tenantB, [flow.encryptedStatus.name])).get(flow.encryptedStatus.name)!;
    await rawCall((service, callback) => service.setAttributes({
      flowId: tenantA,
      runId: "",
      attributes: [{ key: flow.encryptedStatus.name, value: stolen, indexConfig: undefined, syncConfig: undefined, isHardDeleteOnNull: false }],
      requestId: flowId("copy"),
      actionPermissionMappings: undefined,
    }, callback));
    await assert.rejects(
      client.getAttributes(tenantA, flow.encryptedStatus),
      (failure: unknown) => failure instanceof PayloadEncryptionError
        && failure.category === "unknown_key"
        && !failure.message.includes("tenant-b-secret"),
    );
    for (const id of [tenantA, tenantB]) {
      await client.stopFlow(id, { type: StopType.TERMINATE });
    }
  });
});

function tenantOf(flowIdValue: string): string {
  return flowIdValue.startsWith("tenant-b-") ? "tenant-b" : "tenant-a";
}

test("a codec-less Worker leaves encrypted Flows failing with the missing-codec error", async () => {
  const flow = new EncryptedMatchFlow();
  const codec = new AesGcmPayloadCodec(await fileProvider("k1"));
  const id = flowId("encrypted-no-codec-worker");
  await withEnvironment([flow], async ({ client: plainClient }) => {
    const encryptingClient = new Client(new Registry([flow]), openBlobCache({
      directory: mkdtempSync(join(keyDirectory, "enc-cache-")),
      maxBytes: 1 << 20,
    }), { serverAddress: process.env.DEX_SERVER_ADDRESS ?? "127.0.0.1:8801", payloadCodec: codec });
    try {
      await encryptingClient.startFlow(flow, id, "encrypted input");
      const failure = await waitUntil(async () => {
        const state = await plainClient.getFlowState(id);
        return state.activeStepExecutions[0]?.lastFailure;
      }, "codec-less Worker failure");
      assert.match(failure.detail, /value is encrypted; configure a PayloadCodec/);
      assert.ok(!failure.detail.includes("encrypted input"));
      await encryptingClient.stopFlow(id, { type: StopType.TERMINATE });
    } finally {
      await encryptingClient.close();
    }
  });
});

test("a codec-less Worker fails an attempt that loads an encrypted Attribute even when it does not read it", async () => {
  const flow = new EncryptedMatchFlow();
  const codec = new AesGcmPayloadCodec(await fileProvider("k1"));
  const id = flowId("encrypted-unread-attribute");
  const secret = plaintextMarker("unreadmarker");
  await withEnvironment([flow], async ({ client: plainClient }) => {
    const encryptingClient = new Client(new Registry([flow]), openBlobCache({
      directory: mkdtempSync(join(keyDirectory, "unread-cache-")),
      maxBytes: 1 << 20,
    }), {
      serverAddress: process.env.DEX_SERVER_ADDRESS ?? "127.0.0.1:8801",
      payloadCodec: codec,
      encryptStringValues: false,
    });
    try {
      await encryptingClient.startFlow(flow, id, "plaintext input", {
        attributes: [InitialAttribute.of(flow.unreadDetails, { text: secret })],
      });
      assertEnvelope(
        (await rawAttributes(id, [flow.unreadDetails.name])).get(flow.unreadDetails.name),
        "unread object Attribute",
      );
      const failure = await waitUntil(async () => {
        const state = await plainClient.getFlowState(id);
        return state.activeStepExecutions[0]?.lastFailure;
      }, "codec-less Worker failure for an unread encrypted Attribute");
      assert.match(failure.detail, /value is encrypted; configure a PayloadCodec/);
      assert.ok(!failure.detail.includes(secret) && !failure.stackTrace.includes(secret));
      await encryptingClient.stopFlow(id, { type: StopType.TERMINATE });
    } finally {
      await encryptingClient.close();
    }
  });
});

const leakSentinel = "KEY_MATERIAL_SENTINEL";

/** A custom codec whose every failure carries the sentinel in its message, cause, and properties. */
class SentinelThrowingCodec implements PayloadCodec {
  public constructor(private readonly failure: () => Error) {}

  public async encode(): Promise<PayloadEnvelope[]> {
    throw this.failure();
  }

  public async decode(): Promise<Uint8Array[]> {
    throw this.failure();
  }
}

const leakingCodecs: readonly { readonly name: string; readonly codec: () => PayloadCodec; readonly category: RegExp }[] = [
  {
    name: "key provider",
    codec: () => new AesGcmPayloadCodec({
      async currentKeyId(): Promise<string> {
        throw new Error(leakSentinel);
      },
      async key(): Promise<Uint8Array> {
        throw new Error(leakSentinel);
      },
    }),
    category: /unknown_key/,
  },
  {
    name: "custom codec plain Error",
    codec: () => new SentinelThrowingCodec(() => Object.assign(new Error(leakSentinel), { secret: leakSentinel })),
    category: /codec_failed/,
  },
  {
    name: "custom codec PayloadEncryptionError",
    codec: () => new SentinelThrowingCodec(() => new PayloadEncryptionError(
      "decode",
      leakSentinel as never,
      leakSentinel,
      leakSentinel,
      { cause: new Error(leakSentinel) },
    )),
    category: /codec_failed/,
  },
  {
    name: "custom codec error with throwing getters",
    codec: () => new SentinelThrowingCodec(() => hostilePayloadError("getters")),
    category: /codec_failed/,
  },
  {
    name: "custom codec Proxy error",
    codec: () => new SentinelThrowingCodec(() => hostilePayloadError("proxy")),
    category: /codec_failed/,
  },
  {
    name: "custom codec that relabels envelopes and throws",
    codec: () => new EnvelopeRelabelingCodec("throw"),
    category: /codec_failed|authentication_failed/,
  },
  {
    name: "custom codec that relabels envelopes and returns an invalid plaintext",
    codec: () => new EnvelopeRelabelingCodec("invalid"),
    category: /codec_failed|invalid_plaintext/,
  },
];

/** Rewrites each envelope's key ID to the sentinel, then fails so the SDK reports a key ID. */
class EnvelopeRelabelingCodec implements PayloadCodec {
  public constructor(private readonly outcome: "throw" | "invalid") {}

  public async encode(): Promise<PayloadEnvelope[]> {
    throw new Error(leakSentinel);
  }

  public async decode(_context: PayloadContext, envelopes: readonly PayloadEnvelope[]): Promise<Uint8Array[]> {
    for (const envelope of envelopes) {
      (envelope as { keyId: string }).keyId = leakSentinel;
    }
    if (this.outcome === "throw") {
      throw new PayloadEncryptionError("decode", "authentication_failed", leakSentinel, leakSentinel);
    }
    return envelopes.map(() => new Uint8Array([0xff, 0xff, 0xff]));
  }
}

/** A PayloadEncryptionError whose name, getters, or Proxy traps carry or throw the sentinel. */
function hostilePayloadError(kind: string): Error {
  const throwSentinel = (): never => {
    throw new Error(leakSentinel);
  };
  const error = new PayloadEncryptionError("decode", "unknown_key", leakSentinel, "k1", { cause: new Error(leakSentinel) });
  if (kind === "name") {
    error.name = leakSentinel;
    return error;
  }
  if (kind === "getters") {
    for (const property of ["category", "keyId", "name", "message", "stack", "operation", "cause"]) {
      Object.defineProperty(error, property, { get: throwSentinel, configurable: true });
    }
    return error;
  }
  return new Proxy(error, { get: throwSentinel, has: throwSentinel, getOwnPropertyDescriptor: throwSentinel });
}

/** Records console output while `run` executes; the SDK must never log codec or handler text. */
async function assertConsoleHidesSentinel(run: () => Promise<void>): Promise<void> {
  const lines: string[] = [];
  const originals = { error: console.error, warn: console.warn, log: console.log, info: console.info };
  const record = (...items: unknown[]): void => {
    lines.push(items.map((item) => {
      try {
        return inspect(item, { depth: 10, showHidden: true });
      } catch {
        return "[uninspectable]";
      }
    }).join(" "));
  };
  Object.assign(console, { error: record, warn: record, log: record, info: record });
  try {
    await run();
  } finally {
    Object.assign(console, originals);
  }
  const output = lines.join("\n");
  assert.ok(!output.includes(leakSentinel), output);
}

for (const kind of ["name", "getters", "proxy"]) {
  test(`a handler-thrown PayloadEncryptionError with hostile ${kind} never reaches Dex`, async () => {
    const flow = new ThrowingFlow((input) => hostilePayloadError(input));
    const codec = new AesGcmPayloadCodec(await fileProvider("k1"));
    await assertConsoleHidesSentinel(async () => {
      const environment = await startEnvironmentWithOptions([flow], {
        client: { payloadCodec: codec },
        worker: { payloadCodec: codec, encryptErrorText: false },
      });
      try {
        const id = flowId(`encrypted-hostile-${kind}`);
        await environment.client.startFlow(flow, id, kind);
        await waitUntil(async () => {
          const state = await environment.client.getFlowState(id);
          return state.activeStepExecutions[0]?.lastFailure;
        }, `Worker failure for a hostile ${kind} error`);
        const rawState = await rawCall<GetFlowStateResponse>((service, callback) =>
          service.getFlowState({ flowId: id, runId: "" }, callback));
        const details = rawState.activeStepExecutions[0]?.lastFailureInfo?.details;
        assert.equal(details?.originalWorkerErrorType, "PayloadEncryptionError");
        assert.equal(details?.originalWorkerErrorStackTrace, "");
        assert.ok(!Buffer.from(GetFlowStateResponse.encode(rawState).finish()).includes(Buffer.from(leakSentinel)));
        assert.ok(!(await rawHistoryBytes(id)).includes(Buffer.from(leakSentinel)));
        await environment.client.stopFlow(id, { type: StopType.TERMINATE });
      } finally {
        await environment.close();
      }
    });
  });
}

for (const leaking of leakingCodecs) {
  test(`${leaking.name} failures never reach Client errors or the Worker failure sent to Dex`, () => assertConsoleHidesSentinel(async () => {
    const flow = new EncryptedMatchFlow();
    const goodCodec = new AesGcmPayloadCodec(await fileProvider("k1"));
    const leakingCodec = leaking.codec();
    const id = flowId("encrypted-leaking-codec");
    const environment = await startEnvironmentWithOptions([flow], {
      client: { payloadCodec: goodCodec },
      worker: { payloadCodec: leakingCodec, encryptErrorText: false },
    });
    const leakingClient = new Client(new Registry([flow]), openBlobCache({
      directory: mkdtempSync(join(keyDirectory, "leaking-cache-")),
      maxBytes: 1 << 20,
    }), { serverAddress: process.env.DEX_SERVER_ADDRESS ?? "127.0.0.1:8801", payloadCodec: leakingCodec });
    try {
      await environment.client.startFlow(flow, id, "encrypted input", {
        attributes: [InitialAttribute.of(flow.encryptedStatus, "status")],
      });
      const startId = flowId("leaking-start");
      for (const { operation, expectedFlowId } of [
        { operation: () => leakingClient.startFlow(flow, startId, "input"), expectedFlowId: startId },
        { operation: () => leakingClient.getAttributes(id, flow.encryptedStatus), expectedFlowId: id },
      ]) {
        await assert.rejects(operation(), (failure: unknown) => {
          assert.ok(failure instanceof PayloadEncryptionError, String(failure));
          assert.equal(failure.cause, undefined);
          assert.ok(!inspect(failure, { depth: 10, showHidden: true }).includes(leakSentinel));
          assert.ok(!failure.message.includes(expectedFlowId), failure.message);
          assert.match(failure.message, leaking.category);
          return true;
        });
      }
      await waitUntil(async () => {
        const state = await environment.client.getFlowState(id);
        return state.activeStepExecutions[0]?.lastFailure;
      }, `Worker failure from the leaking ${leaking.name}`);
      const rawState = await rawCall<GetFlowStateResponse>((service, callback) =>
        service.getFlowState({ flowId: id, runId: "" }, callback));
      assert.ok(!Buffer.from(GetFlowStateResponse.encode(rawState).finish()).includes(Buffer.from(leakSentinel)));
      assert.ok(!(await rawHistoryBytes(id)).includes(Buffer.from(leakSentinel)));
      assert.match(rawState.activeStepExecutions[0]?.lastFailureInfo?.details?.originalWorkerErrorDetail ?? "", leaking.category);
      await environment.client.stopFlow(id, { type: StopType.TERMINATE });
    } finally {
      await leakingClient.close();
      await environment.close();
    }
  }));
}

// The Server deduplicates InvokeRPC by request ID alone, so re-encrypted input still replays.
test(
  "an InvokeRPC request ID replayed from a new Client returns the first result after re-encryption",
  async () => {
    const flow = new EncryptedMatchFlow();
    const firstCodec = new EnvelopeRecordingCodec(new AesGcmPayloadCodec(await fileProvider("k1")));
    const secondCodec = new EnvelopeRecordingCodec(new AesGcmPayloadCodec(await fileProvider("k1")));
    await withEncryptedEnvironment([flow], firstCodec, async ({ client }) => {
      const id = flowId("encrypted-request-id-replay");
      await client.startFlow(flow, id, "start");
      const secondClient = new Client(new Registry([flow]), openBlobCache({
        directory: mkdtempSync(join(keyDirectory, "replay-cache-")),
        maxBytes: 1 << 20,
      }), { serverAddress: process.env.DEX_SERVER_ADDRESS ?? "127.0.0.1:8801", payloadCodec: secondCodec });
      try {
        const requestId = flowId("replayed-request");
        firstCodec.envelopes.length = 0;
        const first = await client.invokeRPCWithOptions(flow.countCall, id, "replay", { requestId });
        const second = await secondClient.invokeRPCWithOptions(flow.countCall, id, "replay", { requestId });
        assert.equal(first, "replay-1");
        assert.equal(second, "replay-1", "the replay returns the first result");
        const [firstInput] = firstCodec.envelopes;
        const [secondInput] = secondCodec.envelopes;
        assert.ok(firstInput !== undefined && secondInput !== undefined);
        assert.notDeepEqual(Buffer.from(firstInput.data), Buffer.from(secondInput.data), "each Client uses a fresh nonce");
        assert.notDeepEqual(
          Buffer.from(firstInput.data.subarray(0, 12)),
          Buffer.from(secondInput.data.subarray(0, 12)),
          "the nonces differ",
        );
        const [count] = await client.getAttributes(id, flow.callCount);
        assert.equal(count, 1n, "the handler ran once");
        await client.stopFlow(id, { type: StopType.TERMINATE });
      } finally {
        await secondClient.close();
      }
    });
  },
);

/** Records every envelope a codec produces, so tests can compare nonces and ciphertext. */
class EnvelopeRecordingCodec implements PayloadCodec {
  public readonly envelopes: PayloadEnvelope[] = [];

  public constructor(private readonly inner: PayloadCodec) {}

  public async encode(context: PayloadContext, plaintexts: readonly Uint8Array[]): Promise<PayloadEnvelope[]> {
    const envelopes = await this.inner.encode(context, plaintexts);
    this.envelopes.push(...envelopes);
    return envelopes;
  }

  public decode(
    context: PayloadContext,
    envelopes: readonly PayloadEnvelope[],
    options?: PayloadCodecDecodeOptions,
  ): Promise<Uint8Array[]> {
    return this.inner.decode(context, envelopes, options);
  }
}
