// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { copyFileSync, mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { basename, join } from "node:path";
import test from "node:test";
import * as tls from "node:tls";
import { promisify } from "node:util";

import { createTestCertificates, PlainTlsEndpoint, type TestCertificates } from "./tls-test-certificates.js";

type ChildScenario = "plain" | "replaceDefaultStore" | "mutateAfterImport";

interface ChildVerification {
  /** Authorization of a default context created after SDK import and before any mutation. */
  readonly earlierDefaultContext: readonly boolean[];
  /** Authorization of the SDK context for each endpoint, in the order of the endpoint ports. */
  readonly sdkContext: readonly boolean[];
  /** Authorization of a default context created after the SDK context, in the same order. */
  readonly laterDefaultContext: readonly boolean[];
}

interface TrustStoreFixture {
  readonly directory: string;
  /** CA A, trusted at startup through NODE_EXTRA_CA_CERTS or SSL_CERT_FILE. */
  readonly startup: TestCertificates;
  /** CA B, passed as additionalRootCAsPem. */
  readonly added: TestCertificates;
  /** CA C, the replacement default store or the mutated NODE_EXTRA_CA_CERTS file. */
  readonly replacement: TestCertificates;
  readonly endpoints: readonly PlainTlsEndpoint[];
}

const verificationScript = `
import { readFileSync, writeFileSync } from "node:fs";
import * as tls from "node:tls";
const scenario = process.env.DEX_TLS_SCENARIO;
if (scenario === "replaceDefaultStore") {
  tls.setDefaultCACertificates([readFileSync(process.env.DEX_TLS_REPLACEMENT_CA_FILE, "utf8")]);
}
const { createFlowServiceSecureContext } = await import(process.env.DEX_TLS_MODULE_URL);
const earlierDefaultContext = tls.createSecureContext();
if (scenario === "mutateAfterImport") {
  process.chdir("/");
  writeFileSync(process.env.DEX_TLS_STARTUP_CA_COPY, readFileSync(process.env.DEX_TLS_REPLACEMENT_CA_FILE));
  process.env.NODE_EXTRA_CA_CERTS = process.env.DEX_TLS_REPLACEMENT_CA_FILE;
}
const sdkContext = createFlowServiceSecureContext(readFileSync(process.env.DEX_TLS_ADDITIONAL_CA_FILE));
const laterDefaultContext = tls.createSecureContext();
const verify = (secureContext, port) => new Promise((resolve, reject) => {
  const socket = tls.connect({ host: "127.0.0.1", port, servername: "localhost", secureContext,
    rejectUnauthorized: false }, () => {
    resolve(socket.authorized);
    socket.end();
  });
  socket.on("error", reject);
});
const ports = JSON.parse(process.env.DEX_TLS_PORTS);
const verifyAll = async (secureContext) => {
  const results = [];
  for (const port of ports) results.push(await verify(secureContext, port));
  return results;
};
process.stdout.write(JSON.stringify({
  earlierDefaultContext: await verifyAll(earlierDefaultContext),
  sdkContext: await verifyAll(sdkContext),
  laterDefaultContext: await verifyAll(laterDefaultContext),
}));
`;

// Subtests share one fixture so setup and cleanup run in order on every supported Node.js release.
test("FlowService TLS trust store", async (context) => {
  const fixture = await startTrustStoreFixture();
  try {
    await context.test("additionalRootCAsPem keeps every active default root without leaking added roots",
      () => verifyDefaultRootsKept(fixture));
    await context.test("additionalRootCAsPem does not restore roots that setDefaultCACertificates removed", {
      skip: typeof tls.setDefaultCACertificates !== "function"
        && "tls.setDefaultCACertificates requires Node.js 22.19 or newer",
    }, () => verifyReplacedDefaultStoreKept(fixture));
    await context.test("trust after SDK import ignores later NODE_EXTRA_CA_CERTS, directory, and file changes",
      () => verifyMutationAfterImportIgnored(fixture));
  } finally {
    for (const endpoint of fixture.endpoints) await endpoint.close();
    rmSync(fixture.directory, { recursive: true, force: true });
  }
});

async function startTrustStoreFixture(): Promise<TrustStoreFixture> {
  const directory = mkdtempSync(join(tmpdir(), "dex-typescript-trust-store-"));
  const startup = createTestCertificates(directory, "startup");
  const added = createTestCertificates(directory, "added");
  const replacement = createTestCertificates(directory, "replacement");
  const endpoints = [
    await PlainTlsEndpoint.start(startup),
    await PlainTlsEndpoint.start(added),
    await PlainTlsEndpoint.start(replacement),
  ];
  return { directory, startup, added, replacement, endpoints };
}

async function verifyDefaultRootsKept(fixture: TrustStoreFixture): Promise<void> {
  for (const [name, nodeFlags, environment] of [
    ["NODE_EXTRA_CA_CERTS", [], { NODE_EXTRA_CA_CERTS: fixture.startup.caFile }],
    ["--use-openssl-ca", ["--use-openssl-ca"], { SSL_CERT_FILE: fixture.startup.caFile }],
  ] as const) {
    const unrelatedAdded = await verifyInChildProcess(fixture, "plain", nodeFlags, environment, fixture.replacement.caFile);
    assert.deepEqual(unrelatedAdded.sdkContext, [true, false, true],
      `${name}: the startup root stays trusted after adding an unrelated CA`);
    const added = await verifyInChildProcess(fixture, "plain", nodeFlags, environment, fixture.added.caFile);
    assert.deepEqual(added.sdkContext, [true, true, false], `${name}: the added CA is trusted`);
    assert.deepEqual(added.laterDefaultContext, [true, false, false], `${name}: the added CA does not leak`);
    const duplicate = await verifyInChildProcess(fixture, "plain", nodeFlags, environment, fixture.startup.caFile);
    assert.deepEqual(duplicate.sdkContext, [true, false, false],
      `${name}: adding a CA that the default store already trusts is harmless`);
  }
  const withoutStartupRoot = await verifyInChildProcess(fixture, "plain", [], {}, fixture.added.caFile);
  assert.deepEqual(withoutStartupRoot.sdkContext, [false, true, false],
    "the startup endpoint is untrusted without its CA in the default store");
}

async function verifyReplacedDefaultStoreKept(fixture: TrustStoreFixture): Promise<void> {
  const result = await verifyInChildProcess(fixture, "replaceDefaultStore", [],
    { NODE_EXTRA_CA_CERTS: fixture.startup.caFile }, fixture.added.caFile);
  assert.deepEqual(result.laterDefaultContext, [false, false, true], "the replacement store trusts only CA C");
  assert.deepEqual(result.sdkContext, [false, true, true], "the SDK context trusts B and C but not the removed A");
}

async function verifyMutationAfterImportIgnored(fixture: TrustStoreFixture): Promise<void> {
  // The relative startup path and its file copy let the child change both after import.
  // Node.js 22.15+ loads NODE_EXTRA_CA_CERTS on first TLS use, so the child uses TLS before mutating.
  const startupCopy = join(fixture.directory, "startup-ca-copy.pem");
  copyFileSync(fixture.startup.caFile, startupCopy);
  const result = await verifyInChildProcess(fixture, "mutateAfterImport", [],
    { NODE_EXTRA_CA_CERTS: basename(startupCopy), DEX_TLS_STARTUP_CA_COPY: startupCopy }, fixture.added.caFile);
  assert.deepEqual(result.earlierDefaultContext, [true, false, false], "the startup store trusts A");
  assert.deepEqual(result.sdkContext, [true, true, false], "the SDK keeps A, adds B, and never gains C");
}

// Default roots come from process flags and environment, so each case needs a fresh Node.js process.
async function verifyInChildProcess(
  fixture: TrustStoreFixture,
  scenario: ChildScenario,
  nodeFlags: readonly string[],
  environment: Readonly<Record<string, string>>,
  additionalCaFile: string,
): Promise<ChildVerification> {
  const { stdout } = await promisify(execFile)(
    process.execPath,
    [...nodeFlags, "--input-type=module", "-e", verificationScript],
    {
      cwd: fixture.directory,
      env: {
        ...withoutTrustEnvironment(process.env),
        ...environment,
        DEX_TLS_SCENARIO: scenario,
        DEX_TLS_MODULE_URL: new URL("../src/flow-service-tls.js", import.meta.url).href,
        DEX_TLS_ADDITIONAL_CA_FILE: additionalCaFile,
        DEX_TLS_REPLACEMENT_CA_FILE: fixture.replacement.caFile,
        DEX_TLS_PORTS: JSON.stringify(fixture.endpoints.map((endpoint) => endpoint.port)),
      },
    },
  );
  return JSON.parse(stdout) as ChildVerification;
}

function withoutTrustEnvironment(environment: NodeJS.ProcessEnv): NodeJS.ProcessEnv {
  const trusted = { ...environment };
  for (const name of ["NODE_EXTRA_CA_CERTS", "SSL_CERT_FILE", "SSL_CERT_DIR"]) delete trusted[name];
  return trusted;
}
