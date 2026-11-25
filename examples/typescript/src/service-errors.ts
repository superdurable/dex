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

import { setTimeout as delay } from "node:timers/promises";

import type { Response } from "express";

import {
  FlowAlreadyStartedError,
  FlowNotActiveOrNotFoundError,
  FlowNotFoundError,
  RpcLockConflictError,
} from "@superdurable/dex";

const LOCK_CONFLICT_ATTEMPTS = 5;
const LOCK_CONFLICT_BACKOFF_MS = 200;

/**
 * Invokes an RPC and retries a lock conflict with bounded backoff. Retrying is
 * safe: a conflicting call committed nothing.
 *
 * @returns The RPC result.
 * @throws {RpcLockConflictError} When every attempt conflicts.
 */
export async function invokeRPCRetryingLockConflict<T>(
  invoke: () => Promise<T>,
): Promise<T> {
  for (let attempt = 1; ; attempt += 1) {
    try {
      return await invoke();
    } catch (error) {
      if (!(error instanceof RpcLockConflictError) || attempt === LOCK_CONFLICT_ATTEMPTS) {
        throw error;
      }
      // Jitter spreads out callers that conflicted at the same time.
      await delay(attempt * LOCK_CONFLICT_BACKOFF_MS + Math.random() * LOCK_CONFLICT_BACKOFF_MS);
    }
  }
}

/**
 * Answers 409 for an RPC that still lost its lock race after the retries.
 *
 * @returns Whether the error was a lock conflict and a response was sent.
 */
export function respondLockConflict(response: Response, error: unknown): boolean {
  if (!(error instanceof RpcLockConflictError)) {
    return false;
  }
  response.status(409).send("the Flow is busy with another update; retry the request");
  return true;
}

export function isFlowAlreadyStarted(error: unknown): boolean {
  return error instanceof FlowAlreadyStartedError;
}

export function isFlowMissingOrInactive(error: unknown): boolean {
  return (
    error instanceof FlowNotActiveOrNotFoundError ||
    error instanceof FlowNotFoundError
  );
}
