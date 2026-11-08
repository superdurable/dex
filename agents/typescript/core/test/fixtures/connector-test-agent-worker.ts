// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { runAgentWorkerProcess } from "../../src/testing/index.js";
import {
  connectorAgentDroppedToolsVariable,
  connectorAgentMaxResultBytesVariable,
  createConnectorAgentFlow,
} from "./connector-test-agent.js";
import { fakeLedgerStateDirectoryVariable } from "./fake-ledger.js";

const stateDirectory = process.env[fakeLedgerStateDirectoryVariable];
if (stateDirectory === undefined || stateDirectory === "") {
  throw new Error(`${fakeLedgerStateDirectoryVariable} is not set`);
}
const limit = process.env[connectorAgentMaxResultBytesVariable];
const dropped = (process.env[connectorAgentDroppedToolsVariable] ?? "").split(",").filter((name) => name !== "");
await runAgentWorkerProcess([
  createConnectorAgentFlow(stateDirectory, limit === undefined || limit === "" ? undefined : Number(limit), new Set(dropped)),
]);
