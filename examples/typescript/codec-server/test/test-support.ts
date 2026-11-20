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
import { randomBytes } from "node:crypto";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { request as httpRequest, type IncomingHttpHeaders } from "node:http";
import { tmpdir } from "node:os";
import { join } from "node:path";
import type { TestContext } from "node:test";

import { createRequestAuthenticator, type CallerPayloadCodecSelector } from "../src/auth.ts";
import { CorsOriginAllowList } from "../src/cors.ts";
import {
  createPayloadCodecHttpServer,
  PayloadCodecHttpServer,
  type CodecServerLogger,
} from "../src/codec-server.ts";
import {
  authModeBearerToken,
  defaultMaxDecodedBytesPerRequest,
  defaultMaxEnvelopeBytes,
  defaultMaxPlaintextBytes,
  derivePayloadSizeLimits,
  validateCodecServerConfig,
  type CodecServerConfig,
} from "../src/config.ts";
import { listenCodecHttpServer } from "../src/http-listener.ts";
import type { DexValue } from "../src/value-protobuf.ts";

export const testAllowedOrigin = "https://dex-web.example.com";
export const testBearerToken = "test-bearer-token-0123456789abcdef";
export const testPayloadContext = { flow_type: "OrderFlow", flow_id: "order-1001" };

const vectorsUrl = new URL("../../../../protos/conformance/payload-encryption/vectors.json", import.meta.url);

/** The parts of protos/conformance/payload-encryption/vectors.json that these tests read. */
export interface GoldenVectorFile {
  readonly keyFile: unknown;
  readonly limits: GoldenLimits;
  readonly positive: readonly GoldenVectorCase[];
  readonly negative: readonly GoldenVectorCase[];
}

export interface GoldenLimits {
  readonly maxEnvelopeBytes: number;
  readonly maxEncodedTextBytes: number;
  readonly maxDataBytes: number;
  readonly maxPlaintextBytes: number;
}

export interface GoldenVectorCase {
  readonly name: string;
  readonly input: { readonly value?: { readonly objValue?: { readonly encoding?: string; readonly payload?: string } }; readonly text?: string };
  readonly limits?: GoldenLimits;
  readonly expectedPlaintext?: string;
  readonly expectedValue?: unknown;
  readonly expectedError?: string;
}

export function readGoldenVectors(): GoldenVectorFile {
  return JSON.parse(readFileSync(vectorsUrl, "utf8")) as GoldenVectorFile;
}

/** @returns What a caller sends to /decode: the dex/1 payload or the text after the prefix. */
export function envelopeTextOf(vectorCase: GoldenVectorCase): string | undefined {
  if (vectorCase.input.text !== undefined) {
    return vectorCase.input.text.startsWith("dex-encoded:")
      ? vectorCase.input.text.slice("dex-encoded:".length)
      : undefined;
  }
  const objValue = vectorCase.input.value?.objValue;
  if (objValue?.encoding !== "dex/1") {
    return undefined;
  }
  return objValue.payload ?? "";
}

export function writeKeyFile(testContext: TestContext, keyFile: unknown): string {
  const directory = mkdtempSync(join(tmpdir(), "dex-codec-server-test-"));
  const path = join(directory, "payload-keys.json");
  writeFileSync(path, JSON.stringify(keyFile), { mode: 0o600 });
  testContext.after(() => rmSync(directory, { recursive: true, force: true }));
  return path;
}

export function writeRandomKeyFile(testContext: TestContext): { path: string; encodedKey: string } {
  const encodedKey = randomBytes(32).toString("base64");
  return { path: writeKeyFile(testContext, { current_key_id: "k1", keys: { k1: encodedKey } }), encodedKey };
}

export function defaultTestConfig(keyFilePath: string): CodecServerConfig {
  return {
    listenHost: "127.0.0.1",
    listenPort: 0,
    keyFilePath,
    keysJson: undefined,
    allowedOrigins: [testAllowedOrigin],
    allowCredentials: false,
    authMode: authModeBearerToken,
    bearerToken: testBearerToken,
    trustedIdentityHeader: undefined,
    maxRequestBodyBytes: 1024 * 1024,
    maxItemsPerRequest: 100,
    maxDecodedBytesPerRequest: defaultMaxDecodedBytesPerRequest,
    payloadSizeLimits: derivePayloadSizeLimits(defaultMaxEnvelopeBytes, defaultMaxPlaintextBytes),
    shutdownTimeoutMs: 1000,
  };
}

/** Collects log records as JSON lines, as the production logger writes them. */
export class CapturedLogs {
  public readonly lines: string[] = [];
  public readonly logger: CodecServerLogger = (level, message, fields) => {
    this.lines.push(JSON.stringify({ level, message, ...fields }));
  };

  public text(): string {
    return this.lines.join("\n");
  }
}

export interface TestCodecServer {
  readonly baseUrl: string;
  readonly logs: CapturedLogs;
}

export async function startTestCodecServer(
  testContext: TestContext,
  config: CodecServerConfig,
  codecSelector?: CallerPayloadCodecSelector,
): Promise<TestCodecServer> {
  validateCodecServerConfig(config);
  const logs = new CapturedLogs();
  const server = codecSelector === undefined
    ? await createPayloadCodecHttpServer(config, logs.logger)
    : new PayloadCodecHttpServer({
      authenticator: createRequestAuthenticator(config),
      codecSelector,
      corsOrigins: new CorsOriginAllowList(config.allowedOrigins, config.allowCredentials),
      limits: config.payloadSizeLimits,
      maxRequestBodyBytes: config.maxRequestBodyBytes,
      maxItemsPerRequest: config.maxItemsPerRequest,
      maxDecodedBytesPerRequest: config.maxDecodedBytesPerRequest,
      logger: logs.logger,
    });
  const listener = await listenCodecHttpServer(server.handleRequest, "127.0.0.1", 0, 1000, logs.logger);
  testContext.after(() => listener.shutDownGracefully());
  return { baseUrl: `http://127.0.0.1:${listener.address.port}`, logs };
}

export interface TestResponse {
  readonly status: number;
  readonly headers: IncomingHttpHeaders;
  readonly body: string;
}

/**
 * Sends a request with the test bearer token and JSON content type.
 * A header set to null is removed.
 */
export function sendRequest(
  baseUrl: string,
  method: string,
  path: string,
  body: unknown,
  headers: Record<string, string | string[] | null> = {},
): Promise<TestResponse> {
  const encoded = body === undefined
    ? Buffer.alloc(0)
    : Buffer.isBuffer(body)
    ? body
    : Buffer.from(typeof body === "string" ? body : JSON.stringify(body));
  const requestHeaders: Record<string, string | string[]> = {
    "Content-Type": "application/json",
    Authorization: `Bearer ${testBearerToken}`,
    "Content-Length": String(encoded.length),
  };
  for (const [name, value] of Object.entries(headers)) {
    const existing = Object.keys(requestHeaders).find((key) => key.toLowerCase() === name.toLowerCase());
    if (existing !== undefined) {
      delete requestHeaders[existing];
    }
    if (value !== null) {
      requestHeaders[name] = value;
    }
  }
  return new Promise((resolve, reject) => {
    const outgoing = httpRequest(`${baseUrl}${path}`, { method, headers: requestHeaders }, (response) => {
      const chunks: Buffer[] = [];
      response.on("data", (chunk: Buffer) => chunks.push(chunk));
      response.on("end", () => resolve({
        status: response.statusCode ?? 0,
        headers: response.headers,
        body: Buffer.concat(chunks).toString("utf8"),
      }));
      response.on("error", reject);
    });
    outgoing.on("error", reject);
    outgoing.end(encoded);
  });
}

export function postJson(
  server: TestCodecServer,
  path: string,
  body: unknown,
  headers: Record<string, string | string[] | null> = {},
): Promise<TestResponse> {
  return sendRequest(server.baseUrl, "POST", path, body, headers);
}

/** An intValue field whose varint overflows 64 bits; not a valid dex.Value. */
export const overflowingIntValuePlaintext = Uint8Array.from([
  0x28, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x02,
]);

/** Compares Values, keeping NaN equal to NaN and the sign of zero. */
export function assertSameDexValue(actual: DexValue, expected: DexValue, label: string): void {
  if (actual.kind === "doubleValue" && expected.kind === "doubleValue") {
    assert.ok(Object.is(actual.doubleValue, expected.doubleValue), `${label}: ${actual.doubleValue} vs ${expected.doubleValue}`);
    return;
  }
  assert.deepEqual(actual, expected, label);
}

export function assertErrorResponse(
  response: TestResponse,
  status: number,
  category: string,
  index?: number,
): void {
  assert.equal(response.status, status, `status ${response.status}, body ${response.body}`);
  const parsed = JSON.parse(response.body) as { error: { category: string; message: string; index?: number } };
  assert.equal(parsed.error.category, category, response.body);
  assert.ok(parsed.error.message.length > 0, response.body);
  assert.equal(parsed.error.index, index, response.body);
}
