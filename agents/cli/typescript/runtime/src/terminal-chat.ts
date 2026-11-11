// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { open, stat } from "node:fs/promises";
import { basename, join, resolve } from "node:path";
import type { Writable } from "node:stream";
import { setTimeout as delay } from "node:timers/promises";

import { DexServiceError, LongPollTimeoutError, RpcLockConflictError, type Client } from "@superdurable/dex";
import {
  ImageValidationError,
  detectImageMimeType,
  maxImageBytes,
  maxImagesPerMessage,
  readAgentSnapshot,
  sendAgentUserMessage,
  type AgentEvent,
  type AgentFrameworkRunner,
  type AgentImageReference,
  type AgentInterrupt,
  type AgentSessionFlow,
  type AgentSnapshot,
  type DeferredToolOutcome,
  type MediaStore,
} from "@superdurable/dex-agents";

import { lineWaitAborted, type TerminalInput } from "./terminal-input.js";

/** The command that attaches an image file to the next message: `/image <path>`. */
const imageCommand = "/image";
const bytesPerKibibyte = 1024;
const snapshotPollIntervalMs = 250;
const eventPollTimeoutMs = 2_000;
const eventCatchUpTimeoutMs = 2_000;
const lockConflictRetryTimeoutMs = 30_000;
const grpcFailedPrecondition = 9;
const turnEndEventTypes = new Set(["turn_completed", "turn_failed", "turn_cancelled", "turn_steered"]);

type SessionFlow = AgentSessionFlow<AgentFrameworkRunner<DeferredToolOutcome>>;

/**
 * The terminal chat of one session: it sends each typed line as a user message, streams the reply and tool
 * calls, asks for approvals and answers inline, and cancels the turn on Ctrl-C.
 *
 * The session's snapshot drives the turn: its end, its pending approvals and questions, and its error. The event
 * stream only adds live text and tool lines, because event delivery is best effort. Exiting leaves the session
 * running in Dex.
 */
export class TerminalChat {
  private isExitRequested = false;
  private hasFailure = false;
  private turnCancellation: AbortController | undefined;
  private isCancelRequested = false;
  private readonly handledInterruptIds = new Set<string>();
  private readonly eventReading = new AbortController();
  private eventReader: Promise<void> | undefined;
  private isMidLine = false;
  private readonly turnsWithText = new Set<string>();
  private readonly endedTurns = new Map<string, string>();
  private isEventStreamAvailable = true;
  private readonly exitController = new AbortController();

  private pendingImages: AgentImageReference[] = [];

  /**
   * Creates the chat.
   * @param client - A Client whose Registry contains `flow`.
   * @param flow - The session Flow.
   * @param sessionId - The session.
   * @param input - Terminal input.
   * @param output - Where the chat writes.
   * @param images - The store that `/image` uploads to, and the home directory a leading `~` means.
   */
  public constructor(
    private readonly client: Client,
    private readonly flow: SessionFlow,
    private readonly sessionId: string,
    private readonly input: TerminalInput,
    private readonly output: Writable,
    private readonly images: { readonly mediaStore: MediaStore; readonly homeDirectory: string },
  ) {}

  /**
   * Runs the chat until `/exit`, a second Ctrl-C, the end of input, or a failed session.
   * @returns Whether a turn or the session failed during the chat; the session keeps running either way.
   */
  public async run(): Promise<boolean> {
    this.input.onInterrupt(() => this.handleInterrupt());
    this.eventReader = this.readEvents();
    try {
      const snapshot = await readAgentSnapshot(this.client, this.flow, this.sessionId);
      if (snapshot.state?.activeTurnId !== null && snapshot.state?.activeTurnId !== undefined) {
        this.write("A turn is running; waiting for it.\n");
        await this.awaitTurnEnd(snapshot.state.turnCount);
      }
      while (!this.isExitRequested) {
        this.write("\n> ");
        const line = await this.input.nextLine(this.exitSignal());
        if (line === null || line === lineWaitAborted) {
          break;
        }
        const text = line.trim();
        if (text === "") {
          continue;
        }
        if (text === "/exit" || text === "/quit") {
          break;
        }
        if (text === imageCommand || text.startsWith(`${imageCommand} `)) {
          await this.attachImage(text.slice(imageCommand.length));
          continue;
        }
        await this.runTurn(text);
      }
    } finally {
      this.isExitRequested = true;
      this.eventReading.abort();
      await this.eventReader;
    }
    return this.hasFailure;
  }

  private async runTurn(text: string): Promise<void> {
    const before = await readAgentSnapshot(this.client, this.flow, this.sessionId);
    if (before.state?.status === "failed") {
      this.write(`The session failed: ${before.state.lastTurnError ?? "unknown reason"}\n`);
      this.hasFailure = true;
      this.isExitRequested = true;
      return;
    }
    const images = this.pendingImages;
    this.pendingImages = [];
    const isQueued = await sendAgentUserMessage(this.client, this.flow, this.sessionId, {
      text,
      ...(images.length === 0 ? {} : { images }),
    });
    if (!isQueued) {
      this.write(
        images.length === 0
          ? "The session did not accept the message.\n"
          : `Not sent: this agent does not take images. The attached images (${countOf(images.length, "image")}) were removed; send the message again without them.\n`,
      );
      return;
    }
    await this.awaitTurnEnd(before.state?.turnCount ?? 0);
  }

  /** Copies one image file into the media store and keeps it for the next message. */
  private async attachImage(argument: string): Promise<void> {
    const pathText = unquote(argument.trim());
    if (pathText === "") {
      this.write(`Image not attached: usage: ${imageCommand} <path>\n`);
      return;
    }
    if (this.pendingImages.length >= maxImagesPerMessage) {
      this.write(`Image not attached: a message carries at most ${maxImagesPerMessage} images.\n`);
      return;
    }
    const path =
      pathText === "~" || pathText.startsWith("~/")
        ? join(this.images.homeDirectory, pathText.slice(2))
        : resolve(process.cwd(), pathText);
    let data: Uint8Array;
    let mimeType: string;
    try {
      data = await readImageFile(path);
      mimeType = detectImageMimeType(data);
    } catch (error: unknown) {
      this.write(`Image not attached: ${error instanceof ImageValidationError ? `${path}: ` : ""}${describe(error)}\n`);
      return;
    }
    let reference: AgentImageReference;
    try {
      reference = await this.images.mediaStore.putImage(data);
    } catch (error: unknown) {
      this.write(`Image not attached: cannot store ${path}: ${describe(error)}\n`);
      return;
    }
    this.pendingImages.push(reference);
    this.write(
      `Attached ${basename(path)} (${mimeType}, ${formatSize(data.length)}); it goes with your next message (${countOf(this.pendingImages.length, "image")} attached).\n`,
    );
  }

  /** Waits until the session is idle with more than `turnCountBefore` finished turns. */
  private async awaitTurnEnd(turnCountBefore: number): Promise<void> {
    this.turnCancellation = new AbortController();
    this.isCancelRequested = false;
    try {
      for (;;) {
        if (this.isExitRequested) {
          return;
        }
        const snapshot = await readAgentSnapshot(this.client, this.flow, this.sessionId);
        const state = snapshot.state;
        if (state?.status === "failed") {
          this.endLine();
          this.write(`The session failed: ${state.lastTurnError ?? "unknown reason"}\n`);
          this.hasFailure = true;
          this.isExitRequested = true;
          return;
        }
        if (state !== null && state !== undefined && state.status === "waiting_for_user_message" && state.turnCount > turnCountBefore) {
          // A turn's ID is turn-<turnCount at its start + 1>, so the last finished turn is turn-<turnCount>.
          await this.finishTurn(snapshot, `turn-${state.turnCount}`);
          return;
        }
        for (const interrupt of snapshot.pendingInterrupts) {
          if (!this.handledInterruptIds.has(interrupt.interruptId) && !this.isCancelRequested) {
            this.handledInterruptIds.add(interrupt.interruptId);
            await this.answerInterrupt(interrupt);
          }
        }
        await delay(snapshotPollIntervalMs);
      }
    } finally {
      this.turnCancellation = undefined;
    }
  }

  private async finishTurn(snapshot: AgentSnapshot, turnId: string): Promise<void> {
    if (this.isEventStreamAvailable) {
      const deadline = Date.now() + eventCatchUpTimeoutMs;
      while (!this.endedTurns.has(turnId) && Date.now() < deadline) {
        await delay(50);
      }
    }
    const endEvent = this.endedTurns.get(turnId);
    if (!this.turnsWithText.has(turnId)) {
      const lastMessage = snapshot.messages.at(-1);
      if (lastMessage?.role === "assistant" && lastMessage.text !== null && lastMessage.text !== "" && endEvent !== "turn_cancelled") {
        this.write(lastMessage.text);
        this.isMidLine = !lastMessage.text.endsWith("\n");
      }
    }
    this.endLine();
    const isCancelled = endEvent === undefined ? this.isCancelRequested && !this.isEventStreamAvailable : endEvent === "turn_cancelled";
    if (isCancelled) {
      this.write("Turn cancelled.\n");
    } else if (snapshot.state?.lastTurnError !== null && snapshot.state?.lastTurnError !== undefined) {
      this.write(`Turn failed: ${snapshot.state.lastTurnError}\n`);
      this.hasFailure = true;
    }
  }

  private async answerInterrupt(interrupt: AgentInterrupt): Promise<void> {
    this.endLine();
    const signal = this.turnCancellation?.signal;
    if (interrupt.kind === "question") {
      this.write(`Question: ${interrupt.question ?? ""}\n`);
      const choices = interrupt.choices ?? [];
      choices.forEach((choice, index) => this.write(`  ${index + 1}) ${choice}\n`));
      this.write("Answer: ");
      const line = await this.input.nextLine(signal);
      if (line === null || line === lineWaitAborted) {
        return;
      }
      const choiceNumber = Number(line.trim());
      const answer =
        Number.isInteger(choiceNumber) && choiceNumber >= 1 && choiceNumber <= choices.length
          ? (choices[choiceNumber - 1] ?? line.trim())
          : line.trim();
      const isAccepted = await this.retryLockConflicts(() =>
        this.client.invokeRPC(this.flow.answerQuestion, this.sessionId, { interruptId: interrupt.interruptId, answer }),
      );
      this.write(isAccepted ? "" : "The question is no longer pending.\n");
      return;
    }
    this.write(`Tool ${interrupt.toolName} wants to run with ${interrupt.arguments}\n`);
    this.write(`Approve ${interrupt.toolName}? [y/n, or n <reason>]: `);
    const line = await this.input.nextLine(signal);
    if (line === null || line === lineWaitAborted) {
      return;
    }
    const answer = line.trim();
    const isApproved = /^y(es)?$/i.test(answer);
    const reason = isApproved ? null : answer.replace(/^no?\b\s*/i, "");
    const isResolved = await this.retryLockConflicts(() =>
      this.client.invokeRPC(this.flow.resolveInterrupt, this.sessionId, {
        interruptId: interrupt.interruptId,
        approved: isApproved,
        rejectionMessage: reason === null || reason === "" ? null : reason,
      }),
    );
    this.write(isResolved ? (isApproved ? "Approved.\n" : "Rejected.\n") : "The approval is no longer pending.\n");
  }

  private handleInterrupt(): void {
    if (this.turnCancellation !== undefined && !this.isCancelRequested) {
      this.isCancelRequested = true;
      this.endLine();
      this.write("Cancelling the turn; press Ctrl-C again to exit.\n");
      this.turnCancellation.abort();
      void this.retryLockConflicts(() =>
        this.client.invokeRPC(this.flow.cancelTurn, this.sessionId, { turnId: null }),
      ).catch((error: unknown) => this.write(`Cancelling failed: ${describe(error)}\n`));
      return;
    }
    this.endLine();
    this.isExitRequested = true;
    this.exitController.abort();
  }

  private exitSignal(): AbortSignal {
    return this.exitController.signal;
  }

  private async readEvents(): Promise<void> {
    let resumeToken: string;
    try {
      const page = await this.client.listStreamMessages(this.sessionId, this.flow.agentEvents, 1);
      resumeToken = page.messages[0]?.resumeToken ?? "";
    } catch (error: unknown) {
      this.isEventStreamAvailable = false;
      if (!isStreamStoreDisabled(error)) {
        this.write(`Live output is off: ${describe(error)}\n`);
      }
      return;
    }
    while (!this.eventReading.signal.aborted) {
      try {
        const message = await this.client.readStream(this.sessionId, this.flow.agentEvents, resumeToken, eventPollTimeoutMs);
        resumeToken = message.resumeToken;
        this.showEvent(message.value);
      } catch (error: unknown) {
        if (error instanceof LongPollTimeoutError) {
          continue;
        }
        if (isStreamStoreDisabled(error)) {
          this.isEventStreamAvailable = false;
          return;
        }
        if (this.eventReading.signal.aborted) {
          return;
        }
        await delay(1_000);
      }
    }
  }

  private showEvent(event: AgentEvent): void {
    const payload = parsePayload(event.payload);
    switch (event.type) {
      case "text_delta": {
        const text = typeof payload.text === "string" ? payload.text : "";
        if (text !== "" && event.turnId !== null) {
          this.turnsWithText.add(event.turnId);
        }
        this.write(text);
        this.isMidLine = text !== "" && !text.endsWith("\n");
        return;
      }
      case "tool_call_started":
        this.endLine();
        this.write(`[tool] ${String(payload.tool_name)} started\n`);
        return;
      case "tool_call_finished":
        this.endLine();
        this.write(`[tool] ${String(payload.tool_name)} ${String(payload.status)}\n`);
        return;
      default:
        if (turnEndEventTypes.has(event.type) && event.turnId !== null) {
          this.endedTurns.set(event.turnId, event.type);
        }
    }
  }

  private async retryLockConflicts(invoke: () => Promise<boolean>): Promise<boolean> {
    const deadline = Date.now() + lockConflictRetryTimeoutMs;
    for (;;) {
      try {
        return await invoke();
      } catch (error: unknown) {
        if (!(error instanceof RpcLockConflictError) || Date.now() >= deadline) {
          throw error;
        }
      }
      await delay(50);
    }
  }

  private endLine(): void {
    if (this.isMidLine) {
      this.write("\n");
      this.isMidLine = false;
    }
  }

  private write(text: string): void {
    if (text !== "") {
      this.output.write(text);
    }
  }
}

/** Reads an image file, refusing a missing, special, or oversized file before reading it. */
async function readImageFile(path: string): Promise<Uint8Array> {
  let size: number;
  try {
    const status = await stat(path);
    if (!status.isFile()) {
      throw new Error(`${path} is not a regular file`);
    }
    size = status.size;
  } catch (error: unknown) {
    if ((error as NodeJS.ErrnoException).code === "ENOENT") {
      throw new Error(`${path} does not exist`);
    }
    throw error;
  }
  if (size > maxImageBytes) {
    throw new Error(tooLargeMessage(path, size));
  }
  const handle = await open(path, "r");
  try {
    const buffer = Buffer.alloc(maxImageBytes + 1);
    let length = 0;
    while (length < buffer.length) {
      const { bytesRead } = await handle.read(buffer, length, buffer.length - length, null);
      if (bytesRead === 0) {
        break;
      }
      length += bytesRead;
    }
    if (length > maxImageBytes) {
      throw new Error(tooLargeMessage(path, length));
    }
    return buffer.subarray(0, length);
  } finally {
    await handle.close();
  }
}

function tooLargeMessage(path: string, size: number): string {
  return `${path} has ${size.toLocaleString("en-US")} bytes; an image may have at most ${maxImageBytes.toLocaleString("en-US")}`;
}

function formatSize(size: number): string {
  if (size < bytesPerKibibyte) {
    return `${size} bytes`;
  }
  if (size < bytesPerKibibyte * bytesPerKibibyte) {
    return `${Math.round(size / bytesPerKibibyte)} KB`;
  }
  return `${(size / (bytesPerKibibyte * bytesPerKibibyte)).toFixed(1)} MB`;
}

function countOf(count: number, noun: string): string {
  return count === 1 ? `1 ${noun}` : `${count} ${noun}s`;
}

/** Drops one pair of matching quotes, which terminals add around a dropped file's path. */
function unquote(text: string): string {
  return text.length >= 2 && text[0] === text.at(-1) && (text[0] === "'" || text[0] === '"') ? text.slice(1, -1) : text;
}

function parsePayload(payload: string): Record<string, unknown> {
  try {
    const value: unknown = JSON.parse(payload);
    return typeof value === "object" && value !== null ? (value as Record<string, unknown>) : {};
  } catch {
    return {};
  }
}

function isStreamStoreDisabled(error: unknown): boolean {
  return error instanceof DexServiceError && Number(error.code) === grpcFailedPrecondition;
}

function describe(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}
