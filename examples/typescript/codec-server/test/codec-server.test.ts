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

import assert from "node:assert/strict";
import { test } from "node:test";

import {
  AesGcmPayloadCodec,
  FilePayloadEncryptionKeyProvider,
  PayloadCompression,
  PayloadEncryptionError,
  PayloadEnvelope,
  type PayloadCodec,
  type PayloadCodecDecodeOptions,
  type PayloadContext,
  type PayloadEncryptionKeyProvider,
} from "@superdurable/dex";

import {
  authModeTrustedProxyHeader,
  derivePayloadSizeLimits,
} from "../src/config.ts";
import {
  CallerForbiddenError,
  SingleTenantPayloadCodecSelector,
  type CallerPayloadCodecSelector,
} from "../src/auth.ts";
import {
  decodeDexValue,
  encodeDexValue,
  parseDexValueJson,
  type DexValue,
} from "../src/value-protobuf.ts";
import {
  assertErrorResponse,
  defaultTestConfig,
  envelopeTextOf,
  postJson,
  readGoldenVectors,
  sendRequest,
  startTestCodecServer,
  testAllowedOrigin,
  testBearerToken,
  testPayloadContext,
  writeKeyFile,
  writeRandomKeyFile,
  assertSameDexValue,
  overflowingIntValuePlaintext,
  type GoldenLimits,
} from "./test-support.ts";

const sdkPayloadContext: PayloadContext = { flowType: "OrderFlow", flowId: "order-1001" };
const sentinelSecret = "SENTINEL-c0dec-secret-7f3a";

function sampleValues(): DexValue[] {
  return [
    { kind: "stringValue", stringValue: "card ending 4242" },
    { kind: "stringValue", stringValue: "" },
    { kind: "intValue", intValue: -7n },
    { kind: "doubleValue", doubleValue: -0 },
    { kind: "boolValue", boolValue: false },
    { kind: "objValue", encoding: "json", payload: new Uint8Array(Buffer.from(`{"orderId":"A-1"}`)) },
  ];
}

async function sdkCodecFromKeyFile(path: string, compression?: PayloadCompression): Promise<AesGcmPayloadCodec> {
  const keys = await FilePayloadEncryptionKeyProvider.fromFile(path);
  return new AesGcmPayloadCodec(keys, compression === undefined ? {} : { compression });
}

async function encryptWithSdk(codec: PayloadCodec, values: readonly DexValue[]): Promise<string[]> {
  const envelopes = await codec.encode(sdkPayloadContext, values.map(encodeDexValue));
  return envelopes.map((envelope) => Buffer.from(PayloadEnvelope.encode(envelope).finish()).toString("base64"));
}

function parseValuesResponse(body: string): DexValue[] {
  return (JSON.parse(body) as { values: unknown[] }).values.map(parseDexValueJson);
}

test("/decode returns values encrypted by the TypeScript SDK codec with a shared key file", async (t) => {
  const { path } = writeRandomKeyFile(t);
  const server = await startTestCodecServer(t, defaultTestConfig(path));
  const values = sampleValues();
  const envelopes = await encryptWithSdk(await sdkCodecFromKeyFile(path), values);

  const response = await postJson(server, "/decode", { context: testPayloadContext, envelopes });
  assert.equal(response.status, 200, response.body);
  assert.equal(response.headers["cache-control"], "no-store");
  const decoded = parseValuesResponse(response.body);
  assert.equal(decoded.length, values.length);
  values.forEach((value, index) => assertSameDexValue(decoded[index]!, value, `value ${index}`));
  assert.match(response.body, /"doubleValue":-0/);
});

test("the TypeScript SDK codec decodes envelopes from /encode", async (t) => {
  const { path } = writeRandomKeyFile(t);
  const server = await startTestCodecServer(t, defaultTestConfig(path));
  const valuesJson = [
    { stringValue: "card ending 4242" },
    { stringValue: "" },
    { intValue: "-9223372036854775808" },
    { doubleValue: "Infinity" },
    { boolValue: true },
    { objValue: { encoding: "json", payload: Buffer.from(`{"a":1}`).toString("base64") } },
  ];
  const response = await postJson(server, "/encode", { context: testPayloadContext, values: valuesJson });
  assert.equal(response.status, 200, response.body);
  const { envelopes } = JSON.parse(response.body) as { envelopes: string[] };
  assert.equal(envelopes.length, valuesJson.length);

  const codec = await sdkCodecFromKeyFile(path);
  const plaintexts = await codec.decode(
    sdkPayloadContext,
    envelopes.map((text) => PayloadEnvelope.decode(Buffer.from(text, "base64"))),
  );
  plaintexts.forEach((plaintext, index) => {
    assertSameDexValue(decodeDexValue(plaintext), parseDexValueJson(valuesJson[index]), `value ${index}`);
  });
});

test("empty lists return empty lists", async (t) => {
  const { path } = writeRandomKeyFile(t);
  const server = await startTestCodecServer(t, defaultTestConfig(path));
  const decoded = await postJson(server, "/decode", { context: testPayloadContext, envelopes: [] });
  assert.equal(decoded.status, 200);
  assert.deepEqual(JSON.parse(decoded.body), { values: [] });
  const encoded = await postJson(server, "/encode", { context: testPayloadContext, values: [] });
  assert.equal(encoded.status, 200);
  assert.deepEqual(JSON.parse(encoded.body), { envelopes: [] });
});

test("an empty or missing flow_type is accepted; flow_id is required", async (t) => {
  const { path } = writeRandomKeyFile(t);
  const server = await startTestCodecServer(t, defaultTestConfig(path));
  const [envelope] = await encryptWithSdk(await sdkCodecFromKeyFile(path), [{ kind: "stringValue", stringValue: "x" }]);
  for (const context of [{ flow_type: "", flow_id: "order-1001" }, { flow_id: "order-1001" }]) {
    const decoded = await postJson(server, "/decode", { context, envelopes: [envelope] });
    assert.equal(decoded.status, 200, decoded.body);
    const encoded = await postJson(server, "/encode", { context, values: [{ stringValue: "x" }] });
    assert.equal(encoded.status, 200, encoded.body);
  }
  for (const context of [{ flow_type: "OrderFlow", flow_id: "" }, { flow_type: "OrderFlow" }, { flow_type: 5, flow_id: "a" }]) {
    assertErrorResponse(await postJson(server, "/decode", { context, envelopes: [] }), 400, "malformed");
  }
});

test("/decode applies every golden vector envelope case", async (t) => {
  const vectors = readGoldenVectors();
  const keyFilePath = writeKeyFile(t, vectors.keyFile);
  const servers = new Map<string, Awaited<ReturnType<typeof startTestCodecServer>>>();
  const serverFor = async (limits: GoldenLimits | undefined) => {
    const chosen = limits ?? vectors.limits;
    const cacheKey = JSON.stringify(chosen);
    let server = servers.get(cacheKey);
    if (server === undefined) {
      // Vectors lower each limit on its own, so the test sets all four instead of deriving them.
      server = await startTestCodecServer(t, {
        ...defaultTestConfig(keyFilePath),
        payloadSizeLimits: {
          maxEnvelopeBytes: chosen.maxEnvelopeBytes,
          maxEncodedTextBytes: chosen.maxEncodedTextBytes,
          maxDataBytes: chosen.maxDataBytes,
          maxPlaintextBytes: chosen.maxPlaintextBytes,
        },
      });
      servers.set(cacheKey, server);
    }
    return server;
  };

  let checked = 0;
  for (const vectorCase of vectors.positive) {
    const envelopeText = envelopeTextOf(vectorCase);
    if (envelopeText === undefined) {
      continue;
    }
    const server = await serverFor(vectorCase.limits);
    const response = await postJson(server, "/decode", { context: testPayloadContext, envelopes: [envelopeText] });
    assert.equal(response.status, 200, `${vectorCase.name}: ${response.body}`);
    const [value] = parseValuesResponse(response.body);
    assertSameDexValue(value!, parseDexValueJson(vectorCase.expectedValue), vectorCase.name);
    checked += 1;
  }
  for (const vectorCase of vectors.negative) {
    const envelopeText = envelopeTextOf(vectorCase);
    // The caller checks the marker and that prefix text holds a string, so the server cannot.
    if (
      envelopeText === undefined
      || vectorCase.expectedError === "unsupported_marker"
      || vectorCase.name === "text-plaintext-not-string"
    ) {
      continue;
    }
    const server = await serverFor(vectorCase.limits);
    const response = await postJson(server, "/decode", { context: testPayloadContext, envelopes: [envelopeText] });
    assert.equal(response.status, 422, `${vectorCase.name}: ${response.body}`);
    assertErrorResponse(response, 422, vectorCase.expectedError!, 0);
    checked += 1;
  }
  assert.ok(checked >= 55, `only ${checked} golden vector cases ran; the vector file layout may have changed`);
});

test("a decode error names the first failing index", async (t) => {
  const vectors = readGoldenVectors();
  const server = await startTestCodecServer(t, defaultTestConfig(writeKeyFile(t, vectors.keyFile)));
  const envelopeByName = new Map<string, string>([["non-canonical-base64", "AAE"]]);
  for (const vectorCase of [...vectors.positive, ...vectors.negative]) {
    const text = envelopeTextOf(vectorCase);
    if (text !== undefined) {
      envelopeByName.set(vectorCase.name, text);
    }
  }
  const cases: Array<[string[], string, number]> = [
    [["string-ascii", "int-positive", "wrong-tag"], "authentication_failed", 2],
    [["wrong-tag", "non-canonical-base64"], "authentication_failed", 0],
    [["plaintext-null-value", "wrong-tag"], "invalid_plaintext", 0],
    [["string-ascii", "non-canonical-base64", "unknown-key-id"], "malformed", 1],
  ];
  for (const [names, category, index] of cases) {
    const envelopes = names.map((name) => envelopeByName.get(name)!);
    assertErrorResponse(
      await postJson(server, "/decode", { context: testPayloadContext, envelopes }),
      422,
      category,
      index,
    );
  }
});

test("/decode rejects a plaintext whose int64 varint overflows", async (t) => {
  const { path } = writeRandomKeyFile(t);
  const server = await startTestCodecServer(t, defaultTestConfig(path));
  const codec = await sdkCodecFromKeyFile(path);
  const [envelope] = await codec.encode(sdkPayloadContext, [overflowingIntValuePlaintext]);
  const envelopeText = Buffer.from(PayloadEnvelope.encode(envelope!).finish()).toString("base64");
  assertErrorResponse(
    await postJson(server, "/decode", { context: testPayloadContext, envelopes: [envelopeText] }),
    422,
    "invalid_plaintext",
    0,
  );
});

test("CORS allows only listed origins", async (t) => {
  const { path } = writeRandomKeyFile(t);
  const server = await startTestCodecServer(t, defaultTestConfig(path));
  const preflight = (origin: string) => sendRequest(server.baseUrl, "OPTIONS", "/decode", undefined, {
    Origin: origin,
    "Access-Control-Request-Method": "POST",
    "Access-Control-Request-Headers": "authorization, content-type",
    Authorization: null,
    "Content-Type": null,
  });

  const allowed = await preflight(testAllowedOrigin);
  assert.equal(allowed.status, 204);
  assert.equal(allowed.headers["access-control-allow-origin"], testAllowedOrigin);
  assert.equal(allowed.headers["access-control-allow-methods"], "POST");
  assert.match(String(allowed.headers["access-control-allow-headers"]), /Authorization/);
  assert.equal(allowed.headers["access-control-allow-credentials"], undefined);

  assertErrorResponse(await preflight("https://evil.example.com"), 403, "forbidden");
  const forbiddenPost = await postJson(server, "/decode", { context: testPayloadContext, envelopes: [] }, {
    Origin: "https://evil.example.com",
  });
  assertErrorResponse(forbiddenPost, 403, "forbidden");
  assert.equal(forbiddenPost.headers["access-control-allow-origin"], undefined);

  const allowedPost = await postJson(server, "/decode", { context: testPayloadContext, envelopes: [] }, {
    Origin: testAllowedOrigin,
  });
  assert.equal(allowedPost.status, 200);
  assert.equal(allowedPost.headers["access-control-allow-origin"], testAllowedOrigin);
  assert.match(String(allowedPost.headers.vary), /Origin/);
});

test("credentialed CORS sends Access-Control-Allow-Credentials", async (t) => {
  const { path } = writeRandomKeyFile(t);
  const server = await startTestCodecServer(t, { ...defaultTestConfig(path), allowCredentials: true });
  const response = await postJson(server, "/decode", { context: testPayloadContext, envelopes: [] }, {
    Origin: testAllowedOrigin,
  });
  assert.equal(response.status, 200);
  assert.equal(response.headers["access-control-allow-credentials"], "true");
});

test("bearer token failures return 401", async (t) => {
  const { path } = writeRandomKeyFile(t);
  const server = await startTestCodecServer(t, defaultTestConfig(path));
  const body = { context: testPayloadContext, envelopes: [] };
  for (const authorization of [null, "Bearer wrong-token", `Basic ${testBearerToken}`, "Bearer ", testBearerToken]) {
    assertErrorResponse(await postJson(server, "/decode", body, { Authorization: authorization }), 401, "unauthenticated");
  }
  const lowercaseScheme = await postJson(server, "/decode", body, { Authorization: `bearer ${testBearerToken}` });
  assert.equal(lowercaseScheme.status, 200);
});

test("trusted proxy header authentication records the caller and rejects missing or repeated headers", async (t) => {
  const { path } = writeRandomKeyFile(t);
  const server = await startTestCodecServer(t, {
    ...defaultTestConfig(path),
    authMode: authModeTrustedProxyHeader,
    bearerToken: undefined,
    trustedIdentityHeader: "X-Forwarded-Email",
  });
  const body = { context: testPayloadContext, envelopes: [] };
  const accepted = await postJson(server, "/decode", body, { Authorization: null, "X-Forwarded-Email": "ada@example.com" });
  assert.equal(accepted.status, 200, accepted.body);
  assert.match(server.logs.text(), /"caller":"ada@example.com"/);

  assertErrorResponse(await postJson(server, "/decode", body, { Authorization: null }), 401, "unauthenticated");
  assertErrorResponse(
    await postJson(server, "/decode", body, { Authorization: null, "X-Forwarded-Email": "  " }),
    401,
    "unauthenticated",
  );
  assertErrorResponse(
    await postJson(server, "/decode", body, {
      Authorization: null,
      "X-Forwarded-Email": ["ada@example.com", "eve@example.com"],
    }),
    401,
    "unauthenticated",
  );
});

class LeakingPayloadEncryptionKeyProvider implements PayloadEncryptionKeyProvider {
  public async currentKeyId(): Promise<string> {
    throw new Error(`key service unavailable: ${sentinelSecret}`);
  }

  public async key(): Promise<Uint8Array> {
    throw new Error(`key material ${sentinelSecret}`);
  }
}

class LeakingPayloadCodec implements PayloadCodec {
  private readonly failure: Error;

  public constructor(failure: Error) {
    this.failure = failure;
  }

  public async encode(): Promise<PayloadEnvelope[]> {
    throw this.failure;
  }

  public async decode(): Promise<Uint8Array[]> {
    throw this.failure;
  }
}

class StaticPayloadCodecSelector implements CallerPayloadCodecSelector {
  private readonly codec: PayloadCodec | undefined;
  private readonly failure: Error | undefined;

  public constructor(codec: PayloadCodec | undefined, failure?: Error) {
    this.codec = codec;
    this.failure = failure;
  }

  public async selectPayloadCodec(): Promise<PayloadCodec> {
    if (this.failure !== undefined) {
      throw this.failure;
    }
    return this.codec!;
  }
}

test("/decode passes the plaintext limit to a custom codec", async (t) => {
  const { path } = writeRandomKeyFile(t);
  const maxPlaintextBytes = 64;
  const config = defaultTestConfig(path);
  const plaintext = encodeDexValue({ kind: "stringValue", stringValue: "x".repeat(100) });
  let receivedLimit: number | undefined;
  const codec: PayloadCodec = {
    async encode(): Promise<PayloadEnvelope[]> {
      return [];
    },
    async decode(
      _context: PayloadContext,
      _envelopes: readonly PayloadEnvelope[],
      options?: PayloadCodecDecodeOptions,
    ): Promise<Uint8Array[]> {
      receivedLimit = options?.maxPlaintextBytes;
      if (receivedLimit !== undefined && plaintext.length > receivedLimit) {
        throw new PayloadEncryptionError("decode", "limit_exceeded", "plaintext exceeds the limit");
      }
      return [plaintext];
    },
  };
  const server = await startTestCodecServer(t, {
    ...config,
    payloadSizeLimits: { ...config.payloadSizeLimits, maxPlaintextBytes },
  }, new StaticPayloadCodecSelector(codec));
  const [envelopeText] = await encryptWithSdk(await sdkCodecFromKeyFile(path), [
    { kind: "stringValue", stringValue: "x" },
  ]);

  assertErrorResponse(
    await postJson(server, "/decode", { context: testPayloadContext, envelopes: [envelopeText] }),
    422,
    "limit_exceeded",
    0,
  );
  assert.equal(receivedLimit, maxPlaintextBytes);
});

test("key provider, codec, and selector error text never reaches responses or logs", async (t) => {
  const { path } = writeRandomKeyFile(t);
  const [envelopeText] = await encryptWithSdk(await sdkCodecFromKeyFile(path), [{ kind: "stringValue", stringValue: "x" }]);
  const leakingAesCodec = new AesGcmPayloadCodec(new LeakingPayloadEncryptionKeyProvider());
  const decodeBody = { context: testPayloadContext, envelopes: [envelopeText] };
  const encodeBody = { context: testPayloadContext, values: [{ stringValue: "x" }] };
  const cases: Array<{ name: string; selector: CallerPayloadCodecSelector; path: string; body: unknown; status: number; category: string; index?: number }> = [
    { name: "provider refuses key on decode", selector: new SingleTenantPayloadCodecSelector(leakingAesCodec), path: "/decode", body: decodeBody, status: 422, category: "unknown_key", index: 0 },
    { name: "provider fails on encode", selector: new SingleTenantPayloadCodecSelector(leakingAesCodec), path: "/encode", body: encodeBody, status: 500, category: "internal" },
    { name: "custom codec throws Error on decode", selector: new StaticPayloadCodecSelector(new LeakingPayloadCodec(new Error(sentinelSecret))), path: "/decode", body: decodeBody, status: 500, category: "internal" },
    { name: "custom codec throws Error on encode", selector: new StaticPayloadCodecSelector(new LeakingPayloadCodec(new Error(sentinelSecret))), path: "/encode", body: encodeBody, status: 500, category: "internal" },
    {
      name: "custom codec throws PayloadEncryptionError with secret text on decode",
      selector: new StaticPayloadCodecSelector(new LeakingPayloadCodec(
        new PayloadEncryptionError("decode", "authentication_failed", `bad tag ${sentinelSecret}`, sentinelSecret),
      )),
      path: "/decode",
      body: decodeBody,
      status: 422,
      category: "authentication_failed",
      index: 0,
    },
    { name: "selector fails", selector: new StaticPayloadCodecSelector(undefined, new Error(`tenant lookup failed: ${sentinelSecret}`)), path: "/decode", body: decodeBody, status: 500, category: "internal" },
  ];
  for (const testCase of cases) {
    const server = await startTestCodecServer(t, defaultTestConfig(path), testCase.selector);
    const response = await postJson(server, testCase.path, testCase.body);
    assertErrorResponse(response, testCase.status, testCase.category, testCase.index);
    assert.ok(!response.body.includes("SENTINEL"), `${testCase.name}: body ${response.body}`);
    assert.ok(!server.logs.text().includes("SENTINEL"), `${testCase.name}: logs ${server.logs.text()}`);
  }
});

test("the decoded-bytes budget bounds compressible gzip envelopes", async (t) => {
  const { path } = writeRandomKeyFile(t);
  const server = await startTestCodecServer(t, {
    ...defaultTestConfig(path),
    maxDecodedBytesPerRequest: 4 * 1024 * 1024,
  });
  // Each 1 MiB string compresses to about 1 KiB, so five fit easily in the 1 MiB body limit.
  const value: DexValue = { kind: "stringValue", stringValue: "a".repeat(1024 * 1024) };
  const gzipCodec = await sdkCodecFromKeyFile(path, PayloadCompression.PAYLOAD_COMPRESSION_GZIP);
  const envelopes = await encryptWithSdk(gzipCodec, [value, value, value, value, value]);
  assert.ok(envelopes.every((envelope) => envelope.length < 16 * 1024));

  const underBudget = await postJson(server, "/decode", { context: testPayloadContext, envelopes: envelopes.slice(0, 3) });
  assert.equal(underBudget.status, 200);
  assertErrorResponse(await postJson(server, "/decode", { context: testPayloadContext, envelopes }), 413, "limit_exceeded");

  // A failing entry before the budget runs out still reports its own index.
  const withBadEntry = [envelopes[0], "AAE", envelopes[1], envelopes[2], envelopes[3]];
  assertErrorResponse(
    await postJson(server, "/decode", { context: testPayloadContext, envelopes: withBadEntry }),
    422,
    "malformed",
    1,
  );
});

test("the decoded-bytes budget counts JSON escaping of control characters before serializing", async (t) => {
  const { path } = writeRandomKeyFile(t);
  const server = await startTestCodecServer(t, {
    ...defaultTestConfig(path),
    maxDecodedBytesPerRequest: 4 * 1024 * 1024,
  });
  const gzipCodec = await sdkCodecFromKeyFile(path, PayloadCompression.PAYLOAD_COMPRESSION_GZIP);
  // Each \u0001 is one plaintext byte and six JSON bytes.
  const controlCharacters = (count: number): DexValue => ({ kind: "stringValue", stringValue: "\u0001".repeat(count) });

  // Single item: 1 MiB of plaintext becomes 6 MiB of JSON, over the 4 MiB budget.
  const [large] = await encryptWithSdk(gzipCodec, [controlCharacters(1024 * 1024)]);
  assert.ok(large!.length < 16 * 1024);
  assertErrorResponse(
    await postJson(server, "/decode", { context: testPayloadContext, envelopes: [large] }),
    413,
    "limit_exceeded",
  );

  // Cumulative: each 400 KiB item is about 2.4 MiB of JSON; one fits, two do not.
  const [medium] = await encryptWithSdk(gzipCodec, [controlCharacters(400 * 1024)]);
  const single = await postJson(server, "/decode", { context: testPayloadContext, envelopes: [medium] });
  assert.equal(single.status, 200);
  assert.equal(Buffer.byteLength(single.body), `{"values":[]}`.length + 400 * 1024 * 6 + `{"stringValue":""}`.length);
  assertErrorResponse(
    await postJson(server, "/decode", { context: testPayloadContext, envelopes: [medium, medium] }),
    413,
    "limit_exceeded",
  );
});

test("a forbidden caller gets 403", async (t) => {
  const { path } = writeRandomKeyFile(t);
  const server = await startTestCodecServer(
    t,
    defaultTestConfig(path),
    new StaticPayloadCodecSelector(undefined, new CallerForbiddenError()),
  );
  assertErrorResponse(await postJson(server, "/decode", { context: testPayloadContext, envelopes: [] }), 403, "forbidden");
});

test("body size and entry count limits return 413", async (t) => {
  const { path } = writeRandomKeyFile(t);
  const server = await startTestCodecServer(t, {
    ...defaultTestConfig(path),
    maxRequestBodyBytes: 1024,
    maxItemsPerRequest: 3,
  });
  const oversized = { context: testPayloadContext, values: [{ stringValue: "x".repeat(2048) }] };
  assertErrorResponse(await postJson(server, "/encode", oversized), 413, "limit_exceeded");
  // Without Content-Length the limit is enforced while the body streams in.
  assertErrorResponse(
    await postJson(server, "/encode", oversized, { "Content-Length": null, "Transfer-Encoding": "chunked" }),
    413,
    "limit_exceeded",
  );
  const tooMany = { context: testPayloadContext, envelopes: ["", "", "", ""] };
  assertErrorResponse(await postJson(server, "/decode", tooMany), 413, "limit_exceeded");
});

test("an encode value over the envelope limit returns 422 with its index", async (t) => {
  const { path } = writeRandomKeyFile(t);
  const server = await startTestCodecServer(t, {
    ...defaultTestConfig(path),
    payloadSizeLimits: derivePayloadSizeLimits(64, 1024),
  });
  const values = [{ stringValue: "ok" }, { stringValue: "y".repeat(100) }];
  assertErrorResponse(await postJson(server, "/encode", { context: testPayloadContext, values }), 422, "limit_exceeded", 1);
});

test("protocol errors use the error body format", async (t) => {
  const { path } = writeRandomKeyFile(t);
  const server = await startTestCodecServer(t, defaultTestConfig(path));
  const cases: Array<{ name: string; method?: string; path: string; body: unknown; headers?: Record<string, string | null>; status: number; category: string; index?: number }> = [
    { name: "invalid JSON", path: "/decode", body: `{"context":`, status: 400, category: "malformed" },
    { name: "JSON array body", path: "/decode", body: "[]", status: 400, category: "malformed" },
    { name: "missing context", path: "/decode", body: { envelopes: [] }, status: 400, category: "malformed" },
    { name: "missing envelopes", path: "/decode", body: { context: testPayloadContext }, status: 400, category: "malformed" },
    { name: "non-string envelope", path: "/decode", body: { context: testPayloadContext, envelopes: [1] }, status: 400, category: "malformed" },
    { name: "missing values", path: "/encode", body: { context: testPayloadContext }, status: 400, category: "malformed" },
    { name: "GET", method: "GET", path: "/decode", body: undefined, status: 400, category: "malformed" },
    { name: "wrong content type", path: "/decode", body: { context: testPayloadContext, envelopes: [] }, headers: { "Content-Type": "text/plain" }, status: 400, category: "malformed" },
    { name: "unknown path", path: "/other", body: {}, status: 404, category: "malformed" },
    { name: "value not dex.Value JSON", path: "/encode", body: { context: testPayloadContext, values: [{ stringValue: "a" }, { stringValue: 7 }] }, status: 422, category: "malformed", index: 1 },
    { name: "null value", path: "/encode", body: { context: testPayloadContext, values: [{ nullValue: null }] }, status: 422, category: "invalid_plaintext", index: 0 },
    { name: "blob reference", path: "/encode", body: { context: testPayloadContext, values: [{ internalBlobIdForStringValue: "b" }] }, status: 422, category: "invalid_plaintext", index: 0 },
    { name: "reserved encoding", path: "/encode", body: { context: testPayloadContext, values: [{ objValue: { encoding: "dex/1" } }] }, status: 422, category: "invalid_plaintext", index: 0 },
    { name: "unset kind", path: "/encode", body: { context: testPayloadContext, values: [{}] }, status: 422, category: "invalid_plaintext", index: 0 },
  ];
  for (const testCase of cases) {
    const response = await sendRequest(server.baseUrl, testCase.method ?? "POST", testCase.path, testCase.body, testCase.headers);
    assert.equal(response.headers["content-type"], "application/json", testCase.name);
    assertErrorResponse(response, testCase.status, testCase.category, testCase.index);
  }
});

test("the health check needs no credentials", async (t) => {
  const { path } = writeRandomKeyFile(t);
  const server = await startTestCodecServer(t, defaultTestConfig(path));
  const response = await sendRequest(server.baseUrl, "GET", "/healthz", undefined, { Authorization: null });
  assert.equal(response.status, 200);
  assert.deepEqual(JSON.parse(response.body), { status: "ok" });
});

test("logs contain no payloads, keys, or tokens", async (t) => {
  const { path, encodedKey } = writeRandomKeyFile(t);
  const server = await startTestCodecServer(t, defaultTestConfig(path));
  const secretText = "PLAINTEXT-card-4242-secret";
  const encoded = await postJson(server, "/encode", { context: testPayloadContext, values: [{ stringValue: secretText }] });
  assert.equal(encoded.status, 200);
  const { envelopes } = JSON.parse(encoded.body) as { envelopes: string[] };
  const decoded = await postJson(server, "/decode", { context: testPayloadContext, envelopes });
  assert.equal(decoded.status, 200);
  await postJson(server, "/decode", { context: testPayloadContext, envelopes: ["AAE"] });
  await postJson(server, "/decode", { context: testPayloadContext, envelopes }, { Authorization: "Bearer guess" });

  const logs = server.logs.text();
  assert.match(logs, /"path":"\/decode"/);
  assert.match(logs, /"error_category":"malformed"/);
  for (const secret of [secretText, encodedKey, testBearerToken, envelopes[0]!, "guess"]) {
    assert.ok(!logs.includes(secret), `logs contain ${secret}: ${logs}`);
  }
});
