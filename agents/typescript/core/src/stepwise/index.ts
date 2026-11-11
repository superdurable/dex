// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

/**
 * Deprecated alias of `@superdurable/dex-agents/builtin-loop`, the built-in agent loop.
 *
 * Every name it exported still works and refers to the same value. Two names changed:
 * `StepwiseAgentRunner` is `BuiltinLoopAgentRunner`, and `createStepwiseAgentSessionFlow` is
 * `createBuiltinAgentSessionFlow`.
 *
 * @example
 * ```ts
 * // Before
 * import { createStepwiseAgentSessionFlow } from "@superdurable/dex-agents/stepwise";
 * // After
 * import { createBuiltinAgentSessionFlow } from "@superdurable/dex-agents/builtin-loop";
 * ```
 *
 * @deprecated Since 0.5.0. Import from `@superdurable/dex-agents/builtin-loop` instead.
 * @module
 */

import { createBuiltinAgentSessionFlow } from "../builtin-loop/builtin-agent-session-flow.js";
import { BuiltinLoopAgentRunner } from "../builtin-loop/builtin-loop-agent-runner.js";

export * from "../builtin-loop/index.js";

/**
 * The {@link BuiltinLoopAgentRunner} under its old name.
 *
 * @deprecated Since 0.5.0. Use `BuiltinLoopAgentRunner` from `@superdurable/dex-agents/builtin-loop`; it is the
 * same class.
 */
export const StepwiseAgentRunner = BuiltinLoopAgentRunner;

/**
 * The {@link BuiltinLoopAgentRunner} type under its old name.
 *
 * @deprecated Since 0.5.0. Use `BuiltinLoopAgentRunner` from `@superdurable/dex-agents/builtin-loop`.
 */
export type StepwiseAgentRunner = BuiltinLoopAgentRunner;

/**
 * {@link createBuiltinAgentSessionFlow} under its old name.
 *
 * @deprecated Since 0.5.0. Use `createBuiltinAgentSessionFlow` from `@superdurable/dex-agents/builtin-loop`; it
 * is the same function.
 */
export const createStepwiseAgentSessionFlow = createBuiltinAgentSessionFlow;
