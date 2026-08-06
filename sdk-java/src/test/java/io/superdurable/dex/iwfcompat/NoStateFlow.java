/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Super Durable Source License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Super-Durable-1.0
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
