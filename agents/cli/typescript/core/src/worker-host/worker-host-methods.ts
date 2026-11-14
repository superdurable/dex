// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { open, stat } from "node:fs/promises";
import { basename, isAbsolute } from "node:path";

import { FlowAlreadyStartedError, FlowNotFoundError } from "@superdurable/dex";
import { detectImageMimeType, maxImageBytes, startAgentSession, type AgentImageReference } from "@superdurable/dex-agents";

import type { OpenedSessionWorker } from "../agent-cli.js";
import type { ConnectorEntry } from "../cli-config.js";
import { isConnectorToolEnabledBy } from "../connector-tool-source.js";
import { sessionTaskQueue } from "../session-task-queues.js";
import { refusal } from "../terminal-ui/local-request-relay.js";
import { candidateSessionIds } from "../terminal-ui/request-keys.js";
import type { LocalRequestOutcome } from "../terminal-ui/request-outcome-table.js";
import { describeFailureWithoutDexDetails } from "../dex-connection.js";

/** What the Worker host's methods work on. */
export interface WorkerHostMethodContext {
  readonly harnessName: string;
  readonly opened: OpenedSessionWorker;
  /** `connectors.enabled` of the configuration. */
  readonly connectorsEnabled: readonly ConnectorEntry[] | null;
  /** Writes a line to the Worker host's log. */
  readonly log: (line: string) => void;
}

/**
 * Runs one local request that the launcher relayed: `put_image`, `start_session`, or `list_connectors`.
 * @returns The outcome, with the contract's error codes for expected failures.
 */
export async function executeWorkerHostMethod(
  method: string,
  params: Readonly<Record<string, unknown>>,
  idempotencyKey: string,
  context: WorkerHostMethodContext,
): Promise<LocalRequestOutcome> {
  switch (method) {
    case "put_image":
      return putImage(params, context.opened);
    case "start_session":
      return startSession(idempotencyKey, context);
    case "list_connectors":
      return listConnectors(context);
    default:
      return refusal("unknown_method", `This command does not serve ${method}.`);
  }
}

/** Stores one image file in the media store, addressed by its SHA-256. */
async function putImage(params: Readonly<Record<string, unknown>>, opened: OpenedSessionWorker): Promise<LocalRequestOutcome> {
  const path = params.path;
  if (typeof path !== "string" || !isAbsolute(path)) {
    return refusal("invalid_params", "put_image needs an absolute path.");
  }
  const name = basename(path);
  let size: number;
  try {
    const status = await stat(path);
    if (!status.isFile()) {
      return refusal("not_found", `${name} is not a regular file.`);
    }
    size = status.size;
  } catch {
    return refusal("not_found", `${name} does not exist.`);
  }
  if (size > maxImageBytes) {
    return refusal("too_large", `${name} has ${size} bytes; an image may have at most ${maxImageBytes}.`);
  }
  let data: Uint8Array;
  try {
    data = await readAtMost(path, maxImageBytes + 1);
  } catch {
    return refusal("not_found", `${name} cannot be read.`);
  }
  if (data.length > maxImageBytes) {
    return refusal("too_large", `${name} has more than ${maxImageBytes} bytes; that is the most an image may have.`);
  }
  try {
    detectImageMimeType(data);
  } catch {
    return refusal("not_an_image", `${name} is not a PNG, JPEG, GIF, or WebP image.`);
  }
  let reference: AgentImageReference;
  try {
    reference = await opened.mediaStore.putImage(data);
  } catch {
    return refusal("store_failed", `${name} could not be stored in the media store.`);
  }
  return {
    result: {
      image: {
        type: reference.type,
        uri: reference.uri,
        mime_type: reference.mimeType,
        size_bytes: reference.sizeBytes,
        sha256: reference.sha256,
      },
      name,
    },
  };
}

/** The Dex server calls of `start_session`, with the request's key as every start request ID. */
export interface SessionStarter {
  /** Returns whether a Flow with this ID exists, from `DescribeFlow`. */
  isStarted(sessionId: string): Promise<boolean>;
  /**
   * Starts a Flow with "ignore already started": `started` for a new run or the run this key started, `taken` when
   * another request started it.
   */
  start(sessionId: string): Promise<"started" | "taken">;
}

/**
 * Returns `start_session`'s outcome for a key. The long candidate goes first when it exists, so a resend finds the
 * key's long-ID run even after the run that held the short ID was purged. Then the short candidate, then the long
 * one when it did not exist. Otherwise `id_conflict`.
 * @param harnessName - `command.harness`.
 * @param idempotencyKey - The request's key.
 * @param starter - The Dex server calls.
 */
export async function startSessionForKey(
  harnessName: string,
  idempotencyKey: string,
  starter: SessionStarter,
): Promise<LocalRequestOutcome> {
  const [shortId, longId] = candidateSessionIds(harnessName, idempotencyKey);
  const started = (sessionId: string): LocalRequestOutcome => ({ result: { session_id: sessionId } });
  const isLongStarted = await starter.isStarted(longId);
  if (isLongStarted && (await starter.start(longId)) === "started") {
    return started(longId);
  }
  if ((await starter.start(shortId)) === "started") {
    return started(shortId);
  }
  if (!isLongStarted && (await starter.start(longId)) === "started") {
    return started(longId);
  }
  return refusal("id_conflict", "Other requests already started both session IDs this key derives.");
}

async function startSession(idempotencyKey: string, context: WorkerHostMethodContext): Promise<LocalRequestOutcome> {
  const { opened } = context;
  try {
    return await startSessionForKey(context.harnessName, idempotencyKey, {
      isStarted: async (sessionId) => {
        try {
          await opened.client.describeFlow(sessionId);
          return true;
        } catch (error: unknown) {
          if (error instanceof FlowNotFoundError) {
            return false;
          }
          throw error;
        }
      },
      start: async (sessionId) => {
        try {
          // Each candidate ID has its own task queue; the switch to the session then pulls it.
          await startAgentSession(
            opened.client,
            opened.sessionFlow,
            sessionId,
            { startEnvironment: opened.startEnvironment },
            { requestId: idempotencyKey, ignoreAlreadyStarted: true, taskQueue: sessionTaskQueue(sessionId) },
          );
          return "started";
        } catch (error: unknown) {
          if (error instanceof FlowAlreadyStartedError) {
            return "taken";
          }
          throw error;
        }
      },
    });
  } catch (error: unknown) {
    context.log(`start_session failed: ${describeFailureWithoutDexDetails(error)}`);
    return refusal("start_failed", "The new session could not start.");
  }
}

/** Lists the connector tools the MCP server offers now, with whether the configuration enables each. */
async function listConnectors(context: WorkerHostMethodContext): Promise<LocalRequestOutcome> {
  const connectors = context.opened.connectors;
  if (connectors === undefined) {
    return refusal("mcp_unavailable", "Connectors are off for this command.");
  }
  let identities;
  try {
    identities = await connectors.source.readIdentities(true);
  } catch {
    return refusal("mcp_unavailable", "The connectors MCP server is unavailable.");
  }
  const enabled = context.connectorsEnabled;
  const tools = [...identities.values()]
    .sort((left, right) => (left.toolName < right.toolName ? -1 : left.toolName > right.toolName ? 1 : 0))
    .map((identity) => ({
      name: identity.toolName,
      connector: identity.connector,
      connection: identity.connection,
      operation: identity.operationName ?? identity.operation,
      is_enabled: enabled === null || enabled.some((entry) => isConnectorToolEnabledBy(entry, identity)),
    }));
  return { result: { mcp_url: connectors.url, tools } };
}

async function readAtMost(path: string, byteCount: number): Promise<Uint8Array> {
  const handle = await open(path, "r");
  try {
    const buffer = Buffer.alloc(byteCount);
    let length = 0;
    while (length < buffer.length) {
      const { bytesRead } = await handle.read(buffer, length, buffer.length - length, null);
      if (bytesRead === 0) {
        break;
      }
      length += bytesRead;
    }
    return buffer.subarray(0, length);
  } finally {
    await handle.close();
  }
}
