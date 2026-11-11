// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { parentPort } from "node:worker_threads";

/** One file's text to match, with the expression's source and flags. */
interface GrepMatchRequest {
  readonly source: string;
  readonly flags: string;
  readonly lines: readonly string[];
}

// Runs in a worker thread, so a pattern that backtracks without end can be terminated.
parentPort?.on("message", (request: GrepMatchRequest) => {
  const expression = new RegExp(request.source, request.flags);
  const matchingIndexes: number[] = [];
  request.lines.forEach((line, index) => {
    if (expression.test(line)) {
      matchingIndexes.push(index);
    }
  });
  parentPort?.postMessage(matchingIndexes);
});
