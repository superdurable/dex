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

import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.RPC;
import io.superdurable.dex.RPCAttributeMapLock;
import io.superdurable.dex.RPCResult;

import static io.superdurable.dex.integ.AttributeLockRequirementStepWorkflow.EVENTS;
import static io.superdurable.dex.integ.AttributeLockRequirementStepWorkflow.FREE;
import static io.superdurable.dex.integ.AttributeLockRequirementStepWorkflow.ITEMS;
import static io.superdurable.dex.integ.AttributeLockRequirementStepWorkflow.REPLIES;
import static io.superdurable.dex.integ.AttributeLockRequirementStepWorkflow.TOTAL;

/** Has no Steps, so it stays running for the RPC cases. */
class AttributeLockRequirementRpcWorkflow implements Flow<Void> {
    @Override
    public PersistenceSchema getPersistenceSchema() {
        return AttributeLockRequirementStepWorkflow.lockRequirementSchema();
    }

    /** Also writes an undeclared Attribute, which the failure must discard too. */
    @RPC
    public void publishReplyUnlocked(final Context context, final String reply) {
        FREE.set(context, 1);
        REPLIES.publish(context, reply);
    }

    @RPC(lockAttributes = {"lock-req-coordination"})
    public void publishReplyLocked(final Context context, final String reply) {
        REPLIES.publish(context, reply);
    }

    @RPC
    public RPCResult<Integer> countReplies(final Context context) {
        return RPCResult.of(REPLIES.size(context));
    }

    @RPC
    public void setItem(final Context context, final String instance) {
        ITEMS.set(context, instance, 1);
    }

    @RPC(lockAttributeMaps = {
        @RPCAttributeMapLock(attribute = "lock-req-items", instance = "item a")
    })
    public void setItemLocked(final Context context, final String instance) {
        ITEMS.set(context, instance, 1);
    }

    /** Also writes an undeclared Attribute, which the failure must discard too. */
    @RPC
    public void publishEventUnlocked(final Context context, final String instance) {
        FREE.set(context, 1);
        EVENTS.publish(context, instance, "event");
    }

    @RPC
    public RPCResult<Integer> countEvents(final Context context, final String instance) {
        return RPCResult.of(EVENTS.size(context, instance));
    }

    @RPC(lockAttributes = {"lock-req-total"})
    public void setTotalLocked(final Context context, final Integer value) {
        TOTAL.set(context, value);
    }

    @RPC
    public void deleteTotalUnlocked(final Context context) {
        TOTAL.delete(context);
    }
}
