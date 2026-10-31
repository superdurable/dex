// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { Context, Stream } from "@superdurable/dex";

import type { AgentEventWriter } from "./agent-framework-runner.js";
import type { AgentEvent, AgentEventType, AgentToolCallStatus } from "./agent-session-values.js";

const deltaFlushIntervalMs = 100;
const deltaFlushCharacters = 1024;

type DeltaEventType = "text_delta" | "reasoning_delta";

export class StreamAgentEventWriter implements AgentEventWriter {
  private pendingDeltaType: DeltaEventType | undefined;
  private pendingDeltaText = "";
  private deltaFlushTimer: NodeJS.Timeout | undefined;
  private isClosed = false;

  public constructor(
    private readonly context: Context,
    private readonly stream: Stream<AgentEvent>,
    private readonly turnId: string | null,
  ) {}

  public writeTextDelta(text: string): void {
    this.writeDelta("text_delta", text);
  }

  public writeReasoningDelta(text: string): void {
    this.writeDelta("reasoning_delta", text);
  }

  public writeToolCallStarted(toolCallId: string, toolName: string): void {
    this.writeAgentEvent("tool_call_started", { tool_call_id: toolCallId, tool_name: toolName });
  }

  public writeToolCallFinished(
    toolCallId: string,
    toolName: string,
    status: AgentToolCallStatus,
  ): void {
    this.writeAgentEvent("tool_call_finished", {
      tool_call_id: toolCallId,
      tool_name: toolName,
      status,
    });
  }

  public writeAgentEvent(type: AgentEventType, payload: Readonly<Record<string, unknown>>): void {
    if (this.isClosed) {
      return;
    }
    this.flushPendingDelta();
    this.stream.write(this.context, {
      type,
      turnId: this.turnId,
      attempt: this.context.attempt,
      payload: JSON.stringify(payload),
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
    this.stream.write(this.context, {
      type,
      turnId: this.turnId,
      attempt: this.context.attempt,
      payload: JSON.stringify({ text }),
    });
  }
}
