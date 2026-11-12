// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

/** The gRPC status code `UNAVAILABLE`. */
const grpcUnavailableCode = 14;

/**
 * Returns the one-line error for a Worker that cannot reach the Dex server at all.
 *
 * @param failure - The Worker's startup failure.
 * @param server - The `dex.server` value.
 * @param serverAddress - The gRPC target the Worker used.
 * @returns The error text when a gRPC `UNAVAILABLE` caused the failure; `undefined` otherwise.
 */
export function describeUnreachableDexServer(
  failure: unknown,
  server: string,
  serverAddress: string,
): string | undefined {
  if (!isCausedByUnavailable(failure)) {
    return undefined;
  }
  if (server === "auto") {
    return (
      `cannot reach the Dex server of dexcli dev at ${serverAddress}. Run dexcli dev, or set dex.server to the URL ` +
      "of a running Dex server."
    );
  }
  return (
    `cannot reach the Dex server at ${serverAddress}, which dex.server names. Start that server, or set dex.server ` +
    "to auto to use dexcli dev on this machine."
  );
}

function isCausedByUnavailable(failure: unknown): boolean {
  const seen = new Set<unknown>();
  let current: unknown = failure;
  while (typeof current === "object" && current !== null && !seen.has(current)) {
    seen.add(current);
    // A grpc-js ServiceError, matched by shape: the SDK wraps it in its own Error as the cause.
    if ((current as { readonly code?: unknown }).code === grpcUnavailableCode) {
      return true;
    }
    current = (current as { readonly cause?: unknown }).cause;
  }
  return false;
}
