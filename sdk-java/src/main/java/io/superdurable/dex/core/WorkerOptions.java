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

package io.superdurable.dex.core;

import org.immutables.value.Value;

@Value.Immutable
public abstract class WorkerOptions {

    public abstract ObjectEncoder getObjectEncoder();

    // use this when running with docker-compose of Dex server
    public static final WorkerOptions defaultOptions = minimum(new JacksonJsonObjectEncoder());

    public static WorkerOptions minimum(final ObjectEncoder objectEncoder) {
        return ImmutableWorkerOptions.builder()
                .objectEncoder(objectEncoder)
                .build();
    }

    public static ImmutableWorkerOptions.Builder builder() {
        return ImmutableWorkerOptions.builder();
    }
}
