// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { runAgentWorkerProcess } from "../../src/testing/index.js";
import { fakeMcpStateDirectoryVariable } from "./fake-mcp-server-names.js";
import {
  createMcpScriptedAgentFlow,
  mcpAgentGatewayUrlVariable,
  mcpAgentHttpUrlVariable,
  mcpAgentSessionsUrlVariable,
} from "./mcp-scripted-agent-runner.js";

const stateDirectory = process.env[fakeMcpStateDirectoryVariable];
const httpUrl = process.env[mcpAgentHttpUrlVariable];
if (stateDirectory === undefined || httpUrl === undefined) {
  throw new Error(`${fakeMcpStateDirectoryVariable} and ${mcpAgentHttpUrlVariable} must be set`);
}
function optionalUrl(variable: string): string | undefined {
  const url = process.env[variable];
  return url === undefined || url === "" ? undefined : url;
}

await runAgentWorkerProcess([
  createMcpScriptedAgentFlow(
    stateDirectory,
    httpUrl,
    optionalUrl(mcpAgentGatewayUrlVariable),
    optionalUrl(mcpAgentSessionsUrlVariable),
  ),
]);
