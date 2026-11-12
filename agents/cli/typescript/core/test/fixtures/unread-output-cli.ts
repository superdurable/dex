// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// Writes more than a pipe holds, waits for it with the given bound, then exits.
import { waitForQueuedOutput } from "../../src/queued-output.js";

const timeoutMs = Number(process.argv[2]);
const line = `${"x".repeat(1_023)}\n`;
for (let index = 0; index < 1_024; index += 1) {
  process.stdout.write(line);
}
await waitForQueuedOutput([process.stdout, process.stderr], timeoutMs);
process.exit(0);
