// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { isPlaintextAttributeDefinition } from "./attribute-plaintext.js";
import { PayloadEncryptionError, type PayloadEncryptionErrorCategory } from "./errors.js";
import {
  PayloadCipher,
  PayloadEnvelope,
  Value as ProtoValue,
  WorkerErrorResponse,
  type AttributeWrite,
} from "./gen/dex.js";
import type { PayloadEncryptionOptions } from "./options.js";
import {
  decodeCanonicalBase64,
  defaultMaxPayloadEnvelopeBytes,
  defaultMaxPayloadPlaintextBytes,
  encodedTextPrefix,
  isValidKeyId,
  payloadEnvelopeMarker,
  reservedPayloadEncodingPrefix,
  type PayloadCodec,
  type PayloadContext,
} from "./payload-encryption.js";

const maxMetadataEntries = 16;
const maxMetadataTotalBytes = 4096;
const codecNotConfiguredMessage = "value is encrypted; configure a PayloadCodec";

/** Resolved payload encryption configuration of one Client or Worker. */
export interface PayloadEncryptionSettings {
  readonly codec: PayloadCodec | undefined;
  readonly encryptStringValues: boolean;
  readonly encryptPrimitiveValues: boolean;
  readonly encryptErrorText: boolean;
  readonly maxEnvelopeBytes: number;
  readonly maxEncodedTextBytes: number;
  readonly maxPlaintextBytes: number;
}

/** Names the Flow that stores values; an unknown type becomes the empty string, as in the Go SDK. */
export interface PayloadFlowReference {
  readonly flowId: string;
  readonly flowType: string | undefined;
}

/**
 * Validates Client or Worker options and fills in the defaults.
 * @returns The settings; every switch defaults to encrypt.
 * @throws {RangeError} When a size limit is not a positive safe integer.
 */
export function payloadEncryptionSettings(options: PayloadEncryptionOptions): PayloadEncryptionSettings {
  const maxEnvelopeBytes = options.maxPayloadEnvelopeBytes ?? defaultMaxPayloadEnvelopeBytes;
  const maxPlaintextBytes = options.maxPayloadPlaintextBytes ?? defaultMaxPayloadPlaintextBytes;
  for (const [name, value] of [
    ["maxPayloadEnvelopeBytes", maxEnvelopeBytes],
    ["maxPayloadPlaintextBytes", maxPlaintextBytes],
  ] as const) {
    if (!Number.isSafeInteger(value) || value <= 0) {
      throw new RangeError(`${name} must be a positive safe integer`);
    }
  }
  return {
    codec: options.payloadCodec,
    encryptStringValues: options.encryptStringValues ?? true,
    encryptPrimitiveValues: options.encryptPrimitiveValues ?? true,
    encryptErrorText: options.encryptErrorText ?? true,
    maxEnvelopeBytes,
    maxEncodedTextBytes: 4 * Math.ceil(maxEnvelopeBytes / 3),
    maxPlaintextBytes,
  };
}

/**
 * Reports whether a physical Attribute key belongs to a definition declared Plaintext.
 * @returns Whether the singleton or map definition named by the key's first segment is Plaintext.
 */
export function isPlaintextAttributeKey(persistence: ReadonlyMap<string, object>, key: string): boolean {
  const separator = key.indexOf("/");
  const definition = persistence.get(separator < 0 ? key : key.slice(0, separator));
  return definition !== undefined && isPlaintextAttributeDefinition(definition);
}

/** @returns Encryption without a codec: values pass through and colliding text is space-prefixed. */
export function disabledPayloadEncryption(): PayloadEncryption {
  return new PayloadEncryption(payloadEncryptionSettings({}));
}

/** @returns The deterministic Flow ID of the SubFlow at `index` of a Step execution. */
export function subFlowPayloadFlowId(parentFlowId: string, stepExecutionId: string, index: number): string {
  return `SubFlow:${parentFlowId}-${stepExecutionId}-${index}`;
}

/** Reports whether an object encoding is reserved for Dex. */
export function isReservedPayloadEncoding(encoding: string): boolean {
  return encoding.startsWith(reservedPayloadEncodingPrefix);
}

/** The fields of an SDK-built payload error, frozen when the SDK built it. */
interface SdkPayloadErrorSnapshot {
  readonly operation: "encode" | "decode";
  readonly category: PayloadEncryptionErrorCategory;
  readonly keyId: string | undefined;
}

/** Snapshots of errors the SDK built; later mutations of an error never change what is sent. */
const sdkPayloadErrorSnapshots = new WeakMap<object, SdkPayloadErrorSnapshot>();

/**
 * Builds a payload error and records its fields, the only fields the Worker sends to the Server.
 * @returns The new error.
 */
function sdkPayloadError(
  operation: "encode" | "decode",
  category: PayloadEncryptionErrorCategory,
  detail: string,
  keyId?: string,
): PayloadEncryptionError {
  const shownKeyId = operation === "decode" && keyId !== undefined && isValidKeyId(keyId) ? keyId : undefined;
  const error = new PayloadEncryptionError(operation, category, detail, shownKeyId);
  sdkPayloadErrorSnapshots.set(error, { operation, category, keyId: shownKeyId });
  return error;
}

/** The error for an encrypted value or text that a codec-less SDK cannot read. */
export function codecNotConfiguredError(): PayloadEncryptionError {
  return sdkPayloadError("decode", "codec_not_configured", codecNotConfiguredMessage);
}

/** Hydration errors the SDK constructed itself; only they keep their Dex-matched text in clear. */
const sdkHydrationErrors = new WeakSet<object>();

/**
 * Registers a hydration error at the moment the SDK constructs it; never call it on a caught error.
 * @returns The same failure.
 */
export function markSdkHydrationError(failure: unknown): unknown {
  if (typeof failure === "object" && failure !== null) {
    sdkHydrationErrors.add(failure);
  }
  return failure;
}

/** @returns Whether the Worker's hydration stage raised this failure. */
export function isSdkHydrationError(failure: unknown): boolean {
  return typeof failure === "object" && failure !== null && sdkHydrationErrors.has(failure);
}

/**
 * Encrypts outbound and decrypts inbound Values of one Client or Worker.
 *
 * Typed encoding stays synchronous; callers seal a finished request and open a hydrated one.
 */
export class PayloadEncryption {
  public constructor(public readonly settings: PayloadEncryptionSettings) {}

  public get hasCodec(): boolean {
    return this.settings.codec !== undefined;
  }

  /**
   * Reports whether values of a codec wire kind are encrypted, ignoring index and Plaintext.
   * @returns Whether the SDK encrypts such values; always false without a codec.
   */
  public encryptsWireKind(wireKind: string): boolean {
    if (!this.hasCodec) {
      return false;
    }
    switch (wireKind) {
      case "string":
        return this.settings.encryptStringValues;
      case "bool":
      case "int64":
      case "double":
        return this.settings.encryptPrimitiveValues;
      default:
        return true;
    }
  }

  /**
   * Reports whether writes of an Attribute or AttributeMap definition are encrypted.
   * @returns False for indexed and Plaintext definitions, and without a codec.
   */
  public encryptsAttributeDefinition(definition: {
    readonly index?: unknown;
    readonly codec: { readonly wireKind: string };
  }): boolean {
    return definition.index === undefined
      && !isPlaintextAttributeDefinition(definition)
      && this.encryptsWireKind(definition.codec.wireKind);
  }

  public createSealBatch(): PayloadSealBatch {
    return new PayloadSealBatch(this);
  }

  public createOpenBatch(): PayloadOpenBatch {
    return new PayloadOpenBatch(this);
  }

  /**
   * Seals one Value when encryption applies to it.
   * @returns The envelope Value, or `value` unchanged.
   */
  public async sealValue(flow: PayloadFlowReference, value: ProtoValue | undefined): Promise<ProtoValue | undefined> {
    let sealed = value;
    const batch = this.createSealBatch();
    batch.sealValue(flow, value, (result) => {
      sealed = result;
    });
    await batch.run();
    return sealed;
  }

  /** @returns A context with the known Flow type, or the empty string when only the Flow ID is known. */
  public payloadContext(flow: PayloadFlowReference): PayloadContext {
    return { flowType: flow.flowType ?? "", flowId: flow.flowId };
  }

  /**
   * Decides whether a Value is encrypted on write.
   * @returns Whether the Value's kind is encrypted by the current switches.
   * @throws {@link PayloadEncryptionError} When an application object uses a reserved encoding.
   */
  public shouldEncryptValue(value: ProtoValue | undefined): boolean {
    const kind = value?.kind;
    if (kind === undefined) {
      return false;
    }
    switch (kind.$case) {
      case "objValue":
        if (isReservedPayloadEncoding(kind.value.encoding)) {
          throw sdkPayloadError(
            "encode",
            "invalid_plaintext",
            `object encoding ${JSON.stringify(kind.value.encoding)} is reserved for Dex`,
          );
        }
        return this.hasCodec;
      case "stringValue":
        return this.hasCodec && this.settings.encryptStringValues;
      case "intValue":
      case "doubleValue":
      case "boolValue":
        return this.hasCodec && this.settings.encryptPrimitiveValues;
      default:
        return false;
    }
  }

  /**
   * Encrypts serialized Values for one Flow and wraps each envelope.
   * Encode-direction errors never carry a key ID, because the codec chose it.
   * @returns One serialized envelope per plaintext, in order.
   */
  public async encodeEnvelopes(
    context: PayloadContext,
    plaintexts: readonly Uint8Array[],
  ): Promise<Uint8Array[]> {
    const codec = this.requireCodec("encode");
    for (const plaintext of plaintexts) {
      if (plaintext.length > this.settings.maxPlaintextBytes) {
        throw sdkPayloadError(
          "encode",
          "limit_exceeded",
          `a ${plaintext.length}-byte Value exceeds maxPayloadPlaintextBytes ${this.settings.maxPlaintextBytes}`,
        );
      }
    }
    let returned: unknown;
    try {
      returned = await codec.encode(context, plaintexts.map((plaintext) => new Uint8Array(plaintext)));
    } catch (failure) {
      throw sanitizedCodecError("encode", failure, []);
    }
    const envelopes = copyCodecEnvelopes(returned, plaintexts.length);
    return envelopes.map((envelope) => {
      requireEnvelopeHeader(envelope, "encode");
      if (envelope.cipher === PayloadCipher.PAYLOAD_CIPHER_UNSPECIFIED) {
        throw sdkPayloadError("encode", "unsupported_cipher", "the PayloadCodec returned an envelope without a cipher");
      }
      const serialized = PayloadEnvelope.encode(envelope).finish();
      if (serialized.length > this.settings.maxEnvelopeBytes) {
        throw sdkPayloadError(
          "encode",
          "limit_exceeded",
          `a ${serialized.length}-byte envelope exceeds maxPayloadEnvelopeBytes ${this.settings.maxEnvelopeBytes}`,
        );
      }
      return serialized;
    });
  }

  /**
   * Runs format checks 1 to 6 on an object payload.
   * @returns The parsed envelope.
   */
  public parseObjectEnvelope(encoding: string, payload: Uint8Array): PayloadEnvelope {
    if (encoding !== payloadEnvelopeMarker) {
      throw sdkPayloadError(
        "decode",
        "unsupported_marker",
        `object encoding ${JSON.stringify(encoding)} is not a supported Dex payload format`,
      );
    }
    if (payload.length > this.settings.maxEnvelopeBytes) {
      throw sdkPayloadError(
        "decode",
        "limit_exceeded",
        `a ${payload.length}-byte envelope exceeds maxPayloadEnvelopeBytes ${this.settings.maxEnvelopeBytes}`,
      );
    }
    return parseEnvelope(payload);
  }

  /**
   * Runs format checks 2 to 6 on the base64 text after the `dex-encoded:` prefix.
   * @returns The parsed envelope.
   */
  public parseTextEnvelope(encoded: string): PayloadEnvelope {
    const encodedBytes = Buffer.byteLength(encoded, "utf8");
    if (encodedBytes > this.settings.maxEncodedTextBytes) {
      throw sdkPayloadError(
        "decode",
        "limit_exceeded",
        `a ${encodedBytes}-byte encoded text exceeds the ${this.settings.maxEncodedTextBytes}-byte limit`,
      );
    }
    const payload = decodeCanonicalBase64(encoded);
    if (payload === undefined) {
      throw sdkPayloadError("decode", "malformed", "encoded text is not canonical padded base64");
    }
    if (payload.length > this.settings.maxEnvelopeBytes) {
      throw sdkPayloadError(
        "decode",
        "limit_exceeded",
        `a ${payload.length}-byte envelope exceeds maxPayloadEnvelopeBytes ${this.settings.maxEnvelopeBytes}`,
      );
    }
    return parseEnvelope(payload);
  }

  /**
   * Decrypts envelopes of one Flow and runs checks 13 and 14 on each plaintext.
   * @returns One plaintext Value per envelope, in order.
   */
  public async decodeEnvelopes(
    context: PayloadContext,
    envelopes: readonly PayloadEnvelope[],
    requireStringValues: readonly boolean[],
  ): Promise<ProtoValue[]> {
    const codec = this.requireCodec("decode");
    // The codec gets copies, so its mutations never reach the key IDs the SDK reports.
    const keyIds = envelopes.map((envelope) => envelope.keyId);
    const codecEnvelopes = envelopes.map((envelope) => PayloadEnvelope.decode(PayloadEnvelope.encode(envelope).finish()));
    let returned: unknown;
    try {
      returned = await codec.decode(context, codecEnvelopes, { maxPlaintextBytes: this.settings.maxPlaintextBytes });
    } catch (failure) {
      throw sanitizedCodecError("decode", failure, keyIds);
    }
    const plaintexts = copyCodecPlaintexts(returned, envelopes.length);
    return plaintexts.map((plaintext, index) => {
      const keyId = keyIds[index];
      if (plaintext.length > this.settings.maxPlaintextBytes) {
        throw sdkPayloadError(
          "decode",
          "limit_exceeded",
          `a ${plaintext.length}-byte plaintext exceeds maxPayloadPlaintextBytes ${this.settings.maxPlaintextBytes}`,
          keyId,
        );
      }
      return parsePlaintextValue(plaintext, requireStringValues[index] === true, keyId);
    });
  }

  private requireCodec(operation: "encode" | "decode"): PayloadCodec {
    const codec = this.settings.codec;
    if (codec === undefined) {
      if (operation === "decode") {
        throw codecNotConfiguredError();
      }
      throw sdkPayloadError("encode", "codec_not_configured", "no PayloadCodec is configured");
    }
    return codec;
  }
}

interface PendingSeal {
  readonly plaintext: Uint8Array;
  readonly isText: boolean;
  readonly assignValue: ((sealed: ProtoValue) => void) | undefined;
  readonly assignText: ((sealed: string) => void) | undefined;
}

/** Collects outbound Values and texts and encrypts them with one codec call per Flow. */
export class PayloadSealBatch {
  private readonly groups = new Map<string, { flow: PayloadFlowReference; items: PendingSeal[] }>();

  public constructor(private readonly encryption: PayloadEncryption) {}

  /** Queues a Value; null, blob references, and switched-off kinds keep `value` unchanged. */
  public sealValue(
    flow: PayloadFlowReference,
    value: ProtoValue | undefined,
    assign: (sealed: ProtoValue) => void,
  ): void {
    if (value === undefined || !this.encryption.shouldEncryptValue(value)) {
      return;
    }
    this.add(flow, {
      plaintext: ProtoValue.encode(value).finish(),
      isText: false,
      assignValue: assign,
      assignText: undefined,
    });
  }

  /** Queues every attribute write that is not indexed and not declared Plaintext. */
  public sealAttributeWrites(
    flow: PayloadFlowReference,
    writes: AttributeWrite[],
    isPlaintextAttributeKey: (key: string) => boolean,
  ): void {
    writes.forEach((write, index) => {
      if (write.indexConfig?.enable === true || isPlaintextAttributeKey(write.key)) {
        return;
      }
      this.sealValue(flow, write.value, (sealed) => {
        writes[index] = { ...write, value: sealed };
      });
    });
  }

  /**
   * Queues a string field in the `dex-encoded:` format.
   * Empty text stays empty. Text that starts with the prefix is always encrypted, or space-prefixed
   * without a codec, so readers never mistake it for an envelope.
   */
  public sealText(
    flow: PayloadFlowReference,
    text: string,
    encrypt: boolean,
    assign: (sealed: string) => void,
  ): void {
    if (text.length === 0) {
      return;
    }
    const collides = text.startsWith(encodedTextPrefix);
    if (!this.encryption.hasCodec) {
      if (collides) {
        assign(` ${text}`);
      }
      return;
    }
    if (!encrypt && !collides) {
      return;
    }
    this.add(flow, {
      plaintext: ProtoValue.encode({ kind: { $case: "stringValue", value: text } }).finish(),
      isText: true,
      assignValue: undefined,
      assignText: assign,
    });
  }

  public async run(): Promise<void> {
    for (const { flow, items } of this.groups.values()) {
      const context = this.encryption.payloadContext(flow);
      const envelopes = await this.encryption.encodeEnvelopes(context, items.map((item) => item.plaintext));
      items.forEach((item, index) => {
        const envelope = envelopes[index]!;
        if (item.isText) {
          const encoded = Buffer.from(envelope).toString("base64");
          if (encoded.length > this.encryption.settings.maxEncodedTextBytes) {
            throw sdkPayloadError(
              "encode",
              "limit_exceeded",
              `a ${encoded.length}-byte encoded text exceeds the ${this.encryption.settings.maxEncodedTextBytes}-byte limit`,
            );
          }
          item.assignText!(`${encodedTextPrefix}${encoded}`);
        } else {
          item.assignValue!(ProtoValue.create({
            kind: { $case: "objValue", value: { encoding: payloadEnvelopeMarker, payload: envelope } },
          }));
        }
      });
    }
    this.groups.clear();
  }

  private add(flow: PayloadFlowReference, item: PendingSeal): void {
    const group = this.groups.get(flow.flowId);
    if (group === undefined) {
      this.groups.set(flow.flowId, { flow, items: [item] });
      return;
    }
    if (group.flow.flowType === undefined && flow.flowType !== undefined) {
      group.flow = flow;
    }
    group.items.push(item);
  }
}

interface PendingOpen {
  readonly envelope: PayloadEnvelope;
  readonly isText: boolean;
  readonly assignValue: ((opened: ProtoValue) => void) | undefined;
  readonly assignText: ((opened: string) => void) | undefined;
}

/** Collects inbound envelope Values and encoded texts and decrypts them with one codec call per Flow. */
export class PayloadOpenBatch {
  private readonly groups = new Map<string, { flow: PayloadFlowReference; items: PendingOpen[] }>();
  private missingCodec = false;

  public constructor(private readonly encryption: PayloadEncryption) {}

  /**
   * Queues a Value whose object encoding starts with `dex/`; every other Value passes through.
   * @throws {@link PayloadEncryptionError} Without a codec, at once, for a marked Value.
   */
  public openValue(
    flow: PayloadFlowReference,
    value: ProtoValue | undefined,
    assign: (opened: ProtoValue) => void,
  ): void {
    const kind = value?.kind;
    if (kind?.$case !== "objValue" || !isReservedPayloadEncoding(kind.value.encoding)) {
      return;
    }
    if (!this.encryption.hasCodec) {
      throw codecNotConfiguredError();
    }
    this.add(flow, {
      envelope: this.encryption.parseObjectEnvelope(kind.value.encoding, kind.value.payload),
      isText: false,
      assignValue: assign,
      assignText: undefined,
    });
  }

  /** Queues text that starts with `dex-encoded:`; other text, including the space-prefixed form, passes through. */
  public openText(flow: PayloadFlowReference, text: string, assign: (opened: string) => void): void {
    if (!text.startsWith(encodedTextPrefix)) {
      return;
    }
    if (!this.encryption.hasCodec) {
      this.missingCodec = true;
      return;
    }
    this.add(flow, {
      envelope: this.encryption.parseTextEnvelope(text.slice(encodedTextPrefix.length)),
      isText: true,
      assignValue: undefined,
      assignText: assign,
    });
  }

  public async run(): Promise<void> {
    if (this.missingCodec) {
      throw codecNotConfiguredError();
    }
    for (const { flow, items } of this.groups.values()) {
      const context = this.encryption.payloadContext(flow);
      const values = await this.encryption.decodeEnvelopes(
        context,
        items.map((item) => item.envelope),
        items.map((item) => item.isText),
      );
      items.forEach((item, index) => {
        const value = values[index]!;
        if (item.isText) {
          item.assignText!((value.kind as { $case: "stringValue"; value: string }).value);
        } else {
          item.assignValue!(value);
        }
      });
    }
    this.groups.clear();
  }

  private add(flow: PayloadFlowReference, item: PendingOpen): void {
    const group = this.groups.get(flow.flowId);
    if (group === undefined) {
      this.groups.set(flow.flowId, { flow, items: [item] });
      return;
    }
    if (group.flow.flowType === undefined && flow.flowType !== undefined) {
      group.flow = flow;
    }
    group.items.push(item);
  }
}

const codecCategoryDescriptions: Readonly<Record<string, string>> = {
  malformed: "the payload envelope is malformed",
  unsupported_marker: "the payload format version is not supported",
  unsupported_cipher: "the PayloadCodec does not support the envelope's cipher",
  unsupported_compression: "the envelope's compression is unknown",
  unknown_key: "the key ID is unknown or refused",
  authentication_failed: "payload authentication failed",
  limit_exceeded: "a payload size limit was exceeded",
  invalid_plaintext: "the plaintext is not an encryptable Dex Value",
  codec_failed: "the PayloadCodec failed",
};

/**
 * Replaces any PayloadCodec failure with an SDK error; codec text, causes, and properties are dropped.
 * @returns An error with a known category, and a key ID only when it names one of `envelopeKeyIds`.
 */
function sanitizedCodecError(
  operation: "encode" | "decode",
  failure: unknown,
  envelopeKeyIds: readonly string[],
): PayloadEncryptionError {
  let category: PayloadEncryptionErrorCategory = "codec_failed";
  let keyId: string | undefined;
  if (isPayloadEncryptionErrorWithoutThrowing(failure)) {
    const reported = readStringWithoutThrowing(failure, "category");
    if (reported !== undefined && Object.hasOwn(codecCategoryDescriptions, reported)) {
      category = reported as PayloadEncryptionErrorCategory;
    }
    const reportedKeyId = readStringWithoutThrowing(failure, "keyId");
    if (reportedKeyId !== undefined && envelopeKeyIds.includes(reportedKeyId)) {
      keyId = reportedKeyId;
    }
  }
  return sdkPayloadError(operation, category, codecCategoryDescriptions[category]!, keyId);
}

/**
 * Reports whether a value is a PayloadEncryptionError; a Proxy trap that throws counts as no.
 * @returns Whether `instanceof` succeeded without throwing.
 */
export function isPayloadEncryptionErrorWithoutThrowing(value: unknown): value is PayloadEncryptionError {
  try {
    return value instanceof PayloadEncryptionError;
  } catch {
    return false;
  }
}

/**
 * Reads one property of an untrusted object; getters, Proxy traps, and non-strings yield undefined.
 * @returns The string property value, or `undefined`.
 */
function readStringWithoutThrowing(value: object, property: string): string | undefined {
  try {
    const read: unknown = Reflect.get(value, property);
    return typeof read === "string" ? read : undefined;
  } catch {
    return undefined;
  }
}

/**
 * Builds Worker failure text for a payload error from the snapshot taken when the SDK built it.
 * An error without a snapshot, such as one a handler built, becomes `codec_failed` without a key ID.
 * Never reads the error's properties and never throws.
 * @returns SDK-generated text that is safe to send to the Server in clear.
 */
export function payloadErrorTextForServer(failure: unknown): string {
  const snapshot = typeof failure === "object" && failure !== null ? sdkPayloadErrorSnapshots.get(failure) : undefined;
  const category = snapshot?.category ?? "codec_failed";
  const description = category === "codec_not_configured"
    ? codecNotConfiguredMessage
    : codecCategoryDescriptions[category] ?? codecCategoryDescriptions.codec_failed!;
  return new PayloadEncryptionError(snapshot?.operation ?? "decode", category, description, snapshot?.keyId).message;
}

/**
 * Builds the Worker failure for a PayloadEncryptionError from fixed fields only.
 * The original error's name, message, and stack are never read.
 * @returns A fresh response with a fixed error type, sanitized detail, and empty stack trace.
 */
export function payloadWorkerErrorResponse(failure: unknown, grpcStatus: number): WorkerErrorResponse {
  return WorkerErrorResponse.create({
    detail: payloadErrorTextForServer(failure),
    errorType: "PayloadEncryptionError",
    stackTrace: "",
    retryAfterSeconds: 0,
    grpcStatus,
  });
}

function isInt32(value: unknown): value is number {
  return typeof value === "number" && Number.isInteger(value) && value >= -2_147_483_648 && value <= 2_147_483_647;
}

/**
 * Copies bytes into a plain Uint8Array.
 * The typed-array constructor reads internal slots, not species, getters, or `constructor`.
 * @returns The copy.
 */
function copyBytes(value: unknown): Uint8Array {
  if (!(value instanceof Uint8Array)) {
    throw new TypeError("bytes");
  }
  return new Uint8Array(value);
}

/**
 * Copies a codec's envelopes into SDK-owned objects, so later checks never touch codec code.
 * It reads entries by index and never calls methods of the returned array.
 * @returns One copied envelope per expected entry.
 */
function copyCodecEnvelopes(raw: unknown, expectedCount: number): PayloadEnvelope[] {
  const copies: PayloadEnvelope[] = [];
  try {
    if (!Array.isArray(raw) || Reflect.get(raw, "length") !== expectedCount) {
      throw new TypeError("count");
    }
    for (let index = 0; index < expectedCount; index += 1) {
      const entry: unknown = Reflect.get(raw, index);
      if (typeof entry !== "object" || entry === null) {
        throw new TypeError("entry");
      }
      const cipher: unknown = Reflect.get(entry, "cipher");
      const keyId: unknown = Reflect.get(entry, "keyId");
      const data = copyBytes(Reflect.get(entry, "data"));
      const compression: unknown = Reflect.get(entry, "compression");
      const metadata: unknown = Reflect.get(entry, "metadata") ?? {};
      if (!isInt32(cipher) || typeof keyId !== "string" || !isInt32(compression)
        || typeof metadata !== "object" || metadata === null) {
        throw new TypeError("field");
      }
      const copiedMetadata: { [key: string]: Uint8Array } = Object.create(null) as { [key: string]: Uint8Array };
      const metadataKeys: unknown = Object.keys(metadata);
      if (!Array.isArray(metadataKeys)) {
        throw new TypeError("metadata");
      }
      for (const key of metadataKeys) {
        if (typeof key !== "string") {
          throw new TypeError("metadata key");
        }
        copiedMetadata[key] = copyBytes(Reflect.get(metadata, key));
      }
      copies.push({ cipher, keyId, data, compression, metadata: copiedMetadata });
    }
  } catch {
    throw sdkPayloadError("encode", "codec_failed", `the PayloadCodec did not return ${expectedCount} valid envelopes`);
  }
  return copies;
}

/**
 * Copies a codec's plaintexts into SDK-owned byte arrays by index.
 * @returns One copied plaintext per expected entry.
 */
function copyCodecPlaintexts(raw: unknown, expectedCount: number): Uint8Array[] {
  const copies: Uint8Array[] = [];
  try {
    if (!Array.isArray(raw) || Reflect.get(raw, "length") !== expectedCount) {
      throw new TypeError("count");
    }
    for (let index = 0; index < expectedCount; index += 1) {
      copies.push(copyBytes(Reflect.get(raw, index)));
    }
  } catch {
    throw sdkPayloadError("decode", "codec_failed", `the PayloadCodec did not return ${expectedCount} byte plaintexts`);
  }
  return copies;
}

function parseEnvelope(payload: Uint8Array): PayloadEnvelope {
  let envelope: PayloadEnvelope;
  try {
    envelope = PayloadEnvelope.decode(payload);
  } catch {
    throw sdkPayloadError("decode", "malformed", "envelope bytes are not a PayloadEnvelope");
  }
  requireEnvelopeHeader(envelope, "decode");
  if (envelope.cipher === PayloadCipher.PAYLOAD_CIPHER_UNSPECIFIED) {
    throw sdkPayloadError("decode", "unsupported_cipher", "envelope has no cipher", envelope.keyId);
  }
  return envelope;
}

/** Runs checks 5 and 6: the key ID format and the metadata limits. */
function requireEnvelopeHeader(envelope: PayloadEnvelope, operation: "encode" | "decode"): void {
  if (typeof envelope.keyId !== "string" || !isValidKeyId(envelope.keyId)) {
    throw sdkPayloadError(
      operation,
      "malformed",
      "envelope key ID must have 1 to 64 printable ASCII characters without spaces",
    );
  }
  const metadata = Object.entries(envelope.metadata ?? {});
  if (metadata.length > maxMetadataEntries) {
    throw sdkPayloadError(
      operation,
      "limit_exceeded",
      `envelope has ${metadata.length} metadata entries, more than ${maxMetadataEntries}`,
      envelope.keyId,
    );
  }
  let metadataBytes = 0;
  for (const [key, value] of metadata) {
    metadataBytes += Buffer.byteLength(key, "utf8") + value.length;
  }
  if (metadataBytes > maxMetadataTotalBytes) {
    throw sdkPayloadError(
      operation,
      "limit_exceeded",
      `envelope metadata has ${metadataBytes} bytes, more than ${maxMetadataTotalBytes}`,
      envelope.keyId,
    );
  }
}

/** Runs check 14: the plaintext is an encryptable Value, and a string for prefix text. */
function parsePlaintextValue(plaintext: Uint8Array, requireString: boolean, keyId: string | undefined): ProtoValue {
  let value: ProtoValue;
  try {
    value = ProtoValue.decode(plaintext);
  } catch {
    throw sdkPayloadError("decode", "invalid_plaintext", "plaintext is not a Dex Value", keyId);
  }
  const kind = value.kind;
  const isEncryptable = kind !== undefined && (
    kind.$case === "stringValue"
    || kind.$case === "intValue"
    || kind.$case === "doubleValue"
    || kind.$case === "boolValue"
    || (kind.$case === "objValue" && !isReservedPayloadEncoding(kind.value.encoding))
  );
  if (!isEncryptable) {
    throw sdkPayloadError(
      "decode",
      "invalid_plaintext",
      `plaintext Value kind ${kind?.$case ?? "unset"} is not encryptable`,
      keyId,
    );
  }
  if (requireString && kind.$case !== "stringValue") {
    throw sdkPayloadError("decode", "invalid_plaintext", "encoded text must hold a string Value", keyId);
  }
  return value;
}
