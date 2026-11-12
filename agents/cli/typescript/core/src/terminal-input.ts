// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { createInterface, type Interface } from "node:readline";
import type { Readable, Writable } from "node:stream";

/** What {@link TerminalInput.nextLine} returns when its wait was aborted. */
export const lineWaitAborted = Symbol("lineWaitAborted");

/**
 * Lines and Ctrl-C from the terminal, or from piped input.
 *
 * Lines typed while nobody waits are queued in order. Ctrl-C arrives as readline's `SIGINT` on a terminal and as
 * the process's `SIGINT` signal otherwise; both call the interrupt handler.
 */
export class TerminalInput {
  private readonly lines: string[] = [];
  private readonly waiters: ((line: string | null) => void)[] = [];
  private readonly readline: Interface;
  private isClosed = false;
  private interruptHandler: () => void = () => undefined;
  private readonly onProcessInterrupt = (): void => this.interruptHandler();

  /**
   * Starts reading.
   * @param input - The input stream.
   * @param output - The output stream, used for echo on a terminal.
   * @param linesTypedAhead - Lines that an earlier reader of the same input left unread, returned first.
   */
  public constructor(input: Readable, output: Writable, linesTypedAhead: readonly string[] = []) {
    this.lines.push(...linesTypedAhead);
    const isTerminal = (input as { isTTY?: boolean }).isTTY === true;
    this.readline = createInterface({ input, ...(isTerminal ? { output, terminal: true } : { terminal: false }) });
    this.readline.on("line", (line) => this.deliver(line));
    this.readline.on("close", () => {
      this.isClosed = true;
      for (const waiter of this.waiters.splice(0)) {
        waiter(null);
      }
    });
    this.readline.on("SIGINT", () => this.interruptHandler());
    process.on("SIGINT", this.onProcessInterrupt);
  }

  /**
   * Sets what Ctrl-C does.
   * @param handler - Called on each Ctrl-C.
   */
  public onInterrupt(handler: () => void): void {
    this.interruptHandler = handler;
  }

  /**
   * Returns the next line.
   * @param signal - Ends the wait early.
   * @returns The line, `null` when input ended, or {@link lineWaitAborted}.
   */
  public nextLine(signal?: AbortSignal): Promise<string | null | typeof lineWaitAborted> {
    const queued = this.lines.shift();
    if (queued !== undefined) {
      return Promise.resolve(queued);
    }
    if (this.isClosed) {
      return Promise.resolve(null);
    }
    if (signal?.aborted === true) {
      return Promise.resolve(lineWaitAborted);
    }
    return new Promise((resolvePromise) => {
      const waiter = (line: string | null): void => {
        signal?.removeEventListener("abort", onAbort);
        resolvePromise(line);
      };
      const onAbort = (): void => {
        const index = this.waiters.indexOf(waiter);
        if (index >= 0) {
          this.waiters.splice(index, 1);
        }
        resolvePromise(lineWaitAborted);
      };
      signal?.addEventListener("abort", onAbort, { once: true });
      this.waiters.push(waiter);
    });
  }

  /**
   * Stops reading and removes the signal handler.
   * @returns The lines typed but not yet read, for a later reader of the same input.
   */
  public close(): string[] {
    process.off("SIGINT", this.onProcessInterrupt);
    this.readline.close();
    return this.lines.splice(0);
  }

  private deliver(line: string): void {
    const waiter = this.waiters.shift();
    if (waiter === undefined) {
      this.lines.push(line);
    } else {
      waiter(line);
    }
  }
}
