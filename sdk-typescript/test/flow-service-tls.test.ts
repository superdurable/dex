// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import test from "node:test";

import { Client, Registry, Worker, type BlobCache } from "../src/index.js";
import { createFlowServiceChannel } from "../src/flow-service-tls.js";

const unsupportedAddress = "serverAddress as host:port, dns:///host:port, or dns://authority/host:port";
const blobCache = {} as BlobCache;

test("omitted flowServiceTls keeps the plaintext target and options", () => {
  for (const serverAddress of ["localhost:8801", "unix:///tmp/dex.sock", "DNS:///localhost:443"]) {
    const channel = createFlowServiceChannel(serverAddress, undefined);
    assert.equal(channel.target, serverAddress);
    assert.deepEqual(channel.channelOptions, {});
    assert.equal(channel.channelCredentials._isSecure(), false);
  }
});

test("flowServiceTls accepts host:port and DNS targets", () => {
  for (const [serverAddress, target] of [
    ["localhost:443", "localhost:443"],
    ["dex.example.com:443", "dex.example.com:443"],
    ["127.0.0.1:443", "127.0.0.1:443"],
    ["[::1]:443", "[::1]:443"],
    ["dns:///localhost:443", "dns:///localhost:443"],
    ["DNS:///localhost:443", "dns:///localhost:443"],
    ["dns://127.0.0.1:53/localhost:443", "dns://127.0.0.1:53/localhost:443"],
  ] as const) {
    const channel = createFlowServiceChannel(serverAddress, {});
    assert.equal(channel.target, target, serverAddress);
    assert.equal(channel.channelCredentials._isSecure(), true);
    assert.deepEqual(channel.channelOptions, {});
  }
});

test("flowServiceTls rejects resolver schemes, paths, and malformed addresses", () => {
  for (const serverAddress of [
    "unix:8443", "Unix:8443", "passthrough:443", "Passthrough:443", "dns:443", "unix:///tmp/dex.sock",
    "UNIX:///tmp/dex.sock", "unix:/tmp/dex.sock", "dns:///localhost/path:443", "https://localhost:443",
    "ipv4:127.0.0.1:443", "localhost", "::1:443", "[::1]", "local host:443", "dns:/localhost:443",
    "dns://bad authority/localhost:443", "user@localhost:443", "[localhost]:443", "[127.0.0.1]:443",
    "dns:///[localhost]:443",
  ]) {
    assert.throws(() => createFlowServiceChannel(serverAddress, {}), (failure: unknown) =>
      failure instanceof TypeError
      && failure.message === `dex: flowServiceTls requires ${unsupportedAddress}, got ${JSON.stringify(serverAddress)}`,
    serverAddress);
  }
  for (const serverAddress of [
    "localhost:0", "localhost:65536", "localhost:https", "localhost:", "localhost:+443", "localhost:-1", "[::1]:+443",
  ]) {
    assert.throws(() => createFlowServiceChannel(serverAddress, {}),
      new TypeError(`dex: flowServiceTls requires serverAddress port 1-65535, got ${JSON.stringify(serverAddress)}`));
  }
});

test("flowServiceTls validates serverName and sends it as the target name and authority", () => {
  for (const serverName of ["localhost:443", "dex.example.com/path", "bad name", "user@dex.example.com"]) {
    assert.throws(() => createFlowServiceChannel("localhost:443", { serverName }),
      new TypeError(`dex: flowServiceTls.serverName must be a DNS name or IP address, got ${JSON.stringify(serverName)}`));
  }
  for (const serverName of ["dex.example.com", "127.0.0.1", "::1"]) {
    assert.deepEqual(createFlowServiceChannel("127.0.0.1:443", { serverName }).channelOptions, {
      "grpc.ssl_target_name_override": serverName,
      "grpc.default_authority": serverName,
    });
  }
  assert.deepEqual(createFlowServiceChannel("localhost:443", { serverName: "" }).channelOptions, {});
});

test("flowServiceTls requires a parsable certificate in a nonempty root CA bundle", () => {
  const unparsable = "-----BEGIN CERTIFICATE-----\nAAAA\n-----END CERTIFICATE-----\n";
  for (const additionalRootCAsPem of ["invalid", unparsable, new TextEncoder().encode("invalid")]) {
    assert.throws(() => createFlowServiceChannel("localhost:443", { additionalRootCAsPem }),
      new TypeError("dex: flowServiceTls.additionalRootCAsPem contains no PEM certificate"));
  }
  for (const additionalRootCAsPem of ["", new Uint8Array()]) {
    assert.equal(createFlowServiceChannel("localhost:443", { additionalRootCAsPem }).channelCredentials._isSecure(), true);
  }
});

test("Client and Worker constructors reject invalid flowServiceTls settings", () => {
  const registry = new Registry([]);
  const flowServiceTls = { additionalRootCAsPem: "invalid" };
  assert.throws(() => new Client(registry, blobCache, { serverAddress: "localhost:443", flowServiceTls }),
    /flowServiceTls\.additionalRootCAsPem/);
  assert.throws(() => new Worker(registry, blobCache, { serverAddress: "localhost:443", flowServiceTls, pull: {} }),
    /flowServiceTls\.additionalRootCAsPem/);
  assert.throws(() => new Client(registry, blobCache, { serverAddress: "https://localhost:443", flowServiceTls: {} }),
    /serverAddress as host:port/);
  assert.throws(() => new Worker(registry, blobCache, { flowServiceTls: { serverName: "localhost:443" }, pull: {} }),
    /flowServiceTls\.serverName/);
});
