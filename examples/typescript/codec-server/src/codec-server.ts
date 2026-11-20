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

import type { IncomingMessage, ServerResponse } from "node:http";

import {
  AesGcmPayloadCodec,
  PayloadCipher,
  PayloadEncryptionError,
  PayloadEnvelope,
  type PayloadCodec,
  type PayloadContext,
} from "@superdurable/dex";

import {
  CallerForbiddenError,
  createRequestAuthenticator,
  SingleTenantPayloadCodecSelector,
  type CallerPayloadCodecSelector,
  type RequestAuthenticator,
} from "./auth.ts";
import type { CodecServerConfig, PayloadSizeLimits } from "./config.ts";
import { CorsOriginAllowList } from "./cors.ts";
import { createPayloadEncryptionKeyProvider, decodeCanonicalBase64, isValidPayloadKeyId } from "./keys.ts";
import {
  decodeDexValue,
  describeUnencryptableValue,
  DexValueFormatError,
  encodeDexValue,
  formatDexValueJson,
  measureDexValueJsonBytes,
  parseDexValueJson,
  type DexValue,
} from "./value-protobuf.ts";

const aesGcmNonceAndTagBytes = 28;
const maxMetadataEntries = 16;
const maxMetadataTotalBytes = 4096;

/** Writes one structured log record. Callers never pass payloads, keys, or credentials. */
export type CodecServerLogger = (level: "info" | "error", message: string, fields: Record<string, unknown>) => void;

/** Everything a PayloadCodecHttpServer needs; build it with createPayloadCodecHttpServer. */
export interface PayloadCodecHttpServerOptions {
  readonly authenticator: RequestAuthenticator;
  readonly codecSelector: CallerPayloadCodecSelector;
  readonly corsOrigins: CorsOriginAllowList;
  readonly limits: PayloadSizeLimits;
  readonly maxRequestBodyBytes: number;
  readonly maxItemsPerRequest: number;
  readonly maxDecodedBytesPerRequest: number;
  readonly logger: CodecServerLogger;
}

/** A protocol error. message never holds payload bytes, plaintext, keys, or codec error text. */
class CodecRequestError {
  public readonly status: number;
  public readonly category: string;
  public readonly message: string;
  public readonly keyId: string | undefined;
  public index: number | undefined;

  public constructor(status: number, category: string, message: string, keyId?: string) {
    this.status = status;
    this.category = category;
    this.message = message;
    this.keyId = keyId;
  }

  public at(index: number): CodecRequestError {
    this.index = index;
    return this;
  }
}

/** What the access log may record; payloads and credentials are never added. */
interface RequestLogFields {
  caller?: string;
  itemCount?: number;
  errorCategory?: string;
  keyId?: string;
  errorIndex?: number;
}

// Fixed client messages; codec and key provider error text is never sent or logged.
const decodeCategoryMessages: ReadonlyMap<string, string> = new Map([
  ["malformed", "envelope is malformed"],
  ["unsupported_marker", "envelope format version is not supported"],
  ["unsupported_cipher", "envelope cipher is not supported"],
  ["unsupported_compression", "envelope compression is not supported"],
  ["unknown_key", "envelope key is not available to this server"],
  ["authentication_failed", "envelope failed authentication"],
  ["limit_exceeded", "envelope exceeds a size limit"],
  ["invalid_plaintext", "decrypted plaintext is not an encryptable Value"],
]);

/**
 * Builds the server from configuration: keys, AES-256-GCM codec, authentication, and CORS.
 * @returns The request handler wiring.
 */
export async function createPayloadCodecHttpServer(
  config: CodecServerConfig,
  logger: CodecServerLogger,
): Promise<PayloadCodecHttpServer> {
  const keyProvider = await createPayloadEncryptionKeyProvider(config);
  // No compression: ciphertext length would reveal how well mixed secret and attacker content compresses.
  const codec = new AesGcmPayloadCodec(keyProvider, {
    maxDecompressedBytes: config.payloadSizeLimits.maxPlaintextBytes,
    maxDataBytes: config.payloadSizeLimits.maxDataBytes,
  });
  return new PayloadCodecHttpServer({
    authenticator: createRequestAuthenticator(config),
    codecSelector: new SingleTenantPayloadCodecSelector(codec),
    corsOrigins: new CorsOriginAllowList(config.allowedOrigins, config.allowCredentials),
    limits: config.payloadSizeLimits,
    maxRequestBodyBytes: config.maxRequestBodyBytes,
    maxItemsPerRequest: config.maxItemsPerRequest,
    maxDecodedBytesPerRequest: config.maxDecodedBytesPerRequest,
    logger,
  });
}

/** Serves the codec server protocol of protos/payload-encryption.md section 11. */
export class PayloadCodecHttpServer {
  private readonly options: PayloadCodecHttpServerOptions;

  public constructor(options: PayloadCodecHttpServerOptions) {
    this.options = options;
  }

  /** Handles one HTTP request: `POST /encode`, `POST /decode`, or `GET /healthz`. */
  public readonly handleRequest = (request: IncomingMessage, response: ServerResponse): void => {
    const path = (request.url ?? "/").split("?")[0];
    if (path === "/healthz" && (request.method === "GET" || request.method === "HEAD")) {
      writeJson(response, 200, `{"status":"ok"}`);
      return;
    }
    if (path !== "/encode" && path !== "/decode") {
      writeJson(response, 404, errorBodyJson(new CodecRequestError(404, "malformed", "not found")));
      return;
    }
    const started = performance.now();
    const fields: RequestLogFields = {};
    response.once("finish", () => {
      this.options.logger("info", "codec request", {
        method: request.method,
        path,
        status: response.statusCode,
        duration_ms: Math.round(performance.now() - started),
        origin: request.headers.origin ?? "",
        caller: fields.caller ?? "",
        item_count: fields.itemCount ?? 0,
        ...(fields.errorCategory === undefined
          ? {}
          : { error_category: fields.errorCategory, key_id: fields.keyId ?? "" }),
        ...(fields.errorIndex === undefined ? {} : { error_index: fields.errorIndex }),
      });
    });
    const corsDecision = this.options.corsOrigins.applyCorsHeaders(request, response);
    if (corsDecision === "preflight-answered") {
      return;
    }
    if (corsDecision === "forbidden") {
      this.writeError(response, fields, new CodecRequestError(403, "forbidden", "origin is not allowed"));
      return;
    }
    this.serveEndpoint(path, request, response, fields).catch(() => {
      this.options.logger("error", "codec request failed", { internal_error_code: "unexpected_handler_failure" });
      if (!response.headersSent) {
        this.writeError(response, fields, internalError());
      } else {
        response.destroy();
      }
    });
  };

  private async serveEndpoint(
    path: "/encode" | "/decode",
    request: IncomingMessage,
    response: ServerResponse,
    fields: RequestLogFields,
  ): Promise<void> {
    try {
      const { codec, payloadContext, items } = await this.admitRequest(
        request,
        fields,
        path === "/decode" ? "envelopes" : "values",
      );
      if (path === "/decode") {
        const values = await this.decodeEnvelopes(codec, payloadContext, items as string[]);
        writeJson(response, 200, `{"values":[${values.join(",")}]}`);
      } else {
        const envelopes = await this.encodeValues(codec, payloadContext, items);
        writeJson(response, 200, JSON.stringify({ envelopes }));
      }
    } catch (failure) {
      if (failure instanceof CodecRequestError) {
        this.writeError(response, fields, failure);
        return;
      }
      throw failure;
    }
  }

  /** Checks method, content type, credentials, authorization, body, JSON, and item count, in order. */
  private async admitRequest(
    request: IncomingMessage,
    fields: RequestLogFields,
    itemsField: "envelopes" | "values",
  ): Promise<{ codec: PayloadCodec; payloadContext: PayloadContext; items: unknown[] }> {
    if (request.method !== "POST") {
      throw malformedRequest("use POST");
    }
    // Requiring JSON also forces a CORS preflight for every cross-origin request.
    const mediaType = (request.headers["content-type"] ?? "").split(";")[0]!.trim().toLowerCase();
    if (mediaType !== "application/json") {
      throw malformedRequest("Content-Type must be application/json");
    }
    const caller = this.options.authenticator.authenticateRequest(request);
    if (caller === undefined) {
      throw new CodecRequestError(401, "unauthenticated", "credentials are missing or invalid");
    }
    fields.caller = caller.subject;
    let codec: PayloadCodec;
    try {
      codec = await this.options.codecSelector.selectPayloadCodec(caller);
    } catch (failure) {
      if (failure instanceof CallerForbiddenError) {
        throw new CodecRequestError(403, "forbidden", "the caller may not use payload keys");
      }
      this.options.logger("error", "select payload codec failed", { internal_error_code: "codec_selector_failed" });
      throw internalError();
    }
    const body = await readRequestBody(request, this.options.maxRequestBodyBytes);
    let parsed: unknown;
    try {
      parsed = JSON.parse(body.toString("utf8"));
    } catch {
      throw malformedRequest("request body is not valid protocol JSON");
    }
    if (!isJsonObject(parsed)) {
      throw malformedRequest("request body is not valid protocol JSON");
    }
    const payloadContext = parsePayloadContext(parsed.context);
    const items = parsed[itemsField];
    if (!Array.isArray(items)) {
      throw malformedRequest(`the request has no ${itemsField} list`);
    }
    if (itemsField === "envelopes" && !items.every((item) => typeof item === "string")) {
      throw malformedRequest("envelopes must be base64 strings");
    }
    fields.itemCount = items.length;
    if (items.length > this.options.maxItemsPerRequest) {
      throw new CodecRequestError(413, "limit_exceeded", "the request has more entries than the server's limit");
    }
    return { codec, payloadContext, items };
  }

  /**
   * Runs decode steps 3 to 14 per envelope, one at a time, to bound memory.
   * @returns Value JSON texts in request order.
   */
  private async decodeEnvelopes(
    codec: PayloadCodec,
    payloadContext: PayloadContext,
    envelopeTexts: readonly string[],
  ): Promise<string[]> {
    const values: string[] = [];
    let decodedBytes = 0;
    for (const [index, envelopeText] of envelopeTexts.entries()) {
      let value: DexValue;
      try {
        value = await this.decodeEnvelope(codec, payloadContext, envelopeText);
      } catch (failure) {
        if (failure instanceof CodecRequestError && failure.status === 422) {
          failure.at(index);
        }
        throw failure;
      }
      // Measure before serializing: escaping can make the JSON several times the plaintext.
      decodedBytes += measureDexValueJsonBytes(value);
      if (decodedBytes > this.options.maxDecodedBytesPerRequest) {
        throw new CodecRequestError(
          413,
          "limit_exceeded",
          "decoded values exceed the server's per-request limit; send fewer envelopes per request",
        );
      }
      values.push(formatDexValueJson(value));
    }
    return values;
  }

  /** @returns The decoded, encryptable Value of one envelope. */
  private async decodeEnvelope(
    codec: PayloadCodec,
    payloadContext: PayloadContext,
    envelopeText: string,
  ): Promise<DexValue> {
    const envelope = this.parseEnvelopeText(envelopeText);
    let plaintexts: Uint8Array[];
    try {
      plaintexts = await codec.decode(payloadContext, [envelope], {
        maxPlaintextBytes: this.options.limits.maxPlaintextBytes,
      });
    } catch (failure) {
      throw this.classifyCodecDecodeFailure(failure, envelope.keyId);
    }
    if (!Array.isArray(plaintexts) || plaintexts.length !== 1 || !(plaintexts[0] instanceof Uint8Array)) {
      this.options.logger("error", "payload codec failed", { internal_error_code: "decode_result_count_mismatch" });
      throw internalError();
    }
    const plaintext = plaintexts[0];
    if (plaintext.length > this.options.limits.maxPlaintextBytes) {
      throw entryError("limit_exceeded", "plaintext exceeds the server's limit", envelope.keyId);
    }
    let value: DexValue;
    try {
      value = decodeDexValue(plaintext);
    } catch {
      throw entryError("invalid_plaintext", "plaintext is not a Value", envelope.keyId);
    }
    const problem = describeUnencryptableValue(value);
    if (problem !== undefined) {
      throw entryError("invalid_plaintext", problem, envelope.keyId);
    }
    return value;
  }

  /** Runs decode steps 3 to 7, which the SDK runs before calling a codec. */
  private parseEnvelopeText(envelopeText: string): PayloadEnvelope {
    const limits = this.options.limits;
    if (envelopeText.length > limits.maxEncodedTextBytes) {
      throw entryError("limit_exceeded", "envelope text exceeds the server's limit");
    }
    if (envelopeText.length % 4 !== 0) {
      throw entryError("malformed", "envelope is not canonical padded standard base64");
    }
    const padding = envelopeText.endsWith("==") ? 2 : envelopeText.endsWith("=") ? 1 : 0;
    if ((envelopeText.length / 4) * 3 - padding > limits.maxEnvelopeBytes) {
      throw entryError("limit_exceeded", "envelope exceeds the server's limit");
    }
    const envelopeBytes = decodeCanonicalBase64(envelopeText);
    if (envelopeBytes === undefined) {
      throw entryError("malformed", "envelope is not canonical padded standard base64");
    }
    let envelope: PayloadEnvelope;
    try {
      envelope = PayloadEnvelope.decode(envelopeBytes);
    } catch {
      throw entryError("malformed", "envelope is not a PayloadEnvelope");
    }
    if (!isValidPayloadKeyId(envelope.keyId)) {
      throw entryError("malformed", "envelope key ID is invalid");
    }
    const metadata = Object.entries(envelope.metadata);
    let metadataBytes = 0;
    for (const [key, value] of metadata) {
      metadataBytes += Buffer.byteLength(key, "utf8") + value.length;
    }
    if (metadata.length > maxMetadataEntries || metadataBytes > maxMetadataTotalBytes) {
      throw entryError("limit_exceeded", "envelope metadata exceeds its limits", envelope.keyId);
    }
    if (envelope.cipher === PayloadCipher.PAYLOAD_CIPHER_UNSPECIFIED) {
      throw entryError("unsupported_cipher", "envelope cipher is not supported", envelope.keyId);
    }
    return envelope;
  }

  /** Maps a codec failure to a fixed message for its category; anything else is internal. */
  private classifyCodecDecodeFailure(failure: unknown, envelopeKeyId: string): CodecRequestError {
    if (failure instanceof PayloadEncryptionError) {
      const message = decodeCategoryMessages.get(failure.category);
      if (message !== undefined) {
        // The envelope's key ID is already public and validated; the error's own text is discarded.
        return entryError(failure.category, `${message} (key ID ${envelopeKeyId})`, envelopeKeyId);
      }
    }
    this.options.logger("error", "payload codec failed", { internal_error_code: "codec_decode_failed" });
    return internalError();
  }

  /**
   * Checks every value first, so failures carry an index, then encodes one batch.
   * @returns Standard padded base64 envelopes in request order.
   */
  private async encodeValues(
    codec: PayloadCodec,
    payloadContext: PayloadContext,
    valuesJson: readonly unknown[],
  ): Promise<string[]> {
    const limits = this.options.limits;
    const plaintexts: Uint8Array[] = [];
    for (const [index, valueJson] of valuesJson.entries()) {
      let value: DexValue;
      try {
        value = parseDexValueJson(valueJson);
      } catch (failure) {
        if (failure instanceof DexValueFormatError) {
          throw entryError("malformed", "entry is not dex.Value JSON").at(index);
        }
        throw failure;
      }
      const problem = describeUnencryptableValue(value);
      if (problem !== undefined) {
        throw entryError("invalid_plaintext", problem).at(index);
      }
      const plaintext = encodeDexValue(value);
      if (
        plaintext.length > limits.maxPlaintextBytes
        || plaintext.length + aesGcmNonceAndTagBytes > limits.maxDataBytes
      ) {
        throw entryError("limit_exceeded", "value exceeds the server's limit").at(index);
      }
      plaintexts.push(plaintext);
    }
    if (plaintexts.length === 0) {
      return [];
    }
    let envelopes: PayloadEnvelope[];
    try {
      envelopes = await codec.encode(payloadContext, plaintexts);
    } catch {
      this.options.logger("error", "payload codec failed", { internal_error_code: "codec_encode_failed" });
      throw internalError();
    }
    if (!Array.isArray(envelopes) || envelopes.length !== plaintexts.length) {
      this.options.logger("error", "payload codec failed", { internal_error_code: "encode_result_count_mismatch" });
      throw internalError();
    }
    return envelopes.map((envelope, index) => {
      let envelopeBytes: Uint8Array;
      try {
        envelopeBytes = PayloadEnvelope.encode(envelope).finish();
      } catch {
        this.options.logger("error", "payload codec failed", { internal_error_code: "serialize_envelope_failed" });
        throw internalError();
      }
      if (envelopeBytes.length > limits.maxEnvelopeBytes) {
        const keyId = isValidPayloadKeyId(envelope.keyId) ? envelope.keyId : undefined;
        throw entryError("limit_exceeded", "envelope exceeds the server's limit", keyId).at(index);
      }
      return Buffer.from(envelopeBytes).toString("base64");
    });
  }

  private writeError(response: ServerResponse, fields: RequestLogFields, failure: CodecRequestError): void {
    fields.errorCategory = failure.category;
    fields.keyId = failure.keyId;
    fields.errorIndex = failure.index;
    if (failure.status === 413) {
      // The rest of an oversized body is not read, so the connection cannot be reused.
      response.setHeader("Connection", "close");
    }
    writeJson(response, failure.status, errorBodyJson(failure));
  }
}

function parsePayloadContext(context: unknown): PayloadContext {
  if (!isJsonObject(context)) {
    throw malformedRequest("context is required");
  }
  const { flow_type: flowType, flow_id: flowId } = context;
  // Clients that know only a Flow ID send an empty flow_type.
  if ((flowType !== undefined && flowType !== null && typeof flowType !== "string")
    || typeof flowId !== "string" || flowId === "") {
    throw malformedRequest("context.flow_id is required, and context.flow_type must be a string");
  }
  return { flowType: typeof flowType === "string" ? flowType : "", flowId };
}

/** Reads the body, answering 413 as soon as it exceeds the limit. */
function readRequestBody(request: IncomingMessage, maxBytes: number): Promise<Buffer> {
  const declaredLength = Number(request.headers["content-length"] ?? "0");
  if (declaredLength > maxBytes) {
    return Promise.reject(bodyTooLarge());
  }
  return new Promise((resolve, reject) => {
    const chunks: Buffer[] = [];
    let receivedBytes = 0;
    let settled = false;
    request.on("data", (chunk: Buffer) => {
      if (settled) {
        return;
      }
      receivedBytes += chunk.length;
      if (receivedBytes > maxBytes) {
        settled = true;
        chunks.length = 0;
        reject(bodyTooLarge());
        return;
      }
      chunks.push(chunk);
    });
    request.on("end", () => {
      if (!settled) {
        settled = true;
        resolve(Buffer.concat(chunks));
      }
    });
    request.on("error", () => {
      if (!settled) {
        settled = true;
        reject(malformedRequest("request body could not be read"));
      }
    });
  });
}

function bodyTooLarge(): CodecRequestError {
  return new CodecRequestError(413, "limit_exceeded", "request body exceeds the server's limit");
}

function malformedRequest(message: string): CodecRequestError {
  return new CodecRequestError(400, "malformed", message);
}

function internalError(): CodecRequestError {
  return new CodecRequestError(500, "internal", "internal error");
}

function entryError(category: string, message: string, keyId?: string): CodecRequestError {
  return new CodecRequestError(422, category, message, keyId);
}

function errorBodyJson(failure: CodecRequestError): string {
  return JSON.stringify({
    error: {
      category: failure.category,
      message: failure.message,
      ...(failure.index === undefined ? {} : { index: failure.index }),
    },
  });
}

function writeJson(response: ServerResponse, status: number, body: string): void {
  response.writeHead(status, {
    "Content-Type": "application/json",
    "Cache-Control": "no-store",
    "Content-Length": Buffer.byteLength(body, "utf8"),
  });
  response.end(body);
}

function isJsonObject(json: unknown): json is Record<string, unknown> {
  return typeof json === "object" && json !== null && !Array.isArray(json);
}
