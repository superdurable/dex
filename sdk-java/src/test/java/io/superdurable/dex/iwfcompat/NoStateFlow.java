/*
 * Portions of this file are derived from indeedeng/iwf-java-sdk.
 * Those portions are licensed under the Apache License, Version 2.0.
 * See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
 *
 * Modifications Copyright (c) 2026 Super Durable, Inc.
 *
 * Modifications are licensed under the Super Durable Source License 1.0.
 * Third-Party Materials remain under the Apache License, Version 2.0.
 * See LICENSE and LEGACY_NOTICES.md.
 */

package io.superdurable.dex.iwfcompat;

import io.superdurable.dex.Attribute;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.RPC;
import io.superdurable.dex.RPCResult;

final class NoStateFlow implements Flow<Void> {
    private final Attribute<Integer> counter = Attribute.define("counter", Integer.class);

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(counter);
    }

    @RPC(lockAttributes = {"counter"})
    public RPCResult<Integer> increaseCounter(final Context context) {
        final int next = counter.get(context) + 1;
        counter.set(context, next);
        return RPCResult.of(next);
    }

    @RPC
    public RPCResult<Integer> getCounter(final Context context) {
        return RPCResult.of(counter.get(context));
    }

    @RPC
    public RPCResult<Long> fail(final Context context, final String input) {
        throw new IllegalArgumentException(input);
    }
}
