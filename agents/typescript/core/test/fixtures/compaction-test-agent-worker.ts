// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { runAgentWorkerProcess } from "../../src/testing/index.js";
import {
  compactionMarkerDirectoryVariable,
  compactionSummaryPadVariable,
  compactionWindowTokensVariable,
  createCompactionTestAgentFlow,
} from "./compaction-test-agent.js";

const markerDirectory = process.env[compactionMarkerDirectoryVariable];
if (markerDirectory === undefined || markerDirectory === "") {
  throw new Error(`${compactionMarkerDirectoryVariable} is not set`);
}
await runAgentWorkerProcess([
  createCompactionTestAgentFlow(
    markerDirectory,
    Number(process.env[compactionWindowTokensVariable] ?? "600"),
    Number(process.env[compactionSummaryPadVariable] ?? "0"),
  ),
]);
