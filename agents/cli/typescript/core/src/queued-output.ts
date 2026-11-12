// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { Writable } from "node:stream";

/** Longest wait for queued output before the command exits anyway, so a reader that never reads cannot hang it. */
export const queuedOutputWaitMs = 2_000;

/**
 * Waits until every earlier write to each stream reached the operating system, the stream failed or closed, or the
 * time ran out.
 *
 * @param streams - The streams, such as stdout and stderr.
 * @param timeoutMs - The longest wait.
 * @returns A Promise that resolves when each stream is done or the time ran out; it never rejects.
 */
export async function waitForQueuedOutput(streams: readonly Writable[], timeoutMs = queuedOutputWaitMs): Promise<void> {
  await Promise.all(streams.map((stream) => waitForQueuedStreamOutput(stream, timeoutMs)));
}

async function waitForQueuedStreamOutput(stream: Writable, timeoutMs: number): Promise<void> {
  const writableLength = (stream as Partial<Writable>).writableLength;
  if (typeof writableLength !== "number" || writableLength === 0 || stream.destroyed || stream.writableEnded) {
    return;
  }
  await new Promise<void>((resolve) => {
    let isDone = false;
    const done = (): void => {
      if (isDone) {
        return;
      }
      isDone = true;
      clearTimeout(timer);
      stream.off("close", done);
      stream.off("finish", done);
      // A failed write emits "error" after its callback; keep the listener until then, so it cannot crash the exit.
      setImmediate(() => stream.off("error", done));
      resolve();
    };
    const timer = setTimeout(done, timeoutMs);
    stream.once("error", done);
    stream.once("close", done);
    stream.once("finish", done);
    try {
      // Writes complete in order, so the callback of an empty write follows every queued one.
      stream.write("", () => done());
    } catch {
      done();
    }
  });
}
