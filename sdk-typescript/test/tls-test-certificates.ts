// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { execFileSync } from "node:child_process";
import { readFileSync, writeFileSync } from "node:fs";
import type { AddressInfo } from "node:net";
import { join } from "node:path";
import * as tls from "node:tls";

export interface TestCertificates {
  readonly caFile: string;
  readonly caPem: string;
  readonly certificatePem: string;
  readonly privateKeyPem: string;
}

// Node.js cannot issue X.509 certificates, so the test uses the openssl CLI.
export function createTestCertificates(directory: string, name: string): TestCertificates {
  const file = (suffix: string) => join(directory, `${name}-${suffix}`);
  writeFileSync(file("extensions.cnf"), [
    "[dex_test_ca]",
    "basicConstraints = critical, CA:TRUE",
    "keyUsage = critical, keyCertSign",
    "[dex_test_server]",
    "basicConstraints = CA:FALSE",
    "keyUsage = critical, digitalSignature",
    "extendedKeyUsage = serverAuth",
    "subjectAltName = DNS:localhost, IP:127.0.0.1, IP:::1",
    "",
  ].join("\n"));
  const openssl = (...arguments_: string[]) => execFileSync("openssl", arguments_, { stdio: "pipe" });
  openssl("ecparam", "-name", "prime256v1", "-genkey", "-noout", "-out", file("ca.key"));
  // Distinct CA names keep verification from matching the wrong issuer.
  openssl("req", "-new", "-key", file("ca.key"), "-subj", `/CN=Dex test CA ${name}`, "-out", file("ca.csr"));
  openssl("x509", "-req", "-in", file("ca.csr"), "-signkey", file("ca.key"), "-days", "1",
    "-extfile", file("extensions.cnf"), "-extensions", "dex_test_ca", "-out", file("ca.pem"));
  openssl("ecparam", "-name", "prime256v1", "-genkey", "-noout", "-out", file("server.key"));
  openssl("req", "-new", "-key", file("server.key"), "-subj", "/CN=localhost", "-out", file("server.csr"));
  openssl("x509", "-req", "-in", file("server.csr"), "-CA", file("ca.pem"), "-CAkey", file("ca.key"),
    "-set_serial", "2", "-days", "1", "-extfile", file("extensions.cnf"), "-extensions", "dex_test_server",
    "-out", file("server.pem"));
  return {
    caFile: file("ca.pem"),
    caPem: readFileSync(file("ca.pem"), "utf8"),
    certificatePem: readFileSync(file("server.pem"), "utf8"),
    privateKeyPem: readFileSync(file("server.key"), "utf8"),
  };
}

/** Accepts TLS connections with one test certificate and closes them after the handshake. */
export class PlainTlsEndpoint {
  private readonly server: tls.Server;

  private constructor(certificates: TestCertificates) {
    this.server = tls.createServer({ cert: certificates.certificatePem, key: certificates.privateKeyPem },
      (socket) => socket.end());
  }

  public static async start(certificates: TestCertificates): Promise<PlainTlsEndpoint> {
    const endpoint = new PlainTlsEndpoint(certificates);
    await new Promise<void>((resolve) => endpoint.server.listen(0, "127.0.0.1", resolve));
    return endpoint;
  }

  public get port(): number {
    return (this.server.address() as AddressInfo).port;
  }

  public async close(): Promise<void> {
    await new Promise<void>((resolve) => this.server.close(() => resolve()));
  }
}
