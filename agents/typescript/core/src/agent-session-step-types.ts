// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

/** Step types of every agent session Flow, shared with the Python core. */
export const agentSessionStepTypes = {
  startAgentSession: "StartAgentSession",
  awaitUserMessage: "AwaitUserMessage",
  runModel: "RunModel",
  compactContext: "CompactContext",
  awaitToolApproval: "AwaitToolApproval",
  awaitQuestionAnswer: "AwaitQuestionAnswer",
  awaitTimer: "AwaitTimer",
  callTool: "CallTool",
  callSingleAttemptTool: "CallSingleAttemptTool",
  pollToolTask: "PollToolTask",
  recordToolFailure: "RecordToolFailure",
  recordToolOutcomeUnknown: "RecordToolOutcomeUnknown",
  resumeTurn: "ResumeTurn",
  applyTurnControl: "ApplyTurnControl",
  recordTurnFailure: "RecordTurnFailure",
  finishTurnCancellation: "FinishTurnCancellation",
  cancelToolTask: "CancelToolTask",
} as const;
