// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { X509Certificate } from "node:crypto";
import { isIPv6 } from "node:net";
import * as tls from "node:tls";

import { credentials, type ChannelCredentials, type ChannelOptions } from "@grpc/grpc-js";

import type { FlowServiceTlsOptions } from "./options.js";

/** Target, transport credentials, and channel options for one FlowService gRPC client. */
export interface FlowServiceChannel {
  readonly target: string;
  readonly channelCredentials: ChannelCredentials;
  readonly channelOptions: ChannelOptions;
}

// Go and grpc-js resolver schemes; grpc-js lacks passthrough and would treat it as a DNS host.
const resolverSchemes = new Set(["dns", "ipv4", "ipv6", "passthrough", "unix", "unix-abstract"]);
const pemCertificatePattern = /-----BEGIN CERTIFICATE-----[\s\S]*?-----END CERTIFICATE-----/g;

/**
 * Builds the FlowService channel for a Client or Worker connection.
 * @param serverAddress - Dex FlowService target from the Client or Worker options.
 * @param tlsOptions - TLS settings; undefined keeps the plaintext channel unchanged.
 * @returns The plaintext target and insecure credentials when tlsOptions is undefined;
 * otherwise the TLS target with a lowercased dns scheme, TLS credentials, and the
 * server name override channel options.
 * @throws {TypeError} When TLS is set and the address, server name, or root CA bundle is invalid.
 */
export function createFlowServiceChannel(
  serverAddress: string,
  tlsOptions: FlowServiceTlsOptions | undefined,
): FlowServiceChannel {
  if (tlsOptions === undefined) {
    return { target: serverAddress, channelCredentials: credentials.createInsecure(), channelOptions: {} };
  }
  const target = flowServiceTlsTarget(serverAddress);
  const serverName = tlsOptions.serverName ?? "";
  if (serverName !== "" && !isValidFlowServiceTlsName(serverName)) {
    throw new TypeError(`dex: flowServiceTls.serverName must be a DNS name or IP address, got ${JSON.stringify(serverName)}`);
  }
  const secureContext = tls.createSecureContext({
    minVersion: "TLSv1.2",
    ...additionalRootCertificateAuthorities(tlsOptions.additionalRootCAsPem),
  });
  // ssl_target_name_override covers SNI and verification; default_authority sets :authority.
  const channelOptions: ChannelOptions = serverName === ""
    ? {}
    : { "grpc.ssl_target_name_override": serverName, "grpc.default_authority": serverName };
  return { target, channelCredentials: credentials.createFromSecureContext(secureContext), channelOptions };
}

function flowServiceTlsTarget(serverAddress: string): string {
  let target = serverAddress;
  let endpoint = serverAddress;
  const schemeEnd = serverAddress.indexOf("://");
  if (schemeEnd >= 0 && serverAddress.slice(0, schemeEnd).toLowerCase() === "dns") {
    const dnsTarget = serverAddress.slice(schemeEnd + 3);
    const authorityEnd = dnsTarget.indexOf("/");
    if (authorityEnd < 0 || /[ \t\r\n?#@]/.test(dnsTarget.slice(0, authorityEnd))
      || /[/?#]/.test(dnsTarget.slice(authorityEnd + 1))) {
      throw unsupportedFlowServiceTlsAddress(serverAddress);
    }
    endpoint = dnsTarget.slice(authorityEnd + 1);
    // grpc-js matches resolver schemes case-sensitively, while Go accepts DNS:///.
    target = `dns://${dnsTarget}`;
  } else {
    const schemeLength = serverAddress.indexOf(":");
    if (/[/?#]/.test(serverAddress)
      || (schemeLength >= 0 && resolverSchemes.has(serverAddress.slice(0, schemeLength).toLowerCase()))) {
      throw unsupportedFlowServiceTlsAddress(serverAddress);
    }
  }
  const hostPort = splitHostPort(endpoint);
  if (hostPort === undefined || !isValidFlowServiceTlsName(hostPort.host)) {
    throw unsupportedFlowServiceTlsAddress(serverAddress);
  }
  const port = /^[+-]?[0-9]+$/.test(hostPort.port) ? Number(hostPort.port) : Number.NaN;
  if (!(port >= 1 && port <= 65_535)) {
    throw new TypeError(`dex: flowServiceTls requires serverAddress port 1-65535, got ${JSON.stringify(serverAddress)}`);
  }
  return target;
}

function unsupportedFlowServiceTlsAddress(serverAddress: string): TypeError {
  return new TypeError(
    "dex: flowServiceTls requires serverAddress as host:port, dns:///host:port, or dns://authority/host:port, "
      + `got ${JSON.stringify(serverAddress)}`,
  );
}

// Ports Go's net.SplitHostPort, including its bracket rules.
function splitHostPort(hostPort: string): { host: string; port: string } | undefined {
  const portSeparator = hostPort.lastIndexOf(":");
  if (portSeparator < 0) return undefined;
  let host: string;
  let openBracketSearchStart = 0;
  let closeBracketSearchStart = 0;
  if (hostPort.startsWith("[")) {
    const closeBracket = hostPort.indexOf("]");
    if (closeBracket < 0 || closeBracket + 1 !== portSeparator) return undefined;
    host = hostPort.slice(1, closeBracket);
    openBracketSearchStart = 1;
    closeBracketSearchStart = closeBracket + 1;
  } else {
    host = hostPort.slice(0, portSeparator);
    if (host.includes(":")) return undefined;
  }
  if (hostPort.indexOf("[", openBracketSearchStart) >= 0 || hostPort.indexOf("]", closeBracketSearchStart) >= 0) {
    return undefined;
  }
  return { host, port: hostPort.slice(portSeparator + 1) };
}

function isValidFlowServiceTlsName(name: string): boolean {
  return name !== "" && !/[ \t\r\n/#?@]/.test(name)
    && (!name.includes(":") || (isIPv6(name) && !name.includes("%")));
}

function additionalRootCertificateAuthorities(
  additionalRootCAsPem: string | Uint8Array | undefined,
): { ca?: string[] } {
  if (additionalRootCAsPem === undefined || additionalRootCAsPem.length === 0) return {};
  const bundle = typeof additionalRootCAsPem === "string"
    ? additionalRootCAsPem
    : Buffer.from(additionalRootCAsPem).toString("utf8");
  const certificates = [...bundle.matchAll(pemCertificatePattern)]
    .map((match) => match[0])
    .filter(isParsableCertificate);
  if (certificates.length === 0) {
    throw new TypeError("dex: flowServiceTls.additionalRootCAsPem contains no PEM certificate");
  }
  // A ca list replaces Node's default roots, so keep them first.
  return { ca: [...defaultRootCertificates(), ...certificates] };
}

function isParsableCertificate(pem: string): boolean {
  try {
    new X509Certificate(pem);
    return true;
  } catch {
    return false;
  }
}

function defaultRootCertificates(): readonly string[] {
  // Node.js before 22.15 lacks getCACertificates; rootCertificates omits NODE_EXTRA_CA_CERTS.
  return typeof tls.getCACertificates === "function" ? tls.getCACertificates("default") : tls.rootCertificates;
}
