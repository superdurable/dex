#!/usr/bin/env node
// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { processAgentCliIo, runAgentCli } from "@superdurable/dex-agents-cli-core";

import { claudeSdkHarness } from "./claude-sdk-harness.js";

// Exit explicitly: open connections can keep Node running after the chat ends.
process.exit(await runAgentCli(claudeSdkHarness, processAgentCliIo()));
