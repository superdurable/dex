// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { Duplex } from "node:stream";

/** The longest line on the channel, in bytes of UTF-8 without the newline. */
export const maxChannelLineBytes = 1_048_576;
const newline = 0x0a;

/** One message received on the channel: a JSON object with a string `type`. */
export type ChannelMessage = Readonly<Record<string, unknown>> & { readonly type: string };

/** What the channel reports to its owner. */
export interface ChannelListener {
  /** One message, in order. Called synchronously for every complete line of a chunk. */
  onMessage(message: ChannelMessage): void;
  /** A line broke the framing rules; the channel is closed. */
  onProtocolError(reason: string): void;
  /** The peer closed the channel, or it broke. */
  onEnd(): void;
}

/**
 * The launcher's end of the terminal UI channel: newline-delimited compact JSON over one end of a Unix stream
 * socket, at most 1 MiB per line. Any framing error ends the contract, so the channel closes itself.
 */
export class TerminalUiChannel {
  private buffered: Buffer[] = [];
  private bufferedBytes = 0;
  private isEnded = false;

  /**
   * Starts reading.
   * @param socket - The socket end.
   * @param listener - Receives the messages.
   */
  public constructor(
    private readonly socket: Duplex,
    private readonly listener: ChannelListener,
  ) {
    socket.on("data", (chunk: Buffer) => this.receive(chunk));
    socket.on("end", () => this.end());
    socket.on("close", () => this.end());
    // A peer that closed while we write gives EPIPE: that is the end of the contract, not a crash.
    socket.on("error", () => this.end());
  }

  /** Whether the channel ended. */
  public get hasEnded(): boolean {
    return this.isEnded;
  }

  /**
   * Sends one message as compact JSON on one line. A message over 1 MiB is a bug in the caller.
   * @param message - The message.
   */
  public send(message: Readonly<Record<string, unknown>>): void {
    if (this.isEnded) {
      return;
    }
    const line = JSON.stringify(message);
    if (Buffer.byteLength(line, "utf8") > maxChannelLineBytes) {
      throw new RangeError(`a ${String(message.type)} message is longer than 1 MiB`);
    }
    this.socket.write(`${line}\n`);
  }

  /** Closes the launcher's end; the terminal UI reads EOF. */
  public close(): void {
    this.socket.end();
    this.socket.destroy();
    this.end();
  }

  private receive(chunk: Buffer): void {
    let start = 0;
    for (;;) {
      if (this.isEnded) {
        return;
      }
      const index = chunk.indexOf(newline, start);
      if (index < 0) {
        this.buffer(chunk.subarray(start));
        return;
      }
      const piece = chunk.subarray(start, index);
      start = index + 1;
      const lineBytes = this.bufferedBytes + piece.length;
      const line = this.buffered.length === 0 ? piece : Buffer.concat([...this.buffered, piece]);
      this.buffered = [];
      this.bufferedBytes = 0;
      if (lineBytes > maxChannelLineBytes) {
        this.fail("a line is longer than 1 MiB");
        return;
      }
      const message = parseChannelLine(line);
      if (typeof message === "string") {
        this.fail(message);
        return;
      }
      this.listener.onMessage(message);
    }
  }

  private buffer(piece: Buffer): void {
    if (piece.length === 0) {
      return;
    }
    this.bufferedBytes += piece.length;
    if (this.bufferedBytes > maxChannelLineBytes) {
      this.fail("a line is longer than 1 MiB");
      return;
    }
    this.buffered.push(piece);
  }

  private fail(reason: string): void {
    if (this.isEnded) {
      return;
    }
    this.listener.onProtocolError(reason);
    this.close();
  }

  private end(): void {
    if (this.isEnded) {
      return;
    }
    this.isEnded = true;
    this.listener.onEnd();
  }
}

/** Returns the message of one line, or why the line breaks the framing rules. */
function parseChannelLine(line: Buffer): ChannelMessage | string {
  let text: string;
  try {
    text = new TextDecoder("utf-8", { fatal: true }).decode(line);
  } catch {
    return "a line is not UTF-8";
  }
  let value: unknown;
  try {
    value = JSON.parse(text);
  } catch {
    return "a line is not JSON";
  }
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    return "a line is not a JSON object";
  }
  const record = value as Record<string, unknown>;
  return typeof record.type === "string" ? (record as ChannelMessage) : "a message has no string type";
}
