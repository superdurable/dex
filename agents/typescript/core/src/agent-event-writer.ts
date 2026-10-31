// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { Context, Stream } from "@superdurable/dex";

import type { AgentEventWriter } from "./agent-framework-runner.js";
import type { AgentEvent, AgentToolCallStatus } from "./agent-session-values.js";

const deltaFlushIntervalMs = 100;
const deltaFlushCharacters = 1024;

type DeltaEventType = "text_delta" | "reasoning_delta";

export class StreamAgentEventWriter implements AgentEventWriter {
  private pendingDeltaType: DeltaEventType | undefined;
  private pendingDeltaText = "";
  private deltaFlushTimer: ReturnType<typeof setTimeout> | undefined;
  private isClosed = false;

  public constructor(
    private readonly context: Context,
    private readonly stream: Stream<AgentEvent>,
    public readonly turnId: string | null,
  ) {}

  public get attempt(): number {
    return this.context.attempt;
  }

  public writeEvent(type: string, payload: Readonly<Record<string, unknown>>): void {
    if (this.isClosed) {
      return;
    }
    this.flushPendingDelta();
    this.writeStreamMessage(type, payload);
  }

  public writeTextDelta(text: string): void {
    this.writeDelta("text_delta", text);
  }

  public writeReasoningDelta(text: string): void {
    this.writeDelta("reasoning_delta", text);
  }

  public writeToolCallStarted(toolCallId: string, toolName: string): void {
    this.writeEvent("tool_call_started", { tool_call_id: toolCallId, tool_name: toolName });
  }

  public writeToolCallFinished(
    toolCallId: string,
    toolName: string,
    status: AgentToolCallStatus,
  ): void {
    this.writeEvent("tool_call_finished", {
      tool_call_id: toolCallId,
      tool_name: toolName,
      status,
    });
  }

  /** Flushes buffered deltas and drops later writes; call before the handler returns. */
  public close(): void {
    if (this.isClosed) {
      return;
    }
    this.flushPendingDelta();
    this.isClosed = true;
  }

  private writeDelta(type: DeltaEventType, text: string): void {
    if (this.isClosed || text.length === 0) {
      return;
    }
    if (this.pendingDeltaType !== type) {
      this.flushPendingDelta();
      this.pendingDeltaType = type;
    }
    this.pendingDeltaText += text;
    if (this.pendingDeltaText.length >= deltaFlushCharacters) {
      this.flushPendingDelta();
      return;
    }
    this.deltaFlushTimer ??= setTimeout(() => this.flushPendingDelta(), deltaFlushIntervalMs);
  }

  private flushPendingDelta(): void {
    if (this.deltaFlushTimer !== undefined) {
      clearTimeout(this.deltaFlushTimer);
      this.deltaFlushTimer = undefined;
    }
    const type = this.pendingDeltaType;
    const text = this.pendingDeltaText;
    this.pendingDeltaType = undefined;
    this.pendingDeltaText = "";
    if (type === undefined || text.length === 0 || this.isClosed) {
      return;
    }
    this.writeStreamMessage(type, { text });
  }

  private writeStreamMessage(type: string, payload: Readonly<Record<string, unknown>>): void {
    this.stream.write(this.context, {
      type,
      turnId: this.turnId,
      attempt: this.context.attempt,
      stepExecutionId: this.context.stepExecutionId,
      payload: stringifyWithSortedKeys(payload),
    });
  }
}

/** Serializes with sorted object keys, as the Python core does, so payload text is deterministic. */
export function stringifyWithSortedKeys(value: unknown): string {
  const text = JSON.stringify(value, (_key, nestedValue: unknown) => {
    if (typeof nestedValue !== "object" || nestedValue === null || Array.isArray(nestedValue)) {
      return nestedValue;
    }
    const record = nestedValue as Record<string, unknown>;
    return Object.fromEntries(Object.keys(record).sort().map((key) => [key, record[key]]));
  });
  if (text === undefined) {
    throw new TypeError("event payload cannot be serialized as JSON");
  }
  return text;
}
