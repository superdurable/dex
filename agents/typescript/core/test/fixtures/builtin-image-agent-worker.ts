// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { join } from "node:path";

import { LocalDirectoryMediaStore } from "../../src/index.js";
import { runAgentWorkerProcess } from "../../src/testing/index.js";
import { createBuiltinImageAgentFlows, builtinImageAgentStateDirectoryVariable } from "./builtin-image-agent.js";
import { mediaDirectoryName } from "./image-test-agent.js";

const stateDirectory = process.env[builtinImageAgentStateDirectoryVariable];
if (stateDirectory === undefined || stateDirectory === "") {
  throw new Error(`${builtinImageAgentStateDirectoryVariable} is not set`);
}
await runAgentWorkerProcess(
  createBuiltinImageAgentFlows(stateDirectory, new LocalDirectoryMediaStore(join(stateDirectory, mediaDirectoryName))),
);
