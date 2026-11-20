// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import { chmodSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";
import { inspect } from "node:util";

import {
  AesGcmPayloadCodec,
  FilePayloadEncryptionKeyProvider,
  PayloadCompression,
  PayloadEncryptionError,
  type PayloadCodec,
  type PayloadCodecDecodeOptions,
  type PayloadContext,
  type PayloadEnvelope,
} from "../src/index.js";
import {
  ErrorSubStatus as ProtoErrorSubStatus,
  PayloadEnvelope as PayloadEnvelopeMessage,
  ServiceErrorResponse,
  Value,
  WorkerErrorResponse,
} from "../src/gen/dex.js";
import { NullValue } from "../src/gen/google/protobuf/struct.js";
import {
  PayloadEncryption,
  payloadEncryptionSettings,
  payloadErrorTextForServer,
  payloadWorkerErrorResponse,
  type PayloadEncryptionSettings,
} from "../src/payload-value-encryption.js";
import { decodeValue, encodeUnknown, ValueHydrator } from "../src/value-mapper.js";
import { WorkerDispatcher } from "../src/worker-dispatcher.js";
import { Registry, type BlobCache } from "../src/index.js";
import { stringCodec } from "../src/codec.js";
import { ValueMappingError, WorkerInvocationError } from "../src/errors.js";
import { PullWorker } from "../src/pull-worker.js";
import { translateServiceErrorOpeningText } from "../src/grpc-status.js";
import { BinaryWriter } from "@bufbuild/protobuf/wire";
import { Metadata, status, type ServiceError } from "@grpc/grpc-js";

interface VectorLimits {
  readonly maxEnvelopeBytes: number;
  readonly maxEncodedTextBytes: number;
  readonly maxDataBytes: number;
  readonly maxPlaintextBytes: number;
}

interface VectorCase {
  readonly name: string;
  readonly input: { readonly value?: Record<string, unknown>; readonly text?: string };
  readonly expectedPlaintext?: string;
  readonly expectedValue?: Record<string, unknown>;
  readonly expectedText?: string;
  readonly expectedError?: string;
  readonly limits?: VectorLimits;
}

interface Vectors {
  readonly keyFile: unknown;
  readonly limits: VectorLimits;
  readonly passthrough: readonly VectorCase[];
  readonly positive: readonly VectorCase[];
  readonly negative: readonly VectorCase[];
}

const vectorsPath = join(
  dirname(fileURLToPath(import.meta.url)),
  "../../../protos/conformance/payload-encryption/vectors.json",
);
const vectors = JSON.parse(readFileSync(vectorsPath, "utf8")) as Vectors;
const flow = { flowType: "VectorFlow", flowId: "vector-flow-1" };
const context: PayloadContext = flow;

async function withKeyFile<T>(contents: unknown, mode: number, run: (path: string) => Promise<T>): Promise<T> {
  const directory = mkdtempSync(join(tmpdir(), "dex-payload-keys-"));
  const path = join(directory, "keys.json");
  writeFileSync(path, JSON.stringify(contents));
  chmodSync(path, mode);
  try {
    return await run(path);
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
}

const keyProvider = await withKeyFile(vectors.keyFile, 0o600, (path) => FilePayloadEncryptionKeyProvider.fromFile(path));

class RecordingCodec implements PayloadCodec {
  public readonly plaintexts: Uint8Array[] = [];

  public constructor(private readonly inner: PayloadCodec) {}

  public encode(payloadContext: PayloadContext, plaintexts: readonly Uint8Array[]): Promise<PayloadEnvelope[]> {
    return this.inner.encode(payloadContext, plaintexts);
  }

  public async decode(
    payloadContext: PayloadContext,
    envelopes: readonly PayloadEnvelope[],
    options?: PayloadCodecDecodeOptions,
  ): Promise<Uint8Array[]> {
    assert.deepEqual(payloadContext, context);
    const plaintexts = await this.inner.decode(payloadContext, envelopes, options);
    this.plaintexts.push(...plaintexts);
    return plaintexts;
  }
}

function encryptionFor(limits: VectorLimits, codec: PayloadCodec | undefined): PayloadEncryption {
  const settings: PayloadEncryptionSettings = {
    ...payloadEncryptionSettings(codec === undefined ? {} : { payloadCodec: codec }),
    maxEnvelopeBytes: limits.maxEnvelopeBytes,
    maxEncodedTextBytes: limits.maxEncodedTextBytes,
    maxPlaintextBytes: limits.maxPlaintextBytes,
  };
  return new PayloadEncryption(settings);
}

function vectorCodec(limits: VectorLimits): RecordingCodec {
  return new RecordingCodec(new AesGcmPayloadCodec(keyProvider, {
    maxDataBytes: limits.maxDataBytes,
    maxDecompressedBytes: limits.maxPlaintextBytes,
  }));
}

function valueFromJson(json: Record<string, unknown>): Value {
  const [[field, raw]] = Object.entries(json) as [[string, unknown]];
  switch (field) {
    case "stringValue":
      return Value.create({ kind: { $case: "stringValue", value: raw as string } });
    case "boolValue":
      return Value.create({ kind: { $case: "boolValue", value: raw as boolean } });
    case "intValue":
      return Value.create({ kind: { $case: "intValue", value: BigInt(raw as string) } });
    case "doubleValue":
      return Value.create({ kind: { $case: "doubleValue", value: Number(raw) } });
    case "nullValue":
      return Value.create({ kind: { $case: "nullValue", value: NullValue.NULL_VALUE } });
    case "objValue": {
      const object = raw as { encoding?: string; payload?: string };
      return Value.create({
        kind: {
          $case: "objValue",
          value: { encoding: object.encoding ?? "", payload: new Uint8Array(Buffer.from(object.payload ?? "", "base64")) },
        },
      });
    }
    default:
      throw new Error(`unsupported vector Value field ${field}`);
  }
}

function assertSameValue(actual: Value, expected: Value, name: string): void {
  assert.equal(actual.kind?.$case, expected.kind?.$case, name);
  if (actual.kind?.$case === "doubleValue" && expected.kind?.$case === "doubleValue") {
    assert.ok(Object.is(actual.kind.value, expected.kind.value), `${name}: double ${actual.kind.value}`);
    return;
  }
  if (actual.kind?.$case === "objValue" && expected.kind?.$case === "objValue") {
    assert.equal(actual.kind.value.encoding, expected.kind.value.encoding, name);
    assert.deepEqual(Buffer.from(actual.kind.value.payload), Buffer.from(expected.kind.value.payload), name);
    return;
  }
  assert.deepEqual(actual, expected, name);
}

async function decodeCase(vector: VectorCase, codec: PayloadCodec | undefined): Promise<Value | string | undefined> {
  const encryption = encryptionFor(vector.limits ?? vectors.limits, codec);
  const batch = encryption.createOpenBatch();
  let result: Value | string | undefined;
  if (vector.input.text !== undefined) {
    batch.openText({ ...flow }, vector.input.text, (opened) => {
      result = opened;
    });
  } else {
    batch.openValue({ ...flow }, valueFromJson(vector.input.value!), (opened) => {
      result = opened;
    });
  }
  await batch.run();
  return result;
}

test("payload encryption golden vectors decode every positive case", async () => {
  for (const vector of vectors.positive) {
    const codec = vectorCodec(vector.limits ?? vectors.limits);
    const decoded = await decodeCase(vector, codec);
    assert.deepEqual(codec.plaintexts, [new Uint8Array(Buffer.from(vector.expectedPlaintext!, "base64"))], vector.name);
    if (vector.expectedText !== undefined) {
      assert.equal(decoded, vector.expectedText, vector.name);
    } else {
      assert.ok(decoded instanceof Object, vector.name);
      assertSameValue(decoded as Value, valueFromJson(vector.expectedValue!), vector.name);
    }
  }
});

test("payload encryption golden vectors pass every non-envelope through", async () => {
  for (const vector of vectors.passthrough) {
    assert.equal(await decodeCase(vector, vectorCodec(vectors.limits)), undefined, vector.name);
    assert.equal(await decodeCase(vector, undefined), undefined, vector.name);
  }
});

test("payload encryption golden vectors reject every negative case with its category", async () => {
  for (const vector of vectors.negative) {
    await assert.rejects(
      decodeCase(vector, vectorCodec(vector.limits ?? vectors.limits)),
      (failure) => {
        assert.ok(failure instanceof PayloadEncryptionError, `${vector.name}: ${String(failure)}`);
        assert.equal(failure.category, vector.expectedError, `${vector.name}: ${failure.message}`);
        return true;
      },
    );
  }
});

test("AesGcmPayloadCodec round trips every kind, with and without gzip", async () => {
  for (const compression of [PayloadCompression.PAYLOAD_COMPRESSION_NONE, PayloadCompression.PAYLOAD_COMPRESSION_GZIP]) {
    const encryption = new PayloadEncryption(
      payloadEncryptionSettings({ payloadCodec: new AesGcmPayloadCodec(keyProvider, { compression }) }),
    );
    const values = [
      encodeUnknown("x".repeat(500)),
      encodeUnknown({ order: "A-1", lines: [1, 2, 3] }),
      encodeUnknown(7n),
      encodeUnknown(-0),
      encodeUnknown(false),
      encodeUnknown(new Uint8Array([0, 1, 255])),
    ];
    const sealed = [...values];
    const seal = encryption.createSealBatch();
    values.forEach((value, index) => seal.sealValue({ flowId: flow.flowId, flowType: undefined }, value, (result) => {
      sealed[index] = result;
    }));
    await seal.run();
    for (const value of sealed) {
      assert.equal(value.kind?.$case, "objValue");
      assert.equal((value.kind as { value: { encoding: string } }).value.encoding, "dex/1");
    }
    const opened = [...sealed];
    const open = encryption.createOpenBatch();
    sealed.forEach((value, index) => open.openValue({ ...flow }, value, (result) => {
      opened[index] = result;
    }));
    await open.run();
    opened.forEach((value, index) => assertSameValue(value, values[index]!, `value ${index}`));
  }
});

test("sealing keeps null and switched-off kinds in clear and reserves dex/ encodings", async () => {
  const encryption = new PayloadEncryption(
    payloadEncryptionSettings({
      payloadCodec: new AesGcmPayloadCodec(keyProvider),
      encryptStringValues: false,
      encryptPrimitiveValues: false,
    }),
  );
  const batch = encryption.createSealBatch();
  for (const value of [encodeUnknown(null), encodeUnknown("text"), encodeUnknown(3n), encodeUnknown(true)]) {
    batch.sealValue({ ...flow }, value, () => assert.fail("value must stay in clear"));
  }
  await batch.run();
  assert.throws(
    () => batch.sealValue(
      { ...flow },
      Value.create({ kind: { $case: "objValue", value: { encoding: "dex/1", payload: new Uint8Array() } } }),
      () => undefined,
    ),
    (failure) => failure instanceof PayloadEncryptionError && failure.category === "invalid_plaintext",
  );
});

test("text writers never emit plaintext that starts with the prefix", async () => {
  const withoutCodec = new PayloadEncryption(payloadEncryptionSettings({}));
  const plain = withoutCodec.createSealBatch();
  const results: string[] = [];
  plain.sealText({ ...flow }, "dex-encoded:looks encrypted", true, (text) => results.push(text));
  plain.sealText({ ...flow }, "ordinary", true, (text) => results.push(text));
  await plain.run();
  assert.deepEqual(results, [" dex-encoded:looks encrypted"]);

  const withCodec = new PayloadEncryption(
    payloadEncryptionSettings({ payloadCodec: new AesGcmPayloadCodec(keyProvider), encryptErrorText: false }),
  );
  const sealed: string[] = [];
  const batch = withCodec.createSealBatch();
  batch.sealText({ ...flow }, "", true, () => assert.fail("empty text stays empty"));
  batch.sealText({ ...flow }, "ordinary", false, () => assert.fail("text encryption is off"));
  batch.sealText({ ...flow }, "dex-encoded:collides", false, (text) => sealed.push(text));
  await batch.run();
  assert.equal(sealed.length, 1);
  assert.match(sealed[0]!, /^dex-encoded:[A-Za-z0-9+/]+=*$/);
  const opened: string[] = [];
  const open = withCodec.createOpenBatch();
  open.openText({ ...flow }, sealed[0]!, (text) => opened.push(text));
  await open.run();
  assert.deepEqual(opened, ["dex-encoded:collides"]);

  const missing = withoutCodec.createOpenBatch();
  missing.openText({ ...flow }, sealed[0]!, () => assert.fail("no codec"));
  await assert.rejects(missing.run(), /value is encrypted; configure a PayloadCodec/);
});

test("typed decoding of an encrypted Value without a codec names the missing codec", async () => {
  const encryption = new PayloadEncryption(
    payloadEncryptionSettings({ payloadCodec: new AesGcmPayloadCodec(keyProvider) }),
  );
  const sealed = await encryption.sealValue({ ...flow }, encodeUnknown("secret text"));
  const withoutCodec = new PayloadEncryption(payloadEncryptionSettings({})).createOpenBatch();
  assert.throws(
    () => withoutCodec.openValue({ ...flow }, sealed, () => assert.fail("no codec")),
    (failure) => failure instanceof PayloadEncryptionError && failure.category === "codec_not_configured",
  );
  withoutCodec.openValue({ ...flow }, encodeUnknown("plain"), () => assert.fail("plaintext passes through"));
  await withoutCodec.run();
  assert.throws(
    () => decodeValue(stringCodec, sealed!),
    (failure) => failure instanceof PayloadEncryptionError
      && failure.category === "codec_not_configured"
      && failure.message.includes("value is encrypted; configure a PayloadCodec")
      && !failure.message.includes("secret"),
  );
});

test("key provider failures never reach the error text, cause, or inspection output", async () => {
  const sentinel = "KEY_MATERIAL_SENTINEL";
  const leakingProviders = [
    {
      async currentKeyId(): Promise<string> {
        throw new Error(sentinel);
      },
      async key(): Promise<Uint8Array> {
        throw new Error(sentinel);
      },
    },
    {
      async currentKeyId(): Promise<string> {
        return "k1";
      },
      async key(): Promise<Uint8Array> {
        throw new PayloadEncryptionError("decode", "malformed", sentinel, "k1", { cause: new Error(sentinel) });
      },
    },
  ];
  const envelope = (await new AesGcmPayloadCodec(keyProvider).encode(context, [new Uint8Array([1, 2])]))[0]!;
  for (const provider of leakingProviders) {
    const codec = new AesGcmPayloadCodec(provider);
    for (const operation of [
      () => codec.encode(context, [new Uint8Array([1])]),
      () => codec.decode(context, [{ ...envelope, keyId: "k1" }]),
    ]) {
      await assert.rejects(operation(), (failure) => {
        assert.ok(failure instanceof PayloadEncryptionError);
        assert.equal(failure.category, "unknown_key");
        assert.equal(failure.cause, undefined);
        assert.ok(!failure.message.includes(context.flowId), failure.message);
        assert.ok(!inspect(failure, { depth: 10 }).includes(sentinel));
        return true;
      });
    }
  }
});

test("custom codec failures are replaced by sanitized SDK errors", async () => {
  const sentinel = "KEY_MATERIAL_SENTINEL";
  const realCodec = new AesGcmPayloadCodec(keyProvider);
  const sealer = new PayloadEncryption(payloadEncryptionSettings({ payloadCodec: realCodec }));
  const sealed = await sealer.sealValue({ ...flow }, encodeUnknown("payload"));
  const failures: { readonly failure: () => Error; readonly category: string; readonly keyId: string | undefined }[] = [
    { failure: () => Object.assign(new Error(sentinel), { secret: sentinel }), category: "codec_failed", keyId: undefined },
    {
      failure: () => new PayloadEncryptionError("decode", sentinel as never, sentinel, sentinel, { cause: new Error(sentinel) }),
      category: "codec_failed",
      keyId: undefined,
    },
    {
      failure: () => new PayloadEncryptionError("decode", "authentication_failed", sentinel, "k2", { cause: new Error(sentinel) }),
      category: "authentication_failed",
      keyId: "k2",
    },
  ];
  for (const { failure, category, keyId } of failures) {
    const codec: PayloadCodec = {
      async encode(): Promise<PayloadEnvelope[]> {
        throw failure();
      },
      async decode(): Promise<Uint8Array[]> {
        throw failure();
      },
    };
    const encryption = new PayloadEncryption(payloadEncryptionSettings({ payloadCodec: codec }));
    const checks: { readonly run: () => Promise<unknown>; readonly keyId: string | undefined }[] = [
      { run: () => encryption.sealValue({ ...flow }, encodeUnknown("payload")), keyId: undefined },
      {
        run: async () => {
          const batch = encryption.createOpenBatch();
          batch.openValue({ ...flow }, sealed, () => undefined);
          await batch.run();
        },
        keyId: keyId === "k2" ? "k2" : undefined,
      },
    ];
    for (const check of checks) {
      await assert.rejects(check.run(), (error) => {
        assert.ok(error instanceof PayloadEncryptionError);
        assert.equal(error.category, category);
        assert.equal(error.cause, undefined);
        assert.equal(error.keyId, check.keyId, "a key ID survives only when it names a decoded envelope");
        assert.ok(!inspect(error, { depth: 10, showHidden: true }).includes(sentinel));
        const serverText = payloadErrorTextForServer(error);
        assert.ok(!serverText.includes(sentinel), serverText);
        assert.equal(serverText.includes("key ID k2"), check.keyId === "k2", "an SDK error keeps its trusted key ID");
        return true;
      });
    }
  }
  const handlerError = new PayloadEncryptionError("decode", "unknown_key", sentinel, sentinel, { cause: new Error(sentinel) });
  const serverText = payloadErrorTextForServer(handlerError);
  assert.ok(!serverText.includes(sentinel), serverText);
  assert.match(serverText, /codec_failed/, "a handler-built error has no SDK snapshot, so only a fixed category is sent");
});

/** Builds errors whose every property read can throw the sentinel, as a hostile codec or handler could. */
function hostileErrors(sentinel: string): (() => unknown)[] {
  const throwSentinel = (): never => {
    throw new Error(sentinel);
  };
  return [
    () => {
      const error = new PayloadEncryptionError("decode", "unknown_key", sentinel, "k1", { cause: new Error(sentinel) });
      error.name = sentinel;
      return error;
    },
    () => {
      const error = new PayloadEncryptionError("decode", "unknown_key", sentinel, "k1");
      for (const property of ["category", "keyId", "name", "message", "stack", "operation", "cause"]) {
        Object.defineProperty(error, property, { get: throwSentinel, configurable: true });
      }
      return error;
    },
    () => new Proxy(new PayloadEncryptionError("decode", "unknown_key", sentinel, "k1"), {
      get: throwSentinel,
      has: throwSentinel,
      ownKeys: throwSentinel,
      getOwnPropertyDescriptor: throwSentinel,
    }),
    () => new Proxy(new PayloadEncryptionError("decode", "unknown_key", sentinel, "k1"), {
      getPrototypeOf: throwSentinel,
      get: throwSentinel,
    }),
    () => {
      const error = new Error(sentinel);
      Object.defineProperty(error, "message", { get: throwSentinel, configurable: true });
      return error;
    },
  ];
}

async function captureConsole<T>(run: () => Promise<T>): Promise<{ result: T | undefined; output: string }> {
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
  console.error = record;
  console.warn = record;
  console.log = record;
  console.info = record;
  try {
    return { result: await run(), output: lines.join("\n") };
  } finally {
    Object.assign(console, originals);
  }
}

test("hostile codec errors and outputs never escape sanitization", async () => {
  const sentinel = "KEY_MATERIAL_SENTINEL";
  const realCodec = new AesGcmPayloadCodec(keyProvider);
  const sealer = new PayloadEncryption(payloadEncryptionSettings({ payloadCodec: realCodec }));
  const sealed = await sealer.sealValue({ ...flow }, encodeUnknown("payload"));
  const throwSentinel = (): never => {
    throw new Error(sentinel);
  };
  const codecs: PayloadCodec[] = hostileErrors(sentinel).map((failure) => ({
    async encode(): Promise<PayloadEnvelope[]> {
      throw failure();
    },
    async decode(): Promise<Uint8Array[]> {
      throw failure();
    },
  }));
  codecs.push({
    async encode(): Promise<PayloadEnvelope[]> {
      return [Object.defineProperty({}, "keyId", { get: throwSentinel }) as PayloadEnvelope];
    },
    async decode(): Promise<Uint8Array[]> {
      return new Proxy([new Uint8Array([1])], { get: throwSentinel });
    },
  });
  const { output } = await captureConsole(async () => {
    for (const codec of codecs) {
      const encryption = new PayloadEncryption(payloadEncryptionSettings({ payloadCodec: codec }));
      const runs = [
        () => encryption.sealValue({ ...flow }, encodeUnknown("payload")),
        async () => {
          const batch = encryption.createOpenBatch();
          batch.openValue({ ...flow }, sealed, () => undefined);
          await batch.run();
        },
      ];
      for (const run of runs) {
        await assert.rejects(run(), (error) => {
          assert.ok(error instanceof PayloadEncryptionError, String(error));
          assert.equal(error.cause, undefined);
          assert.ok(!inspect(error, { depth: 10, showHidden: true }).includes(sentinel));
          return true;
        });
      }
    }
  });
  assert.ok(!output.includes(sentinel), output);
});

test("a codec that relabels the envelopes it decodes cannot change the reported key ID", async () => {
  const sentinel = "KEY_MATERIAL_SENTINEL";
  const realCodec = new AesGcmPayloadCodec(keyProvider);
  const sealer = new PayloadEncryption(payloadEncryptionSettings({ payloadCodec: realCodec }));
  const sealed = await sealer.sealValue({ ...flow }, encodeUnknown("payload"));
  const oversized = new Uint8Array(Value.encode(encodeUnknown("y".repeat(2048))).finish());
  const outcomes: {
    readonly result: () => Uint8Array[] | never;
    readonly category: string;
    readonly keyId: string | undefined;
  }[] = [
    {
      result: () => {
        throw new PayloadEncryptionError("decode", "authentication_failed", sentinel, sentinel);
      },
      category: "authentication_failed",
      keyId: undefined,
    },
    { result: () => [new Uint8Array([0xff, 0xff, 0xff])], category: "invalid_plaintext", keyId: "k2" },
    { result: () => [oversized], category: "limit_exceeded", keyId: "k2" },
  ];
  const { output } = await captureConsole(async () => {
    for (const outcome of outcomes) {
      const relabeling: PayloadCodec = {
        encode: (payloadContext, plaintexts) => realCodec.encode(payloadContext, plaintexts),
        async decode(_payloadContext, envelopes): Promise<Uint8Array[]> {
          for (const envelope of envelopes) {
            (envelope as { keyId: string }).keyId = sentinel;
          }
          return outcome.result();
        },
      };
      const encryption = new PayloadEncryption(payloadEncryptionSettings({
        payloadCodec: relabeling,
        maxPayloadPlaintextBytes: 1024,
      }));
      const batch = encryption.createOpenBatch();
      batch.openValue({ ...flow }, sealed, () => undefined);
      await assert.rejects(batch.run(), (error) => {
        assert.ok(error instanceof PayloadEncryptionError);
        assert.equal(error.category, outcome.category);
        assert.equal(error.keyId, outcome.keyId, "only the key ID validated before the codec call is reported");
        assert.ok(!inspect(error, { depth: 10, showHidden: true }).includes(sentinel));
        assert.ok(!payloadErrorTextForServer(error).includes(sentinel));
        return true;
      });
    }
  });
  assert.ok(!output.includes(sentinel), output);
});

test("codec results are copied without species, overridden methods, or getters", async () => {
  const sentinel = "KEY_MATERIAL_SENTINEL";
  const realCodec = new AesGcmPayloadCodec(keyProvider);
  const sealer = new PayloadEncryption(payloadEncryptionSettings({ payloadCodec: realCodec }));
  const sealed = await sealer.sealValue({ ...flow }, encodeUnknown("payload"));
  class SpeciesBytes extends Uint8Array {
    static get [Symbol.species](): typeof SpeciesBytes {
      return SpeciesBytes;
    }

    override get length(): number {
      throw new Error(sentinel);
    }
  }
  class ForgedErrorBytes extends Uint8Array {
    static get [Symbol.species](): typeof ForgedErrorBytes {
      return ForgedErrorBytes;
    }

    override get length(): number {
      throw new PayloadEncryptionError("decode", "malformed", sentinel, "FORGED-KEYID");
    }
  }
  class ShortLengthBytes extends Uint8Array {
    override get length(): number {
      return 1;
    }
  }
  class MapOverridingArray<T> extends Array<T> {
    override map<U>(): U[] {
      throw new Error(sentinel);
    }
  }
  const wrap = <T extends Uint8Array>(Kind: new (length: number) => T, bytes: Uint8Array): T => {
    const copy = new Kind(bytes.length);
    copy.set(bytes);
    return copy;
  };
  const decodeWith = async (transform: (plaintexts: Uint8Array[]) => unknown, limit?: number) => {
    const codec: PayloadCodec = {
      encode: (payloadContext, plaintexts) => realCodec.encode(payloadContext, plaintexts),
      async decode(payloadContext, envelopes, options): Promise<Uint8Array[]> {
        return transform(await realCodec.decode(payloadContext, envelopes, options)) as Uint8Array[];
      },
    };
    const encryption = new PayloadEncryption(payloadEncryptionSettings({
      payloadCodec: codec,
      ...(limit === undefined ? {} : { maxPayloadPlaintextBytes: limit }),
    }));
    let opened: Value | undefined;
    const batch = encryption.createOpenBatch();
    batch.openValue({ ...flow }, sealed, (value) => {
      opened = value;
    });
    await batch.run();
    return opened;
  };
  const { output } = await captureConsole(async () => {
    for (const Kind of [SpeciesBytes, ForgedErrorBytes]) {
      const opened = await decodeWith((plaintexts) => plaintexts.map((plaintext) => wrap(Kind, plaintext)));
      assert.equal(decodeValue(stringCodec, opened!), "payload", "the copy ignores the subclass getters");
    }
    const mapped = await decodeWith((plaintexts) => {
      const array = new MapOverridingArray<Uint8Array>();
      array.push(...plaintexts);
      return array;
    });
    assert.equal(decodeValue(stringCodec, mapped!), "payload", "the copy never calls the returned array's map");
    const large = await sealer.sealValue({ ...flow }, encodeUnknown("z".repeat(4096)));
    const largeCodec: PayloadCodec = {
      encode: (payloadContext, plaintexts) => realCodec.encode(payloadContext, plaintexts),
      async decode(payloadContext, envelopes, options): Promise<Uint8Array[]> {
        const plaintexts = await realCodec.decode(payloadContext, envelopes, { maxPlaintextBytes: 1 << 20 });
        void options;
        return plaintexts.map((plaintext) => wrap(ShortLengthBytes, plaintext));
      },
    };
    const limited = new PayloadEncryption(payloadEncryptionSettings({ payloadCodec: largeCodec, maxPayloadPlaintextBytes: 1024 }));
    const batch = limited.createOpenBatch();
    batch.openValue({ ...flow }, large, () => assert.fail("a forged length cannot bypass the size check"));
    await assert.rejects(batch.run(), (error) => error instanceof PayloadEncryptionError && error.category === "limit_exceeded");

    for (const transform of [
      (envelopes: PayloadEnvelope[]) => envelopes.map((envelope) => ({ ...envelope, data: wrap(SpeciesBytes, envelope.data) })),
      (envelopes: PayloadEnvelope[]) => {
        const array = new MapOverridingArray<PayloadEnvelope>();
        array.push(...envelopes);
        return array;
      },
    ]) {
      const encodeCodec: PayloadCodec = {
        async encode(payloadContext, plaintexts): Promise<PayloadEnvelope[]> {
          return transform(await realCodec.encode(payloadContext, plaintexts));
        },
        decode: (payloadContext, envelopes, options) => realCodec.decode(payloadContext, envelopes, options),
      };
      const encoder = new PayloadEncryption(payloadEncryptionSettings({ payloadCodec: encodeCodec }));
      const encoded = await encoder.sealValue({ ...flow }, encodeUnknown("x"));
      assert.equal(encoded?.kind?.$case, "objValue", "hostile encode results are copied safely");
    }
    const fractionalCodec: PayloadCodec = {
      async encode(payloadContext, plaintexts): Promise<PayloadEnvelope[]> {
        return (await realCodec.encode(payloadContext, plaintexts))
          .map((envelope) => ({ ...envelope, cipher: 1.5 }) as unknown as PayloadEnvelope);
      },
      decode: (payloadContext, envelopes, options) => realCodec.decode(payloadContext, envelopes, options),
    };
    await assert.rejects(
      new PayloadEncryption(payloadEncryptionSettings({ payloadCodec: fractionalCodec })).sealValue({ ...flow }, encodeUnknown("x")),
      (error) => error instanceof PayloadEncryptionError && error.category === "codec_failed",
      "a non-int32 cipher is rejected",
    );
  });
  assert.ok(!output.includes(sentinel), output);
});

test("Worker failure text uses the snapshot taken when the SDK built the error", async () => {
  const refusingProvider = {
    async currentKeyId(): Promise<string> {
      return "k1";
    },
    async key(): Promise<Uint8Array> {
      throw new Error("refused");
    },
  };
  const sealer = new PayloadEncryption(payloadEncryptionSettings({ payloadCodec: new AesGcmPayloadCodec(keyProvider) }));
  const sealed = await sealer.sealValue({ ...flow }, encodeUnknown("payload"));
  const encryption = new PayloadEncryption(payloadEncryptionSettings({ payloadCodec: new AesGcmPayloadCodec(refusingProvider) }));
  const batch = encryption.createOpenBatch();
  batch.openValue({ ...flow }, sealed, () => undefined);
  const error = await batch.run().then(() => assert.fail("decryption must fail"), (failure: unknown) => failure);
  assert.ok(error instanceof PayloadEncryptionError);
  const before = payloadErrorTextForServer(error);
  assert.match(before, /unknown_key, key ID k2/);
  Object.assign(error, { keyId: "LEAKED-APPLICATION-DATA", category: "malformed", operation: "encode" });
  assert.equal(payloadErrorTextForServer(error), before, "mutating a trusted error changes nothing that is sent");
  const forged = new PayloadEncryptionError("decode", "unknown_key", "handler text", "HANDLER-KEY");
  assert.ok(!payloadErrorTextForServer(forged).includes("HANDLER-KEY"));
  assert.match(payloadErrorTextForServer(forged), /codec_failed/);
});

test("direct callers of the built-in codec never see an invalid key ID in error text", async () => {
  const hostileKeyId = `bad\nkey ${"x".repeat(80)}`;
  const codec = new AesGcmPayloadCodec(keyProvider);
  const envelope = (await codec.encode(context, [new Uint8Array([1])]))[0]!;
  for (const changed of [
    { ...envelope, keyId: hostileKeyId, cipher: 2 },
    { ...envelope, keyId: hostileKeyId, compression: 9 },
    { ...envelope, keyId: hostileKeyId },
  ]) {
    await assert.rejects(codec.decode(context, [changed]), (error) => {
      assert.ok(error instanceof PayloadEncryptionError);
      assert.equal(error.keyId, undefined);
      assert.ok(!error.message.includes("bad\nkey"), error.message);
      return true;
    });
  }
  await assert.rejects(keyProvider.key(context, hostileKeyId), (error) => {
    assert.ok(error instanceof PayloadEncryptionError);
    assert.equal(error.keyId, undefined);
    assert.ok(!error.message.includes("bad\nkey"), error.message);
    return true;
  });
});

test("encode-direction errors never carry a codec-chosen key ID", async () => {
  const codec: PayloadCodec = {
    async encode(): Promise<PayloadEnvelope[]> {
      return [{ cipher: 0, keyId: "codec-key", data: new Uint8Array(28), compression: 0, metadata: {} } as PayloadEnvelope];
    },
    async decode(): Promise<Uint8Array[]> {
      return [];
    },
  };
  const encryption = new PayloadEncryption(payloadEncryptionSettings({ payloadCodec: codec }));
  await assert.rejects(encryption.sealValue({ ...flow }, encodeUnknown("x")), (error) => {
    assert.ok(error instanceof PayloadEncryptionError);
    assert.equal(error.category, "unsupported_cipher");
    assert.equal(error.keyId, undefined);
    assert.ok(!payloadErrorTextForServer(error).includes("codec-key"));
    return true;
  });
});

test("only the hydration stage's own errors keep their Worker text in clear", async () => {
  const sentinel = "KEY_MATERIAL_SENTINEL";
  const codec = new AesGcmPayloadCodec(keyProvider);
  const encryption = new PayloadEncryption(payloadEncryptionSettings({ payloadCodec: codec }));
  const task = { taskId: "t", request: { $case: "execute", value: { flowType: "T", context: { flowId: "f1" } } } };
  const workerErrorResponseForServer = (
    PullWorker.prototype as unknown as {
      workerErrorResponseForServer(this: unknown, task: unknown, failure: unknown, grpcStatus: number): Promise<WorkerErrorResponse>;
    }
  ).workerErrorResponseForServer;
  const host = {
    payloadEncryption: encryption,
    sealWorkerErrorResponse: (PullWorker.prototype as unknown as { sealWorkerErrorResponse: unknown }).sealWorkerErrorResponse,
  };
  const sentBytes = async (failure: unknown) => Buffer.from(WorkerErrorResponse.encode(
    await workerErrorResponseForServer.call(host, task, failure, 2),
  ).finish());

  const handlerHydrateError = new ValueMappingError("hydrate", `${sentinel} handler text`);
  assert.ok(!(await sentBytes(handlerHydrateError)).includes(Buffer.from(sentinel)), "a handler cannot claim the hydrate exemption");
  const blobValue = Value.create({ kind: { $case: "internalBlobIdForStringValue", value: "b1" } });
  const omittingService = {
    loadBlobs(_request: unknown, callback: (error: null, response: { values: Record<string, Value> }) => void) {
      callback(null, { values: {} });
    },
  } as unknown as ConstructorParameters<typeof ValueHydrator>[0];
  const emptyCache = {
    config: { directory: "/unused", maxBytes: 1 },
    get: () => undefined,
    put: () => true,
    delete: () => undefined,
    deleteAll: () => undefined,
    close: () => undefined,
  } as unknown as BlobCache;
  const failingCache = {
    ...emptyCache,
    get: () => {
      throw new Error(`${sentinel} from app BlobCache`);
    },
  } as unknown as BlobCache;
  const hydrationFailure = async (cache: BlobCache): Promise<unknown> => {
    const dispatcher = new WorkerDispatcher(new Registry([]), new ValueHydrator(omittingService, cache), () => 3, encryption);
    const hydrateValues = (dispatcher as unknown as {
      hydrateValues(flowId: string, values: readonly Value[]): Promise<Value[]>;
    }).hydrateValues.bind(dispatcher);
    return hydrateValues("f1", [blobValue]).then(() => assert.fail("hydration must fail"), (failure: unknown) => failure);
  };
  const stageError = await hydrationFailure(emptyCache);
  assert.ok((await sentBytes(stageError)).includes(Buffer.from("did not hydrate blob")), "the hydrator's own text stays in clear");
  const cacheError = await hydrationFailure(failingCache);
  const cacheBytes = await sentBytes(cacheError);
  assert.ok(!cacheBytes.includes(Buffer.from(sentinel)), "an application BlobCache error never reaches Dex");
  assert.ok(cacheBytes.includes(Buffer.from("the BlobCache failed")), "the SDK reports a fixed BlobCache text");

  const failingEncryption = new PayloadEncryption(payloadEncryptionSettings({
    payloadCodec: {
      async encode(): Promise<PayloadEnvelope[]> {
        const error = new PayloadEncryptionError("encode", "malformed", "x");
        Object.defineProperty(error, "category", { get: () => `${sentinel} via category` });
        throw error;
      },
      async decode(): Promise<Uint8Array[]> {
        return [];
      },
    },
  }));
  const { output } = await captureConsole(async () => {
    const response = await workerErrorResponseForServer.call(
      { ...host, payloadEncryption: failingEncryption },
      task,
      new Error("ordinary handler failure"),
      2,
    );
    assert.ok(!Buffer.from(WorkerErrorResponse.encode(response).finish()).includes(Buffer.from(sentinel)));
    assert.ok(!response.detail.includes("ordinary handler failure"), "unencryptable text is never sent in clear");
  });
  assert.ok(!output.includes(sentinel), output);
});

test("Client errors decode dex-encoded text only in statuses that carry Dex details", async () => {
  const encryption = new PayloadEncryption(payloadEncryptionSettings({ payloadCodec: new AesGcmPayloadCodec(keyProvider) }));
  const sealedText = async (text: string): Promise<string> => {
    let sealed = text;
    const batch = encryption.createSealBatch();
    batch.sealText({ ...flow }, text, true, (value) => {
      sealed = value;
    });
    await batch.run();
    return sealed;
  };
  const statusWithDexDetails = (
    code: status,
    statusMessage: string,
    response: Partial<ServiceErrorResponse>,
  ): ServiceError => {
    const encoded = ServiceErrorResponse.encode({
      detail: "",
      subStatus: ProtoErrorSubStatus.ERROR_SUB_STATUS_UNCATEGORIZED,
      originalWorkerErrorDetail: "",
      originalWorkerErrorType: "",
      originalWorkerErrorStatus: 0,
      originalWorkerErrorStackTrace: "",
      dispatchTaskErrorReason: 0,
      ...response,
    }).finish();
    const writer = new BinaryWriter();
    writer.uint32(26).fork();
    writer.uint32(10).string("type.googleapis.com/dex.ServiceErrorResponse");
    writer.uint32(18).bytes(encoded);
    writer.join();
    const metadata = new Metadata();
    metadata.set("grpc-status-details-bin", Buffer.from(writer.finish()));
    return Object.assign(new Error(`${code} ${statusMessage}`), { code, details: statusMessage, metadata });
  };
  const target = { operation: "invokeRPC", flowId: flow.flowId, requirement: "active" as const };

  const detail = await sealedText("payment declined for card 4242");
  const workerDetail = await sealedText("worker said card 4242 declined");
  const failed = await translateServiceErrorOpeningText(statusWithDexDetails(status.INTERNAL, detail, {
    detail,
    subStatus: ProtoErrorSubStatus.ERROR_SUB_STATUS_WORKER_API_ERROR,
    originalWorkerErrorDetail: workerDetail,
  }), target, encryption);
  assert.ok(failed instanceof WorkerInvocationError);
  assert.equal(failed.detail, "payment declined for card 4242");
  assert.equal(failed.message, "payment declined for card 4242");
  assert.equal(failed.workerErrorDetail, "worker said card 4242 declined");

  const statusMessageOnly = await translateServiceErrorOpeningText(
    statusWithDexDetails(status.FAILED_PRECONDITION, detail, {}),
    target,
    encryption,
  );
  assert.equal(statusMessageOnly.detail, "payment declined for card 4242", "the status message of a Dex status decodes too");

  await assert.rejects(
    translateServiceErrorOpeningText(statusWithDexDetails(status.INTERNAL, "dex-encoded:@@@@", { detail: "dex-encoded:@@@@" }), target, encryption),
    (error) => error instanceof PayloadEncryptionError && error.category === "malformed",
    "a malformed prefix is an error, never plaintext",
  );

  const transport = Object.assign(new Error(`14 UNAVAILABLE: ${detail}`), {
    code: status.UNAVAILABLE,
    details: detail,
    metadata: new Metadata(),
  });
  const unchanged = await translateServiceErrorOpeningText(transport, target, encryption);
  assert.equal(unchanged.detail, detail, "a status without Dex details keeps its text unchanged");
});

test("Worker failures for hostile PayloadEncryptionErrors are rebuilt from fixed fields", async () => {
  const sentinel = "KEY_MATERIAL_SENTINEL";
  const { output } = await captureConsole(async () => {
    for (const failure of hostileErrors(sentinel).slice(0, 4)) {
      const error = failure();
      const response = payloadWorkerErrorResponse(error, 2);
      assert.equal(response.errorType, "PayloadEncryptionError");
      assert.equal(response.stackTrace, "");
      assert.equal(response.grpcStatus, 2);
      assert.ok(!Buffer.from(WorkerErrorResponse.encode(response).finish()).includes(Buffer.from(sentinel)));
      assert.ok(!payloadErrorTextForServer(error).includes(sentinel));
    }
  });
  assert.ok(!output.includes(sentinel), output);
});

test("decode passes the SDK plaintext limit to the codec and checks it again afterwards", async () => {
  const realCodec = new AesGcmPayloadCodec(keyProvider, { compression: PayloadCompression.PAYLOAD_COMPRESSION_GZIP });
  const sealer = new PayloadEncryption(payloadEncryptionSettings({ payloadCodec: realCodec }));
  const sealed = await sealer.sealValue({ ...flow }, encodeUnknown("z".repeat(4096)));
  const seenLimits: (number | undefined)[] = [];
  const oversized = new Uint8Array(Value.encode(encodeUnknown("y".repeat(2048))).finish());
  const ignoringLimit: PayloadCodec = {
    encode: (payloadContext, plaintexts) => realCodec.encode(payloadContext, plaintexts),
    async decode(_payloadContext, envelopes, options): Promise<Uint8Array[]> {
      seenLimits.push(options?.maxPlaintextBytes);
      return envelopes.map(() => oversized);
    },
  };
  const limited = new PayloadEncryption(payloadEncryptionSettings({ payloadCodec: ignoringLimit, maxPayloadPlaintextBytes: 1024 }));
  const batch = limited.createOpenBatch();
  batch.openValue({ ...flow }, sealed, () => assert.fail("the SDK rejects an oversized plaintext"));
  await assert.rejects(
    batch.run(),
    (error) => error instanceof PayloadEncryptionError && error.category === "limit_exceeded",
  );
  assert.deepEqual(seenLimits, [1024]);

  const builtIn = new PayloadEncryption(payloadEncryptionSettings({ payloadCodec: realCodec, maxPayloadPlaintextBytes: 1024 }));
  const builtInBatch = builtIn.createOpenBatch();
  builtInBatch.openValue({ ...flow }, sealed, () => assert.fail("gzip stops at the SDK limit"));
  await assert.rejects(
    builtInBatch.run(),
    (error) => error instanceof PayloadEncryptionError && error.category === "limit_exceeded",
  );
  await assert.rejects(
    realCodec.decode(context, [PayloadEnvelopeMessage.decode((sealed!.kind as { value: { payload: Uint8Array } }).value.payload)], {
      maxPlaintextBytes: 1024,
    }),
    (error) => error instanceof PayloadEncryptionError && error.category === "limit_exceeded",
    "the built-in codec stops gzip at the passed limit, below its own default",
  );
});

test("FilePayloadEncryptionKeyProvider rejects invalid key files without key bytes", async () => {
  const key = Buffer.alloc(32, 7).toString("base64");
  const invalidFiles: unknown[] = [
    { current_key_id: "k1", keys: { k1: Buffer.alloc(31, 7).toString("base64") } },
    { current_key_id: "k1", keys: { k1: key.replace(/=+$/, "") } },
    { current_key_id: "k2", keys: { k1: key } },
    { current_key_id: "bad id", keys: { "bad id": key } },
  ];
  for (const contents of invalidFiles) {
    await withKeyFile(contents, 0o600, async (path) => {
      await assert.rejects(FilePayloadEncryptionKeyProvider.fromFile(path), (failure) => {
        assert.ok(failure instanceof Error);
        assert.ok(!failure.message.includes(key.slice(0, 20)), failure.message);
        return true;
      });
    });
  }
  const warnings: string[] = [];
  const originalWarn = console.warn;
  console.warn = (message: string) => warnings.push(message);
  try {
    await withKeyFile({ current_key_id: "k1", keys: { k1: key } }, 0o644, (path) => FilePayloadEncryptionKeyProvider.fromFile(path));
  } finally {
    console.warn = originalWarn;
  }
  if (process.platform !== "win32") {
    assert.equal(warnings.length, 1);
    assert.match(warnings[0]!, /accessible by other users/);
  }
});
