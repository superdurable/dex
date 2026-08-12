// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import type { Codec } from "./codec.js";
import type { StepCompletionOutput, Value } from "./gen/dex.js";
import { decodeValue } from "./value-mapper.js";

/** Contains one output-bearing Step completion returned by {@link Client.waitForFlow}. */
export interface StepCompletion {
  /** Registered Step type that produced this output. */
  readonly stepType: string;
  /** Exact server Step execution identity that produced this output. */
  readonly stepExecutionId: string;
  /**
   * Decodes this already hydrated Step output.
   * @typeParam Output - Expected application output type.
   * @param codec - Codec for the expected output type.
   * @returns The decoded Step output.
   */
  decode<Output>(codec: Codec<Output>): Output;
}

/** Contains every output-bearing completion from a successfully completed Flow. */
export interface WaitForFlowResult {
  /**
   * Immutable completions in server collection order. Parallel Step order is not deterministic.
   */
  readonly completions: readonly StepCompletion[];
  /**
   * Decodes the output when exactly one completion exists.
   * @typeParam Output - Expected application output type.
   * @param codec - Codec for the expected output type.
   * @returns The only decoded Step output.
   * @throws {@link TypeError} when the Flow returned zero or multiple outputs.
   */
  singleOutput<Output>(codec: Codec<Output>): Output;
}

/** @internal */
export function createStepCompletions(
  records: readonly StepCompletionOutput[],
  values: readonly Value[],
): readonly StepCompletion[] {
  return Object.freeze(records.map((record, index) => {
    const value = values[index];
    if (value === undefined) {
      throw new TypeError(`Step completion ${index} has no output`);
    }
    return Object.freeze({
      stepType: record.completedStepType,
      stepExecutionId: record.completedStepExecutionId,
      decode<Output>(codec: Codec<Output>): Output {
        return decodeValue(codec, value);
      },
    });
  }));
}

/** @internal */
export function createWaitForFlowResult(
  completions: readonly StepCompletion[],
): WaitForFlowResult {
  return Object.freeze({
    completions,
    singleOutput<Output>(codec: Codec<Output>): Output {
      if (completions.length !== 1) {
        throw new TypeError(`Expected exactly one Step output, found ${completions.length}`);
      }
      return completions[0]!.decode(codec);
    },
  });
}
