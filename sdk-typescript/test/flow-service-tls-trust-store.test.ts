// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import test from "node:test";

import { createTestCertificates, PlainTlsEndpoint } from "./tls-test-certificates.js";

interface VerificationResult {
  readonly authorized: boolean;
  readonly error: string;
}

interface ChildVerification {
  readonly sdkContext: VerificationResult;
  readonly laterDefaultContext: VerificationResult;
}

const verificationScript = `
import { readFileSync } from "node:fs";
import { connect, createSecureContext } from "node:tls";
const { createFlowServiceSecureContext } = await import(process.env.DEX_TLS_MODULE_URL);
const sdkContext = createFlowServiceSecureContext(readFileSync(process.env.DEX_TLS_ADDITIONAL_CA_FILE));
const verify = (secureContext) => new Promise((resolve, reject) => {
  const socket = connect({ host: "127.0.0.1", port: Number(process.env.DEX_TLS_PORT), servername: "localhost",
    secureContext, rejectUnauthorized: false }, () => {
    resolve({ authorized: socket.authorized, error: String(socket.authorizationError ?? "") });
    socket.end();
  });
  socket.on("error", reject);
});
process.stdout.write(JSON.stringify({
  sdkContext: await verify(sdkContext),
  laterDefaultContext: await verify(createSecureContext()),
}));
`;

// Default roots come from process flags and environment, so each case needs a fresh Node.js process.
async function verifyInChildProcess(
  nodeFlags: readonly string[],
  environment: Readonly<Record<string, string>>,
  port: number,
  additionalCaFile: string,
): Promise<ChildVerification> {
  const { stdout } = await promisify(execFile)(
    process.execPath,
    [...nodeFlags, "--input-type=module", "-e", verificationScript],
    {
      env: {
        ...withoutTrustEnvironment(process.env),
        ...environment,
        DEX_TLS_MODULE_URL: new URL("../src/flow-service-tls.js", import.meta.url).href,
        DEX_TLS_ADDITIONAL_CA_FILE: additionalCaFile,
        DEX_TLS_PORT: String(port),
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

test("additionalRootCAsPem keeps every active default root without leaking added roots", async () => {
  const directory = mkdtempSync(join(tmpdir(), "dex-typescript-trust-store-"));
  const defaultStore = createTestCertificates(directory, "default-store");
  const unrelated = createTestCertificates(directory, "unrelated");
  const added = createTestCertificates(directory, "added");
  const defaultTrustedEndpoint = await PlainTlsEndpoint.start(defaultStore);
  const addedTrustedEndpoint = await PlainTlsEndpoint.start(added);
  try {
    for (const [name, nodeFlags, environment] of [
      ["NODE_EXTRA_CA_CERTS", [], { NODE_EXTRA_CA_CERTS: defaultStore.caFile }],
      ["--use-openssl-ca", ["--use-openssl-ca"], { SSL_CERT_FILE: defaultStore.caFile }],
    ] as const) {
      const keptDefault = await verifyInChildProcess(nodeFlags, environment, defaultTrustedEndpoint.port, unrelated.caFile);
      assert.deepEqual(keptDefault.sdkContext, { authorized: true, error: "" },
        `${name}: a default root stays trusted after adding an unrelated CA`);
      const trustedAdded = await verifyInChildProcess(nodeFlags, environment, addedTrustedEndpoint.port, added.caFile);
      assert.deepEqual(trustedAdded.sdkContext, { authorized: true, error: "" }, `${name}: the added CA is trusted`);
      assert.equal(trustedAdded.laterDefaultContext.authorized, false, `${name}: the added CA does not leak`);
      const duplicateDefault = await verifyInChildProcess(nodeFlags, environment, defaultTrustedEndpoint.port,
        defaultStore.caFile);
      assert.deepEqual(duplicateDefault.sdkContext, { authorized: true, error: "" },
        `${name}: adding a CA that the default store already trusts is harmless`);
    }
    const withoutDefaultRoot = await verifyInChildProcess([], {}, defaultTrustedEndpoint.port, unrelated.caFile);
    assert.equal(withoutDefaultRoot.sdkContext.authorized, false, "the endpoint is untrusted without the default-store CA");
  } finally {
    await defaultTrustedEndpoint.close();
    await addedTrustedEndpoint.close();
    rmSync(directory, { recursive: true, force: true });
  }
});
