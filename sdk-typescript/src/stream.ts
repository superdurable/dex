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
   * @param streamCapacityBytes - Positive approximate byte budget shared by this Flow type's instances.
   */
  public constructor(
    public readonly name: string,
    public readonly codec: Codec<T>,
    public readonly streamCapacityBytes: number,
  ) {
    requireName(name);
    if (!Number.isSafeInteger(streamCapacityBytes) || streamCapacityBytes <= 0) {
      throw new RangeError("Stream streamCapacityBytes must be a positive safe integer");
    }
  }

  /**
   * Appends one message immediately from the current Step execution.
   *
   * The write emits a fire-and-forget frame on the current Worker response stream. One Step method
   * invocation may write any number of messages to the same or different Streams. Dex Stream Store
   * failures are not returned to the handler; local validation and encoding errors still throw.
   * @param context - Current Step Context; RPC Contexts are rejected.
   * @param value - Typed message to append.
   */
  public write(context: Context, value: T): void {
    context.writeStream(this, value);
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
  /** Client-supplied source or Step-generated `#stepExecutionID` source. */
  readonly source: string;
}
