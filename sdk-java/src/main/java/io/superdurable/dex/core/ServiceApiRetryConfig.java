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
public abstract class ServiceApiRetryConfig {

    public abstract long getInitialIntervalMills();

    public abstract long getMaximumIntervalMills();

    public abstract int getMaximumAttempts();
}
