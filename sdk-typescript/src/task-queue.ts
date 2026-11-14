// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

export const namedTaskQueueMinimumServerProtocolVersion: number = 5;

const reservedTaskQueuePrefix = "dex.";
const defaultTaskQueueAlias = "dex.default";
const taskQueueNamePattern = /^[A-Za-z0-9._-]{1,63}$/;

/** Throws when a pull Worker may not register `taskQueue`; the empty default queue is valid. */
export function validatePullWorkerTaskQueue(taskQueue: string): void {
  if (typeof taskQueue !== "string") {
    throw new TypeError("taskQueue must be a string");
  }
  if (taskQueue === "") return;
  if (taskQueue.startsWith(reservedTaskQueuePrefix)) {
    throw new TypeError(`taskQueue ${JSON.stringify(taskQueue)} is invalid: names starting with "dex." are reserved`);
  }
  requireTaskQueueCharacters(taskQueue);
}

/** Throws when a Flow's Worker target may not carry `taskQueue`; also accepts `dex.default`. */
export function validateFlowTaskQueue(taskQueue: string): void {
  if (taskQueue === defaultTaskQueueAlias) return;
  validatePullWorkerTaskQueue(taskQueue);
}

/** Throws when a non-empty task queue would reach a Server that ignores it. */
export function requireNamedTaskQueueSupport(
  negotiatedServerProtocolVersion: number,
  taskQueue: string,
  operation: string,
): void {
  if (taskQueue === "" || negotiatedServerProtocolVersion >= namedTaskQueueMinimumServerProtocolVersion) {
    return;
  }
  throw new Error(
    `${operation} with task queue ${JSON.stringify(taskQueue)} requires Server protocol version `
      + `${namedTaskQueueMinimumServerProtocolVersion} or later; negotiated protocol version is `
      + `${negotiatedServerProtocolVersion}. Upgrade the Server, or omit taskQueue to use the default task queue.`,
  );
}

function requireTaskQueueCharacters(taskQueue: string): void {
  if (!taskQueueNamePattern.test(taskQueue)) {
    throw new TypeError(
      `taskQueue ${JSON.stringify(taskQueue)} is invalid: use 1-63 ASCII letters, digits, dots, underscores, or hyphens`,
    );
  }
}
