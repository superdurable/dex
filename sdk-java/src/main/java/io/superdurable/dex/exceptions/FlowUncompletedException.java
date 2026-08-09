/*
 * Legacy Materials in this file remain under their original licenses.
 * See LEGACY_NOTICES.md.
 */

/*
 * Modifications Copyright (c) 2026 Super Durable, Inc.
 *
 * Modifications after the Legacy Cutoff are licensed under the
 * Super Durable Source License 1.0.
 * Legacy Materials remain under their original licenses.
 * See LICENSE and LEGACY_NOTICES.md.
 */

package io.superdurable.dex.exceptions;

import io.superdurable.dex.FlowErrorType;
import io.superdurable.dex.FlowStatus;
import io.superdurable.gen.StepCompletionOutput;
import io.superdurable.gen.Value;

import java.util.ArrayList;
import java.util.Collections;
import java.util.List;
import java.util.function.BiFunction;

/**
 * Reports that a blocking Flow wait reached an abnormal terminal status.
 *
 * <p>The exception preserves the run identity, terminal status, durable error category, message,
 * and any completed Step outputs returned by Dex. Use {@link #getResult} with a concrete class to
 * decode a selected result.
 */
public final class FlowUncompletedException extends RuntimeException {
    private final String runId;
    private final FlowStatus status;
    private final FlowErrorType errorType;
    private final List<StepCompletionOutput> results;
    private final BiFunction<Value, Class<?>, Object> decoder;

    /**
     * Creates an abnormal-completion exception with lazily decodable Step results.
     *
     * @param runId the run ID that reached a terminal status
     * @param status the abnormal terminal Flow status
     * @param errorType the durable Flow error category, or {@code null} when unavailable
     * @param message the server-provided completion detail
     * @param results completed Step outputs returned by Dex
     * @param decoder the SDK decoder used by {@link #getResult(int, Class)}
     */
    public FlowUncompletedException(
            final String runId,
            final FlowStatus status,
            final FlowErrorType errorType,
            final String message,
            final List<StepCompletionOutput> results,
            final BiFunction<Value, Class<?>, Object> decoder) {
        super(message);
        this.runId = runId;
        this.status = status;
        this.errorType = errorType;
        this.results = Collections.unmodifiableList(
                new ArrayList<StepCompletionOutput>(results));
        this.decoder = decoder;
    }

    /**
     * Returns the run ID that ended abnormally.
     *
     * @return the run ID
     */
    public String getRunId() {
        return runId;
    }

    /**
     * Returns the abnormal terminal status.
     *
     * @return the Flow status
     */
    public FlowStatus getStatus() {
        return status;
    }

    /**
     * Returns the durable failure category.
     *
     * @return the Flow error type, or {@code null} when Dex did not supply one
     */
    public FlowErrorType getErrorType() {
        return errorType;
    }

    /**
     * Returns the number of completed Step outputs carried by this exception.
     *
     * @return the result count
     */
    public int getResultCount() {
        return results.size();
    }

    /**
     * Decodes one completed Step output.
     *
     * @param index the zero-based result index
     * @param resultType the concrete Java class used for decoding
     * @param <T> the requested result type
     * @return the decoded result
     * @throws IndexOutOfBoundsException if {@code index} is outside the available results
     */
    public <T> T getResult(final int index, final Class<T> resultType) {
        return resultType.cast(decoder.apply(
                results.get(index).getCompletedStepOutput(),
                resultType));
    }
}
