// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { AsyncContext } from "@superdurable/dex";

import { compareCodePoints } from "./connector-input-schema.js";

// The source reads it on a Retry; a failed save leaves the earlier value.
const savedHeartbeatValues = new WeakMap<ConnectorStepContext, unknown>();

/** Returns the last heartbeat value this context saved in this attempt, or `undefined`. Internal to the core. */
export function readSavedHeartbeatValue(context: ConnectorStepContext): unknown {
  return savedHeartbeatValues.get(context);
}

/** How a {@link ConnectorStepContext} reaches the tool Step's heartbeats. */
export interface ConnectorStepHeartbeats {
  /** The text checkpoint an earlier attempt of the tool Step saved, or `undefined`. */
  readonly previousCheckpoint: string | undefined;
  /**
   * Records a plain heartbeat, which repeats the attempt's saved checkpoint.
   * @returns A Promise that resolves when the Worker accepted the heartbeat.
   */
  heartbeat(): Promise<void>;
  /**
   * Records a heartbeat carrying a text checkpoint; absent when the tool Step cannot save checkpoints.
   * @param checkpoint - The text to hand to the next attempt.
   * @returns A Promise that resolves when the Worker accepted the heartbeat.
   */
  saveCheckpoint?(checkpoint: string): Promise<void>;
}

/**
 * The Step context a connector operation sees inside an agent tool Step, with heartbeat values kept as text.
 *
 * The single-dispatch fence records markers as heartbeat values and reads the previous attempt's marker back. The
 * agent session's tool Step keeps one text checkpoint per attempt and repeats it on every background heartbeat.
 * This context stores each heartbeat value as that text checkpoint, as JSON with sorted keys, and parses it on the
 * next attempt, so a marker survives the session's own heartbeats and a retry can read it. It exposes only the
 * members the fence uses, plus the identities a Call needs; the agent core's conformance tests pin that list.
 *
 * `ConnectorToolSource` builds one per tool Step attempt. Applications do not construct it.
 * @since 0.3.0
 */
export class ConnectorStepContext {
  /**
   * Creates the context for one tool Step attempt.
   * @param context - The tool Step's live context.
   * @param heartbeats - The tool Step's checkpoint and heartbeat functions.
   */
  public constructor(
    private readonly context: AsyncContext,
    private readonly heartbeats: ConnectorStepHeartbeats,
  ) {}

  /** The Flow ID. */
  public get flowId(): string {
    return this.context.flowId;
  }

  /** The Flow run ID. */
  public get runId(): string {
    return this.context.runId;
  }

  /** The tool Step's execution ID, the same on every attempt. */
  public get stepExecutionId(): string {
    return this.context.stepExecutionId;
  }

  /** The one-based attempt. */
  public get attempt(): number {
    return this.context.attempt;
  }

  /** The attempt's deadline, or `undefined`. */
  public get deadline(): Date | undefined {
    return this.context.deadline;
  }

  /** The signal Dex aborts when it cancels the attempt. */
  public get cancellationSignal(): AbortSignal {
    return this.context.cancellationSignal;
  }

  /**
   * Reports whether an earlier attempt of this tool Step saved a heartbeat value.
   * @returns `true` when the session read a checkpoint for this attempt.
   */
  public hasLastHeartbeatValue(): boolean {
    return this.heartbeats.previousCheckpoint !== undefined;
  }

  /**
   * Parses the earlier attempt's heartbeat value from its JSON text.
   * @param codec - Must be omitted; values are JSON.
   * @returns The parsed value, or `undefined` when there is none.
   * @throws {TypeError} When a codec is given.
   * @throws {SyntaxError} When the checkpoint is not JSON.
   */
  public getLastHeartbeatValue<T = unknown>(codec?: unknown): T | undefined {
    if (codec !== undefined) {
      throw new TypeError("ConnectorStepContext keeps heartbeat values as JSON and takes no codec");
    }
    const previous = this.heartbeats.previousCheckpoint;
    return previous === undefined ? undefined : (JSON.parse(previous) as T);
  }

  /**
   * Records a heartbeat, saving a value as the attempt's text checkpoint.
   * @param value - The heartbeat value, such as a fence marker; `undefined` records a plain heartbeat, which
   * repeats the saved checkpoint.
   * @param codec - Must be omitted; values are JSON.
   * @returns A Promise that resolves when the Worker accepted the heartbeat.
   * @throws {TypeError} When a codec is given, or a value is given and the tool Step cannot save checkpoints.
   */
  public async recordHeartbeat<T>(value?: T, codec?: unknown): Promise<void> {
    if (codec !== undefined) {
      throw new TypeError("ConnectorStepContext keeps heartbeat values as JSON and takes no codec");
    }
    if (value === undefined) {
      await this.heartbeats.heartbeat();
      return;
    }
    if (this.heartbeats.saveCheckpoint === undefined) {
      throw new TypeError("this tool Step cannot save heartbeat checkpoints");
    }
    await this.heartbeats.saveCheckpoint(stringifyWithSortedKeys(value));
    savedHeartbeatValues.set(this, value);
  }
}

/** Returns compact JSON with object keys sorted by code point, the text the Python core stores. */
export function stringifyWithSortedKeys(value: unknown): string {
  return JSON.stringify(sortKeys(value));
}

function sortKeys(value: unknown): unknown {
  if (Array.isArray(value)) {
    return value.map(sortKeys);
  }
  if (typeof value === "object" && value !== null) {
    const record = value as Readonly<Record<string, unknown>>;
    return Object.fromEntries(
      Object.keys(record)
        .filter((key) => record[key] !== undefined)
        .sort(compareCodePoints)
        .map((key) => [key, sortKeys(record[key])]),
    );
  }
  return value;
}
