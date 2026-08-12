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

import io.superdurable.gen.StepCompletionOutput;
import io.superdurable.gen.Value;

import java.util.function.BiFunction;

/**
 * Contains one output-bearing Step completion returned by {@link Client#waitForFlow(String)}.
 *
 * <p>A Flow may return multiple completions, including multiple executions of the same Step type.
 * Use {@link #getStepExecutionId()} when execution identity matters. The list order reflects server
 * collection order and is not deterministic for parallel Steps.
 */
public final class StepCompletion {
    private final String stepType;
    private final String stepExecutionId;
    private final Value output;
    private final BiFunction<Value, Class<?>, Object> decoder;

    StepCompletion(
            final StepCompletionOutput completion,
            final BiFunction<Value, Class<?>, Object> decoder) {
        if (!completion.hasCompletedStepOutput()) {
            throw new IllegalArgumentException("Step completion output is required");
        }
        this.stepType = completion.getCompletedStepType();
        this.stepExecutionId = completion.getCompletedStepExecutionId();
        this.output = completion.getCompletedStepOutput();
        this.decoder = decoder;
    }

    /**
     * Returns the registered Step type that produced this output.
     *
     * @return the durable Step type
     */
    public String getStepType() {
        return stepType;
    }

    /**
     * Returns the server identity of the exact Step execution that produced this output.
     *
     * @return the Step execution ID
     */
    public String getStepExecutionId() {
        return stepExecutionId;
    }

    /**
     * Decodes this Step output as a concrete Java class.
     *
     * <p>The value has already been hydrated before {@code waitForFlow} returns. This method may be
     * called repeatedly with compatible target classes.
     *
     * @param outputType the concrete Java output class
     * @param <T> the expected output type
     * @return the decoded Step output
     * @throws io.superdurable.dex.exceptions.ValueMappingException if the value cannot be decoded
     */
    public <T> T getOutput(final Class<T> outputType) {
        return outputType.cast(decoder.apply(output, outputType));
    }
}
