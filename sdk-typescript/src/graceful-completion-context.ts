// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { Context } from "./context.js";
import { InvocationContext } from "./invocation-context.js";

/**
 * Reports whether the graceful completion condition of the current Wait completed.
 *
 * Call it in `execute` after a Wait that includes `GracefulCompletion.requested()`. It reads the
 * condition results of the current `execute` call and never sends a request. It returns true when
 * the Flow had a graceful completion request as the Wait completed. That holds even when another
 * `Wait.anyOf` condition won, such as a Timer that fired or a Channel that consumed a message.
 * Read consumed Channel messages first, so a message that won is not dropped.
 *
 * It returns false when the Wait has no such condition, outside `execute`, and after
 * `Wait.skipImmediately`. It also returns false for a Context that Dex did not create, such as
 * an application's own `Context` implementation or a test fake. It is a function rather than a
 * `Context` member, so such implementations keep compiling.
 *
 * @example
 * ```ts
 * public execute(context: Context): StepDecision {
 *   const received = messages.results(context);
 *   if (received.length > 0) {
 *     return goTo(new HandleMessageStep(), received[0]);
 *   }
 *   if (isGracefulCompletionConditionCompleted(context)) {
 *     return deadEnd();
 *   }
 *   return goTo(this, undefined);
 * }
 * ```
 *
 * @param context - The Context that Dex passed to the current Step method.
 * @returns Whether the graceful completion condition of the current Wait completed.
 * @since 1.45.6
 */
export function isGracefulCompletionConditionCompleted(context: Context): boolean {
  return context instanceof InvocationContext && context.isGracefulCompletionConditionCompleted();
}

/**
 * Reports whether the connected Dex Server supports the graceful completion condition.
 *
 * Use it in `waitFor` to add `GracefulCompletion.requested()` only when the Server supports it.
 * With an older Server, a Wait that contains the condition fails with
 * `GracefulCompletionConditionUnsupportedError` after `waitFor` returns, and Step code cannot
 * catch that error. Check this function first and keep the Wait without the condition instead.
 *
 * It reads the Server protocol version that the Worker negotiated with `GetServerInfo` when it
 * started. It never sends a request, so it is cheap to call on every invocation. It returns true
 * when that version is 7 or later. It works in `waitFor`, `execute`, and RPC handlers alike. The
 * Worker negotiates before it receives any task, so a Context that Dex created always sees the
 * negotiated version. It returns false for a Context that Dex did not create, such as an
 * application's own `Context` implementation or a test fake.
 *
 * @example
 * ```ts
 * public waitFor(context: Context): Wait {
 *   if (supportsGracefulCompletionCondition(context)) {
 *     return Wait.anyOf(userMessages.forOne(), GracefulCompletion.requested());
 *   }
 *   return Wait.until(userMessages.forOne());
 * }
 * ```
 *
 * @param context - The Context that Dex passed to the current Step or RPC method.
 * @returns Whether the Worker's negotiated Server protocol supports the graceful completion
 * condition.
 * @since 1.45.6
 */
export function supportsGracefulCompletionCondition(context: Context): boolean {
  return context instanceof InvocationContext && context.supportsGracefulCompletionCondition();
}
