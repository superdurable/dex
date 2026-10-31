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
/** Set to `1` to make the fake CLI resume a deferred call without running the PreToolUse hooks. */
export const fakeClaudeResumeWithoutHooksVariable = "FAKE_CLAUDE_RESUME_WITHOUT_HOOKS";
