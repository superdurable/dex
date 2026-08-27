// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import type { Codec } from "./codec.js";
import type { Context } from "./context.js";
import { requireName } from "./validation.js";

/**
 * Defines a typed, best-effort resumable message stream owned by one Flow type.
 * @typeParam T - Message type encoded by the Stream codec.
 */
export class Stream<T> {
  /**
   * Creates a Stream definition for a PersistenceSchema.
   * @param name - Non-empty logical name unique within the Flow.
   * @param codec - Codec used for every Stream message.
   * @param maxEstimatedBytes - Positive approximate byte budget shared by this Flow type's instances.
   */
  public constructor(
    public readonly name: string,
    public readonly codec: Codec<T>,
    public readonly maxEstimatedBytes: number,
  ) {
    requireName(name);
    if (!Number.isSafeInteger(maxEstimatedBytes) || maxEstimatedBytes <= 0) {
      throw new RangeError("Stream maxEstimatedBytes must be a positive safe integer");
    }
  }

  /**
   * Appends one message immediately from the current Step execution.
   *
   * A Step execution may write once per Stream. Retries reuse the same server idempotency key.
   * @param context - Current Step Context; RPC Contexts are rejected.
   * @param value - Typed message to append.
   */
  public async write(context: Context, value: T): Promise<void> {
    await context.writeStream(this, value);
  }
}

/**
 * Describes one retained Stream message returned by Client.readStream.
 * @typeParam T - Decoded application message type.
 */
export interface StreamMessage<T> {
  /** Decoded application message. */
  readonly value: T;
  /** Opaque token to pass unchanged to the next readStream call. */
  readonly resumeToken: string;
  /** Server-assigned UTC creation time. */
  readonly createdTime: Date;
  /** Client key or Step-generated runID#stepExecutionID key. */
  readonly idempotencyKey: string;
}
