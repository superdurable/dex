// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { fileURLToPath } from "node:url";

/** Environment variable naming the directory for the fake MCP server's invocation log and gates. */
export const fakeMcpStateDirectoryVariable = "FAKE_MCP_STATE_DIRECTORY";
/** File name of the invocation log inside the state directory. */
export const fakeMcpInvocationLogName = "mcp-invocations.log";
/** Line the HTTP server prints once it listens. */
export const fakeMcpReadyLine = "fake MCP server listening";
/** Compiled entry module of the fake MCP server. */
export const fakeMcpServerModulePath = fileURLToPath(new URL("./fake-mcp-server.js", import.meta.url));
/** Compiled entry module of the fake MCP gateway. */
export const fakeMcpGatewayModulePath = fileURLToPath(new URL("./fake-mcp-gateway.js", import.meta.url));
