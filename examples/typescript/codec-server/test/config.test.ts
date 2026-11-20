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
import { test } from "node:test";

import { createPayloadCodecHttpServer } from "../src/codec-server.ts";
import { loadCodecServerConfig } from "../src/config.ts";
import { listenCodecHttpServer } from "../src/http-listener.ts";
import { KeyFileJsonPayloadEncryptionKeyProvider } from "../src/keys.ts";
import {
  CapturedLogs,
  defaultTestConfig,
  postJson,
  sendRequest,
  testAllowedOrigin,
  testBearerToken,
  testPayloadContext,
} from "./test-support.ts";

const validKeysJson = JSON.stringify({ current_key_id: "k1", keys: { k1: randomBytes(32).toString("base64") } });

function validEnvironment(): Record<string, string> {
  return {
    DEX_CODEC_SERVER_KEYS_JSON: validKeysJson,
    DEX_CODEC_SERVER_ALLOWED_ORIGINS: testAllowedOrigin,
    DEX_CODEC_SERVER_AUTH_MODE: "bearer-token",
    DEX_CODEC_SERVER_BEARER_TOKEN: testBearerToken,
  };
}

test("a valid environment loads with spec defaults", () => {
  const config = loadCodecServerConfig([], validEnvironment());
  assert.equal(config.listenPort, 8080);
  assert.equal(config.listenHost, undefined);
  assert.deepEqual(config.allowedOrigins, [testAllowedOrigin]);
  assert.equal(config.payloadSizeLimits.maxEnvelopeBytes, 16_842_752);
  assert.equal(config.payloadSizeLimits.maxEncodedTextBytes, 22_457_004);
  assert.equal(config.maxDecodedBytesPerRequest, 64 * 1024 * 1024);
  assert.equal(config.shutdownTimeoutMs, 15_000);

  const flagged = loadCodecServerConfig(
    ["--listen-address", "127.0.0.1:9090", "--allow-credentials", "--shutdown-timeout", "500ms"],
    { ...validEnvironment(), DEX_CODEC_SERVER_ALLOWED_ORIGINS: "HTTPS://Dex-Web.example.com:443, http://localhost:8802" },
  );
  assert.equal(flagged.listenHost, "127.0.0.1");
  assert.equal(flagged.listenPort, 9090);
  assert.equal(flagged.allowCredentials, true);
  assert.equal(flagged.shutdownTimeoutMs, 500);
  assert.deepEqual(flagged.allowedOrigins, ["https://dex-web.example.com", "http://localhost:8802"]);
});

test("unsafe or incomplete configuration is rejected", () => {
  const cases: Array<[string, Record<string, string | undefined>, readonly string[]]> = [
    ["no auth mode", { DEX_CODEC_SERVER_AUTH_MODE: undefined }, []],
    ["unknown auth mode", { DEX_CODEC_SERVER_AUTH_MODE: "none" }, []],
    ["short bearer token", { DEX_CODEC_SERVER_BEARER_TOKEN: "short" }, []],
    ["no bearer token", { DEX_CODEC_SERVER_BEARER_TOKEN: undefined }, []],
    ["trusted header mode without a header", { DEX_CODEC_SERVER_AUTH_MODE: "trusted-proxy-header" }, []],
    ["wildcard origin", { DEX_CODEC_SERVER_ALLOWED_ORIGINS: "*" }, []],
    ["wildcard subdomain origin", { DEX_CODEC_SERVER_ALLOWED_ORIGINS: "https://*.example.com" }, []],
    ["origin with a path", { DEX_CODEC_SERVER_ALLOWED_ORIGINS: "https://dex-web.example.com/app" }, []],
    ["no origins", { DEX_CODEC_SERVER_ALLOWED_ORIGINS: undefined }, []],
    ["no key source", { DEX_CODEC_SERVER_KEYS_JSON: undefined }, []],
    ["two key sources", { DEX_CODEC_SERVER_KEY_FILE: "/etc/dex/payload-keys.json" }, []],
    ["zero body limit", { DEX_CODEC_SERVER_MAX_REQUEST_BODY_BYTES: "0" }, []],
    ["negative item limit", { DEX_CODEC_SERVER_MAX_ITEMS_PER_REQUEST: "-1" }, []],
    ["zero decoded-bytes limit", { DEX_CODEC_SERVER_MAX_DECODED_BYTES_PER_REQUEST: "0" }, []],
    ["zero envelope limit", { DEX_CODEC_SERVER_MAX_ENVELOPE_BYTES: "0" }, []],
    ["non-integer plaintext limit", { DEX_CODEC_SERVER_MAX_PLAINTEXT_BYTES: "1e6" }, []],
    ["zero shutdown timeout", { DEX_CODEC_SERVER_SHUTDOWN_TIMEOUT: "0s" }, []],
    ["unitless shutdown timeout", { DEX_CODEC_SERVER_SHUTDOWN_TIMEOUT: "15" }, []],
    ["invalid listen address", { DEX_CODEC_SERVER_LISTEN_ADDRESS: "8080" }, []],
    ["secret flag does not exist", {}, ["--bearer-token", "x"]],
  ];
  for (const [name, overrides, argumentList] of cases) {
    const environment: Record<string, string | undefined> = { ...validEnvironment(), ...overrides };
    assert.throws(() => loadCodecServerConfig(argumentList, environment), Error, name);
  }
});

test("configuration errors never echo the bearer token or keys", () => {
  const token = "short-secret-token";
  try {
    loadCodecServerConfig([], { ...validEnvironment(), DEX_CODEC_SERVER_BEARER_TOKEN: token });
    assert.fail("expected a configuration error");
  } catch (failure) {
    assert.ok(failure instanceof Error);
    assert.ok(!failure.message.includes(token));
  }
  const key = Buffer.alloc(31, 7).toString("base64");
  assert.throws(
    () => KeyFileJsonPayloadEncryptionKeyProvider.fromJson(JSON.stringify({ current_key_id: "k1", keys: { k1: key } })),
    (failure: Error) => !failure.message.includes(key),
  );
});

test("DEX_CODEC_SERVER_KEYS_JSON follows the key file rules", async () => {
  const invalid = [
    "not json",
    "[]",
    JSON.stringify({ current_key_id: "k2", keys: { k1: randomBytes(32).toString("base64") } }),
    JSON.stringify({ current_key_id: "k 1", keys: { "k 1": randomBytes(32).toString("base64") } }),
    JSON.stringify({ current_key_id: "k1", keys: { k1: randomBytes(32).toString("base64").replace(/=$/, "") } }),
  ];
  for (const keysJson of invalid) {
    assert.throws(() => KeyFileJsonPayloadEncryptionKeyProvider.fromJson(keysJson), Error, keysJson);
  }
  const provider = KeyFileJsonPayloadEncryptionKeyProvider.fromJson(validKeysJson);
  const context = { flowType: "", flowId: "order-1" };
  assert.equal(await provider.currentKeyId(context), "k1");
  assert.equal((await provider.key(context, "k1")).length, 32);
  await assert.rejects(provider.key(context, "k9"));
});

test("a server configured from DEX_CODEC_SERVER_KEYS_JSON encodes and decodes", async (t) => {
  const config = { ...defaultTestConfig(""), keyFilePath: undefined, keysJson: validKeysJson };
  const logs = new CapturedLogs();
  const codecServer = await createPayloadCodecHttpServer(config, logs.logger);
  const listener = await listenCodecHttpServer(codecServer.handleRequest, "127.0.0.1", 0, 1000, logs.logger);
  t.after(() => listener.shutDownGracefully());
  const server = { baseUrl: `http://127.0.0.1:${listener.address.port}`, logs };
  const encoded = await postJson(server, "/encode", { context: testPayloadContext, values: [{ intValue: "42" }] });
  assert.equal(encoded.status, 200, encoded.body);
  const decoded = await postJson(server, "/decode", {
    context: testPayloadContext,
    envelopes: (JSON.parse(encoded.body) as { envelopes: string[] }).envelopes,
  });
  assert.deepEqual(JSON.parse(decoded.body), { values: [{ intValue: "42" }] });
});

test("graceful shutdown lets an in-flight request finish", async () => {
  const logs = new CapturedLogs();
  const listener = await listenCodecHttpServer((_request, response) => {
    setTimeout(() => {
      response.writeHead(200, { "Content-Type": "application/json" });
      response.end(`{"status":"ok"}`);
    }, 200);
  }, "127.0.0.1", 0, 2000, logs.logger);
  const inFlight = sendRequest(`http://127.0.0.1:${listener.address.port}`, "GET", "/slow", undefined);
  await new Promise((resolve) => setTimeout(resolve, 50));
  const finished = await listener.shutDownGracefully();
  assert.equal((await inFlight).status, 200);
  assert.equal(finished, true);
  assert.equal(listener.server.listening, false);
});
