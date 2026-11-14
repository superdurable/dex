// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// The control channel between a launcher and its Worker host. Both ends come from the same CLI core version, so
// it has no version negotiation and is not part of the terminal UI contract.

import type { EffectiveConfig } from "../cli-config.js";
import type { DexConnectionSettings } from "../dex-connection.js";
import type { SessionTerminalLease } from "@superdurable/dex-agents";

import type { DexServerConnection } from "../dex-server.js";
import type { SessionFlowCapabilities } from "../terminal-ui/launch-document.js";
import type { LocalRequestOutcome } from "../terminal-ui/request-outcome-table.js";

/** The hidden subcommand that runs a command's Worker host. */
export const workerHostSubcommand = "__worker-host";

/** What the Worker host needs to open the session, without secrets: those are in its environment. */
export interface WorkerHostStartRequest {
  readonly type: "start";
  /** The effective configuration, after command-line overrides. */
  readonly effective: EffectiveConfig;
  readonly configPath: string;
  readonly isFromFile: boolean;
  readonly server: DexServerConnection;
  /** How to verify and authenticate to the server; the secret it names is in the Worker host's environment. */
  readonly connection: DexConnectionSettings;
  readonly resumeSessionId: string | null;
}

/** A local request the launcher relays to the Worker host. */
export interface WorkerHostExecuteRequest {
  readonly type: "execute";
  /** Pairs the answer with the request on this channel. */
  readonly requestNumber: number;
  readonly method: string;
  readonly params: Readonly<Record<string, unknown>>;
  readonly idempotencyKey: string;
}

/**
 * The launcher's own requests, sent as {@link WorkerHostExecuteRequest}s but never offered to the terminal UI:
 * `acquire_session_lease {session_id}` answers `{is_acquired, lease}`, plus `flow_type` and `notices` once
 * acquired: the session's Flow type and the lines about a takeover from another place. `release_session_lease
 * {session_id}` answers `{}`.
 */
export const workerHostLeaseMethods = {
  acquire: "acquire_session_lease",
  release: "release_session_lease",
} as const;

/** Another terminal took the lease of the session the Worker host renews. */
export interface WorkerHostSessionLeaseLost {
  readonly type: "session_lease_lost";
  readonly sessionId: string;
  readonly lease: SessionTerminalLease;
}

/** No renewal confirmed the lease of the session the Worker host renews, which then stopped pulling it. */
export interface WorkerHostSessionLeaseUnconfirmed {
  readonly type: "session_lease_unconfirmed";
  readonly sessionId: string;
}

/** A message from the launcher to the Worker host. */
export type LauncherToWorkerHostMessage = WorkerHostStartRequest | WorkerHostExecuteRequest | { readonly type: "shutdown" };

/** The session is open and the Worker serves it. */
export interface WorkerHostReady {
  readonly type: "worker_ready";
  readonly sessionId: string;
  readonly flowType: string;
  readonly isResumed: boolean;
  /** The workspace's real path. */
  readonly workspace: string;
  readonly capabilities: SessionFlowCapabilities;
}

/** The session could not open; the launcher exits with `exitCode` after printing `message`. */
export interface WorkerHostStartupFailed {
  readonly type: "startup_failed";
  readonly exitCode: number;
  readonly message: string;
}

/** A message from the Worker host to the launcher. */
export type WorkerHostToLauncherMessage =
  | WorkerHostReady
  | WorkerHostStartupFailed
  | { readonly type: "log"; readonly text: string }
  | { readonly type: "executed"; readonly requestNumber: number; readonly outcome: LocalRequestOutcome }
  | { readonly type: "worker_state"; readonly state: "stopping" }
  | WorkerHostSessionLeaseLost
  | WorkerHostSessionLeaseUnconfirmed;
