// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { runAgentWorkerProcess } from "@superdurable/dex-agents/testing";

import { createCollidingConnectorMastraAgentFlow, createConnectorMastraAgentFlow } from "./connector-mastra-agent.js";
import { fakeLedgerStateDirectoryVariable } from "./fake-ledger.js";

const stateDirectory = process.env[fakeLedgerStateDirectoryVariable];
if (stateDirectory === undefined || stateDirectory === "") {
  throw new Error(`${fakeLedgerStateDirectoryVariable} is not set`);
}
await runAgentWorkerProcess([
  createConnectorMastraAgentFlow(stateDirectory),
  createCollidingConnectorMastraAgentFlow(stateDirectory),
]);
