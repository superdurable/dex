// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { appendFileSync, existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { setTimeout as delay } from "node:timers/promises";

import { pollUntil } from "./polling.js";

const gatePollIntervalMs = 50;

/**
 * Pauses code in a Worker child at an exact point until the test releases it.
 *
 * The child calls {@link markReached} and then awaits {@link waitForRelease}. The test awaits
 * {@link waitUntilReached} and then kills the Worker, or calls {@link release}. Both sides construct
 * the gate with the same directory and name. The gate survives Worker kills, so a replacement Worker
 * passes a released gate immediately. It mirrors the Python core's `FileGate`.
 *
 * @example
 * ```ts
 * // In the tool, inside the Worker child:
 * const gate = new FileGate(stateDirectory, "charge");
 * gate.markReached();
 * await gate.waitForRelease(call.cancellationSignal);
 *
 * // In the test:
 * await new FileGate(stateDirectory, "charge").waitUntilReached();
 * await worker.kill();
 * ```
 */
export class FileGate {
  private readonly reachedPath: string;
  private readonly releasedPath: string;

  /**
   * Names a gate inside a directory both processes can read.
   * @param directory - Shared directory, created when needed.
   * @param name - Gate name, unique within the directory.
   */
  public constructor(directory: string, name: string) {
    this.reachedPath = join(directory, `${name}.reached`);
    this.releasedPath = join(directory, `${name}.released`);
  }

  /** Records that code reached the gate; repeated calls count each arrival. */
  public markReached(): void {
    mkdirSync(dirname(this.reachedPath), { recursive: true });
    appendFileSync(this.reachedPath, "reached\n");
  }

  /**
   * Returns how many times code reached the gate.
   * @returns The number of {@link markReached} calls across all processes.
   */
  public reachedCount(): number {
    return readLines(this.reachedPath).length;
  }

  /**
   * Reports whether the test released the gate.
   * @returns `true` after {@link release}.
   */
  public isReleased(): boolean {
    return existsSync(this.releasedPath);
  }

  /** Lets current and future waiters pass the gate. */
  public release(): void {
    mkdirSync(dirname(this.releasedPath), { recursive: true });
    writeFileSync(this.releasedPath, "");
  }

  /**
   * Waits until the test releases the gate.
   *
   * It never times out: the test either releases the gate or kills the waiting process. Pass the
   * attempt's cancellation signal so a canceled attempt stops waiting.
   * @param signal - Optional signal that ends the wait with an `AbortError`.
   * @returns A Promise that resolves once the gate is released.
   */
  public async waitForRelease(signal?: AbortSignal): Promise<void> {
    while (!this.isReleased()) {
      await delay(gatePollIntervalMs, undefined, signal === undefined ? {} : { signal });
    }
  }

  /**
   * Waits until code reached the gate `count` times.
   * @param count - Arrivals to wait for. Defaults to 1.
   * @param timeoutMs - Maximum wait in milliseconds. Defaults to 90,000.
   * @returns A Promise that resolves once the gate was reached often enough.
   * @throws {Error} When the timeout passes first.
   */
  public async waitUntilReached(count = 1, timeoutMs = 90_000): Promise<void> {
    await pollUntil(() => (this.reachedCount() >= count ? true : undefined), {
      description: `gate ${this.reachedPath} reached ${count} time(s)`,
      timeoutMs,
      intervalMs: gatePollIntervalMs,
    });
  }
}

/**
 * Counts how often named operations ran, across Worker processes.
 *
 * A tool in the Worker child records each real run; the test then asserts that a write ran exactly
 * once, even across a Worker kill. Each record is appended synchronously, so it survives a SIGKILL
 * right after. It mirrors the Python core's `InvocationLog`.
 *
 * @example
 * ```ts
 * new InvocationLog(join(stateDirectory, "invocations.log")).record("send_email");
 * assert.equal(new InvocationLog(join(stateDirectory, "invocations.log")).count("send_email"), 1);
 * ```
 */
export class InvocationLog {
  /**
   * Uses one append-only file shared by the test and its Workers.
   * @param path - Log file, created on the first record.
   */
  public constructor(private readonly path: string) {}

  /**
   * Appends one run of `name`.
   * @param name - Operation name without line breaks.
   * @throws {TypeError} When `name` contains a line break.
   */
  public record(name: string): void {
    if (/[\r\n]/.test(name)) {
      throw new TypeError("InvocationLog names cannot contain line breaks");
    }
    mkdirSync(dirname(this.path), { recursive: true });
    appendFileSync(this.path, `${name}\n`);
  }

  /**
   * Returns how many runs of `name` were recorded.
   * @param name - Operation name.
   * @returns The number of matching records.
   */
  public count(name: string): number {
    return this.names().filter((recordedName) => recordedName === name).length;
  }

  /**
   * Returns every recorded name in order.
   * @returns The records, oldest first, or an empty array before the first record.
   */
  public names(): string[] {
    return readLines(this.path);
  }
}

function readLines(path: string): string[] {
  if (!existsSync(path)) {
    return [];
  }
  return readFileSync(path, "utf8")
    .split("\n")
    .filter((line) => line !== "");
}
