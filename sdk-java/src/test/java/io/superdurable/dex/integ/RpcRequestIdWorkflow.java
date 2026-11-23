/*
 * Portions of this file are derived from indeedeng/iwf-java-sdk.
 * Those portions are licensed under the Apache License, Version 2.0.
 * See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
 *
 * Modifications Copyright (c) 2026 Super Durable, Inc.
 *
 * Modifications are licensed under the Sustainable Use License 1.0.
 * Third-Party Materials remain under the Apache License, Version 2.0.
 * See LICENSE and LEGACY_NOTICES.md.
 */

package io.superdurable.dex.integ;

import io.superdurable.dex.Attribute;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.RPC;
import io.superdurable.dex.RPCResult;

import java.util.concurrent.atomic.AtomicInteger;

class RpcRequestIdWorkflow implements Flow<Void> {
    private final Attribute<Integer> counterTotal = Attribute.define("counter-total", Integer.class);
    private final Attribute<Boolean> eventProcessed = Attribute.define("event-processed", Boolean.class);
    private final AtomicInteger incrementHandlerCalls = new AtomicInteger();

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(counterTotal, eventProcessed);
    }

    @RPC(isTransactional = true)
    public RPCResult<Integer> incrementTransactionally(final Context context, final Integer amount) {
        return incrementCounter(context, amount);
    }

    @RPC
    public RPCResult<Integer> incrementNonTransactionally(final Context context, final Integer amount) {
        return incrementCounter(context, amount);
    }

    @RPC
    public RPCResult<Integer> getCounterTotal(final Context context) {
        return RPCResult.of(counterTotal.get(context));
    }

    @RPC(isTransactional = true)
    public RPCResult<String> markProcessedTransactionally(final Context context) {
        return markEventProcessed(context);
    }

    @RPC
    public RPCResult<String> markProcessedNonTransactionally(final Context context) {
        return markEventProcessed(context);
    }

    int getIncrementHandlerCalls() {
        return incrementHandlerCalls.get();
    }

    private RPCResult<Integer> incrementCounter(final Context context, final int amount) {
        incrementHandlerCalls.incrementAndGet();
        final Integer currentTotal = counterTotal.get(context);
        final int nextTotal = (currentTotal == null ? 0 : currentTotal) + amount;
        counterTotal.set(context, nextTotal);
        return RPCResult.of(nextTotal);
    }

    private RPCResult<String> markEventProcessed(final Context context) {
        if (Boolean.TRUE.equals(eventProcessed.get(context))) {
            return RPCResult.of("alreadyProcessed");
        }
        eventProcessed.set(context, true);
        return RPCResult.of("accepted");
    }
}
