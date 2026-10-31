// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { runAgentWorkerProcess } from "@superdurable/dex-agents/testing";

import { createClaudeTestAgentFlow } from "./claude-test-agent.js";
import { fakeClaudeStateDirectoryVariable } from "./fake-claude-cli-environment.js";

const stateDirectory = process.env[fakeClaudeStateDirectoryVariable];
if (stateDirectory === undefined || stateDirectory === "") {
  throw new Error(`${fakeClaudeStateDirectoryVariable} is not set`);
}
await runAgentWorkerProcess([createClaudeTestAgentFlow(stateDirectory)]);
