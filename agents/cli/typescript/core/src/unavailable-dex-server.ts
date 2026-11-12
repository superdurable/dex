// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

/** The gRPC status code `UNAVAILABLE`. */
const grpcUnavailableCode = 14;
/** grpc-js details when no connection opened: a refused or unreachable port, or no DNS answer. */
const connectFailureMarkers: readonly string[] = ["No connection established", "Name resolution failed"];

/**
 * Returns the one-line error for a Worker whose Dex server answered gRPC `UNAVAILABLE`.
 *
 * A connection that never opened, such as a refused port or a host name that does not resolve, says how to start
 * or choose the server. An `UNAVAILABLE` that a reachable server returned names its details and suggests a retry.
 *
 * @param failure - The Worker's startup failure.
 * @param server - The `dex.server` value.
 * @param serverAddress - The gRPC target the Worker used.
 * @returns The error text when a gRPC `UNAVAILABLE` caused the failure; `undefined` otherwise.
 */
export function describeUnavailableDexServer(
  failure: unknown,
  server: string,
  serverAddress: string,
): string | undefined {
  const details = unavailableDetails(failure);
  if (details === undefined) {
    return undefined;
  }
  if (!connectFailureMarkers.some((marker) => details.includes(marker))) {
    return `the Dex server at ${serverAddress} returned UNAVAILABLE: ${details}. It may still be starting; try again in a moment.`;
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

/** Returns the details of the first gRPC `UNAVAILABLE` in the cause chain, or `undefined`. */
function unavailableDetails(failure: unknown): string | undefined {
  const seen = new Set<unknown>();
  let current: unknown = failure;
  while (typeof current === "object" && current !== null && !seen.has(current)) {
    seen.add(current);
    // A grpc-js ServiceError, matched by shape: the SDK wraps it in its own Error as the cause.
    const candidate = current as { readonly code?: unknown; readonly details?: unknown; readonly cause?: unknown };
    if (candidate.code === grpcUnavailableCode) {
      return typeof candidate.details === "string" ? candidate.details : "";
    }
    current = candidate.cause;
  }
  return undefined;
}
