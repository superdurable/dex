/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Sustainable Use License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0
 */

package io.superdurable.dex;

/**
 * Defines a typed best-effort resumable message Stream owned by one Flow type.
 *
 * <p>Register the exact definition in one {@link PersistenceSchema}. All instances of that Flow
 * type share the approximate byte budget. Step writes are immediate and do not roll back when the
 * handler later fails. A disabled, full, unavailable, or rejecting Stream Store can drop
 * messages without failing the Step. Use Attributes, an RPC, or the Flow result as authoritative
 * state when a consumer must recover from lost progress messages.
 *
 * <p>Step messages have source {@code #StepExecutionID}, shared by every attempt of that
 * Step execution. A source is informational and does not deduplicate writes. With Server
 * protocol 4 (server-zd v0.1.17, required by SDK 1.35.0), ASYNC can rerun the same attempt number
 * after a server failure and repeat messages in that source.
 *
 * <pre>{@code
 * private final Stream<String> progress = Stream.define("progress", String.class, 1048576L);
 *
 * public PersistenceSchema getPersistenceSchema() {
 *     return PersistenceSchema.of(progress);
 * }
 *
 * public StepDecision execute(Context context, String input) {
 *     progress.write(context, "Processing " + input);
 *     return StepDecision.gracefulComplete(input);
 * }
 * }</pre>
 *
 * @param <T> the message type
 */
public final class Stream<T> extends PersistenceDefinition {
    private final String name;
    private final Class<T> valueType;
    private final long streamCapacityBytes;

    private Stream(
            final String name,
            final Class<T> valueType,
            final long streamCapacityBytes) {
        this.name = Attribute.requireName(name);
        if (valueType == null) {
            throw new IllegalArgumentException("Stream value type is required");
        }
        if (streamCapacityBytes <= 0) {
            throw new IllegalArgumentException("Stream streamCapacityBytes must be positive");
        }
        this.valueType = valueType;
        this.streamCapacityBytes = streamCapacityBytes;
    }

    /**
     * Creates a typed Stream definition.
     *
     * @param name the nonblank logical name unique within its Flow
     * @param valueType the concrete message class used for decoding
     * @param streamCapacityBytes the positive approximate shared byte budget
     * @param <T> the message type
     * @return an immutable Stream definition
     * @throws IllegalArgumentException if the name is invalid, the class is null, or capacity is
     *     not positive
     */
    public static <T> Stream<T> define(
            final String name,
            final Class<T> valueType,
            final long streamCapacityBytes) {
        return new Stream<T>(name, valueType, streamCapacityBytes);
    }

    /**
     * Appends one message immediately from the current Step execution.
     *
     * <p>This method sends a fire-and-forget frame to Dex. It may be called repeatedly for the same
     * Stream. Each frame also acts as an implicit Step heartbeat. Storage failures do not fail
     * the Step, but local validation, serialization, or transport failures may throw.
     * A write preserves the last explicit heartbeat checkpoint, including a cleared checkpoint.
     * Use {@link BufferedTextStream} for small text deltas instead of writing each token
     * separately.
     *
     * @param context the current Step Context; RPC and Flow timeout Contexts are rejected
     * @param value the typed message to append
     * @throws IllegalStateException if the Context belongs to an RPC or Flow timeout handler
     * @throws io.grpc.StatusRuntimeException if the Step invocation is canceled or transport fails
     * @throws io.superdurable.dex.exceptions.ValueMappingException if the value cannot be encoded
     */
    public void write(final Context context, final T value) {
        context.writeStream(this, value);
    }

    /**
     * Returns the logical Stream name.
     *
     * @return the stable protocol name
     */
    public String getStreamName() {
        return name;
    }

    /**
     * Returns the approximate shared byte budget.
     *
     * @return the positive budget in bytes
     */
    public long getStreamCapacityBytes() {
        return streamCapacityBytes;
    }

    @Override
    String getName() {
        return name;
    }

    @Override
    Class<?> getValueType() {
        return valueType;
    }
}
