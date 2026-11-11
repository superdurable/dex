// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// The scripted test harness as a command, the way a harness package's bin calls the runtime.

import { processAgentCliIo, runAgentCli, type AgentHarness } from "../../src/index.js";
import {
  scriptedHarness,
  scriptedHarnessVariantVariable,
  scriptedHarnessWithoutImages,
  shellReplacingScriptedHarness,
} from "./scripted-harness.js";

const variants: Readonly<Record<string, AgentHarness>> = {
  "shell-replacing": shellReplacingScriptedHarness,
  "without-images": scriptedHarnessWithoutImages,
};
const harness = variants[process.env[scriptedHarnessVariantVariable] ?? ""] ?? scriptedHarness;
process.exit(await runAgentCli(harness, processAgentCliIo()));
