// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

/** Environment variable naming the directory for the fake CLI's gates and invocation log. */
export const fakeClaudeStateDirectoryVariable = "FAKE_CLAUDE_STATE_DIRECTORY";
/** File name of the invocation log inside the state directory. */
export const fakeClaudeInvocationLogName = "invocations.log";
/** File name of the log with one JSON line per CLI start: `kind` and {@link fakeClaudeRecordedVariables}. */
export const fakeClaudeEnvironmentLogName = "cli-environments.jsonl";
/** Variables the fake CLI records at each start, `null` when unset. */
export const fakeClaudeRecordedVariables = [
  "CLAUDE_CONFIG_DIR",
  "CLAUDE_SECURESTORAGE_CONFIG_DIR",
  "CLAUDE_CODE_ENTRYPOINT",
  "CLAUDE_AGENT_SDK_VERSION",
  "TRACEPARENT",
  "TRACESTATE",
] as const;
/** Set to `1` to make the fake CLI resume a deferred call without running the PreToolUse hooks. */
export const fakeClaudeResumeWithoutHooksVariable = "FAKE_CLAUDE_RESUME_WITHOUT_HOOKS";
/** Model the fake CLI reports in its `init` message and keys `modelUsage` by. */
export const fakeClaudeRequestModel = "claude-scripted";
/** Dated model the fake CLI's replies name, as Messages API responses do. */
export const fakeClaudeResponseModel = "claude-scripted-20261101";
/** Model that answers the subagent an `Agent` tool call runs in the fake CLI. */
export const fakeClaudeSubagentResponseModel = "claude-scripted-subagent-20261101";
