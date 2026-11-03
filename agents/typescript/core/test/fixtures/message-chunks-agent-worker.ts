// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { createAgentSessionFlow, type AgentFrameworkRunner } from "../../src/index.js";
import { runAgentWorkerProcess } from "../../src/testing/index.js";
import {
  EchoRunner,
  messageChunkMaxBytesVariable,
  messageChunkSizeVariable,
  messageChunksAgentFlowType,
  messageChunksMarkerDirectoryVariable,
  messageWindowSizeVariable,
} from "./message-chunks-runner.js";

const markerDirectory = process.env[messageChunksMarkerDirectoryVariable];
if (markerDirectory === undefined || markerDirectory === "") {
  throw new Error(`${messageChunksMarkerDirectoryVariable} is not set`);
}
const windowSize = process.env[messageWindowSizeVariable];
const chunkSize = process.env[messageChunkSizeVariable];
const chunkMaxBytes = process.env[messageChunkMaxBytesVariable];
await runAgentWorkerProcess([
  createAgentSessionFlow(
    // The runner keeps 0.1.0's shape and imports no core, so a released Worker can load it as well.
    new EchoRunner(markerDirectory) as unknown as AgentFrameworkRunner,
    messageChunksAgentFlowType,
    {
      ...(windowSize === undefined ? {} : { messageWindowSize: Number(windowSize) }),
      ...(chunkSize === undefined ? {} : { messageChunkSize: Number(chunkSize) }),
      ...(chunkMaxBytes === undefined ? {} : { messageChunkMaxBytes: Number(chunkMaxBytes) }),
    },
  ),
]);
