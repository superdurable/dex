/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Super Durable Source License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Super-Durable-1.0
 */

package io.superdurable.dex;

import java.util.ArrayList;
import java.util.Collections;
import java.util.List;

/**
 * Contains every output-bearing Step completion from a successfully completed Flow.
 *
 * <p>The completion list is immutable and preserves server collection order. Parallel Step order
 * is not deterministic, so select completions by Step type or Step execution ID.
 */
public final class WaitForFlowResult {
    private final List<StepCompletion> completions;

    WaitForFlowResult(final List<StepCompletion> completions) {
        this.completions = Collections.unmodifiableList(
                new ArrayList<StepCompletion>(completions));
    }

    /**
     * Returns all output-bearing Step completions.
     *
     * @return an immutable list in server collection order
     */
    public List<StepCompletion> getCompletions() {
        return completions;
    }

    /**
     * Decodes the output when exactly one Step completion exists.
     *
     * @param outputType the concrete Java output class
     * @param <T> the expected output type
     * @return the only decoded Step output
     * @throws IllegalStateException if the Flow returned zero or multiple outputs
     * @throws io.superdurable.dex.exceptions.ValueMappingException if the value cannot be decoded
     */
    public <T> T getSingleOutput(final Class<T> outputType) {
        if (completions.size() != 1) {
            throw new IllegalStateException(
                    "Expected exactly one Step output, found " + completions.size());
        }
        return completions.get(0).getOutput(outputType);
    }
}
