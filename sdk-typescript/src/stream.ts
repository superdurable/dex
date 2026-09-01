// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import type { Codec } from "./codec.js";
import type { Context } from "./context.js";
import {
  InvocationContext,
  type StepOutputFinalizer,
} from "./invocation-context.js";
import { requireName } from "./validation.js";

const DEFAULT_BUFFERED_TEXT_FLUSH_INTERVAL_MS = 1_000;
const DEFAULT_BUFFERED_TEXT_MAX_BYTES = 16 * 1_024;

/** Configures a buffered text Stream writer. */
export interface BufferedTextStreamOptions {
  /** One-shot flush interval in milliseconds. Defaults to 1,000. */
  readonly flushIntervalMs?: number;
  /** Soft UTF-8 batch threshold in bytes. Defaults to 16 KiB. */
  readonly maxBufferedBytes?: number;
}

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

  /**
   * Creates an invocation-managed text writer for an asynchronous Step.
   *
   * The writer concatenates chunks exactly and flushes on its timer, soft UTF-8 size threshold,
   * an explicit {@link BufferedTextStream.flush}, or handler completion. The Worker flushes every
   * remaining batch before the final result or error. Empty chunks are ignored.
   *
   * @param context - Current asynchronous Step Context.
   * @param options - Optional flush interval and soft byte threshold.
   * @param this - String Stream that receives each emitted batch.
   * @returns A writer whose bound {@link BufferedTextStream.write} method can be used as a callback.
   * @throws {@link TypeError} if this is not a string Stream or the Context is not an active Step.
   * @throws {@link RangeError} if an option is not a positive finite number.
   */
  public buffered(
    this: Stream<string>,
    context: Context,
    options: BufferedTextStreamOptions = {},
  ): BufferedTextStream {
    if (this.codec.wireKind !== "string") {
      throw new TypeError("Buffered Streams require Stream<string>");
    }
    if (!(context instanceof InvocationContext)) {
      throw new TypeError("Buffered Streams require a Dex Step Context");
    }
    const flushIntervalMs = options.flushIntervalMs ?? DEFAULT_BUFFERED_TEXT_FLUSH_INTERVAL_MS;
    const maxBufferedBytes = options.maxBufferedBytes ?? DEFAULT_BUFFERED_TEXT_MAX_BYTES;
    requirePositiveNumber("flushIntervalMs", flushIntervalMs);
    requirePositiveSafeInteger("maxBufferedBytes", maxBufferedBytes);
    context.prepareBufferedStream(this);
    const writer = new BufferedTextStream(
      this,
      context,
      flushIntervalMs,
      maxBufferedBytes,
    );
    context.registerStepOutputFinalizer(writer);
    return writer;
  }
}

/** Batches text chunks during one asynchronous Step invocation. */
export class BufferedTextStream implements StepOutputFinalizer {
  private readonly encoder = new TextEncoder();
  private readonly chunks: string[] = [];
  private bufferedBytes = 0;
  private timer: ReturnType<typeof setTimeout> | undefined;
  private timerGeneration = 0;
  private isClosed = false;
  private terminalFailure: unknown | undefined;

  /**
   * Creates a writer from validated invocation-owned components.
   * @param stream - Registered string Stream receiving emitted batches.
   * @param context - Current asynchronous Step Context.
   * @param flushIntervalMs - Positive one-shot interval in milliseconds.
   * @param maxBufferedBytes - Positive soft UTF-8 threshold.
   */
  public constructor(
    private readonly stream: Stream<string>,
    private readonly context: Context,
    private readonly flushIntervalMs: number,
    private readonly maxBufferedBytes: number,
  ) {
    this.write = this.write.bind(this);
    this.flush = this.flush.bind(this);
  }

  /**
   * Appends one chunk without modification and flushes after crossing the soft size threshold.
   * @param value - Text to append; an empty string is ignored.
   * @throws {@link Error} if the invocation finished or an earlier background flush failed.
   */
  public write(value: string): void {
    this.requireOpen();
    if (typeof value !== "string") {
      throw new TypeError("Buffered Stream chunks must be strings");
    }
    if (value.length === 0) {
      return;
    }
    const wasEmpty = this.chunks.length === 0;
    this.chunks.push(value);
    this.bufferedBytes += this.encoder.encode(value).byteLength;
    if (wasEmpty) {
      this.startTimer();
    }
    if (this.bufferedBytes >= this.maxBufferedBytes) {
      this.flush();
    }
  }

  /**
   * Immediately emits the current non-empty batch and restarts timing with the next write.
   * @throws {@link Error} if the invocation finished or an earlier flush failed.
   */
  public flush(): void {
    this.requireOpen();
    this.stopTimer();
    this.flushBuffer();
  }

  /** Flushes the tail and closes this writer during invocation finalization. */
  public finalizeStepOutput(): void {
    if (this.isClosed) {
      if (this.terminalFailure !== undefined) {
        throw this.terminalFailure;
      }
      return;
    }
    this.stopTimer();
    try {
      if (this.terminalFailure !== undefined) {
        throw this.terminalFailure;
      }
      this.flushBuffer();
    } finally {
      this.isClosed = true;
    }
  }

  /** Discards buffered text and stops the timer after invocation cancellation. */
  public cancelStepOutput(): void {
    this.stopTimer();
    this.chunks.length = 0;
    this.bufferedBytes = 0;
    this.isClosed = true;
  }

  private startTimer(): void {
    const generation = ++this.timerGeneration;
    this.timer = setTimeout(() => {
      if (this.isClosed || generation !== this.timerGeneration) {
        return;
      }
      this.timer = undefined;
      try {
        this.flushBuffer();
      } catch (failure) {
        this.terminalFailure = failure;
      }
    }, this.flushIntervalMs);
  }

  private stopTimer(): void {
    this.timerGeneration += 1;
    if (this.timer !== undefined) {
      clearTimeout(this.timer);
      this.timer = undefined;
    }
  }

  private flushBuffer(): void {
    if (this.chunks.length === 0) {
      return;
    }
    const value = this.chunks.join("");
    this.chunks.length = 0;
    this.bufferedBytes = 0;
    try {
      this.stream.write(this.context, value);
    } catch (failure) {
      this.terminalFailure = failure;
      throw failure;
    }
  }

  private requireOpen(): void {
    if (this.terminalFailure !== undefined) {
      throw this.terminalFailure;
    }
    if (this.isClosed) {
      throw new Error("Buffered Stream invocation has finished");
    }
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

function requirePositiveNumber(name: string, value: number): void {
  if (!Number.isFinite(value) || value <= 0) {
    throw new RangeError(`Buffered Stream ${name} must be positive`);
  }
}

function requirePositiveSafeInteger(name: string, value: number): void {
  if (!Number.isSafeInteger(value) || value <= 0) {
    throw new RangeError(`Buffered Stream ${name} must be a positive safe integer`);
  }
}
