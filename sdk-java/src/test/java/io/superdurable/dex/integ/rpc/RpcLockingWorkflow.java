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

package io.superdurable.dex.integ.rpc;

import io.superdurable.dex.core.Context;
import io.superdurable.dex.core.ObjectWorkflow;
import io.superdurable.dex.core.RPC;
import io.superdurable.dex.core.StateDef;
import io.superdurable.dex.core.StateMovement;
import io.superdurable.dex.core.communication.Communication;
import io.superdurable.dex.core.communication.CommunicationMethodDef;
import io.superdurable.dex.core.communication.InternalChannelDef;
import io.superdurable.dex.core.persistence.DataAttributeDef;
import io.superdurable.dex.core.persistence.Persistence;
import io.superdurable.dex.core.persistence.PersistenceFieldDef;
import io.superdurable.dex.core.persistence.SearchAttributeDef;
import io.superdurable.dex.gen.models.PersistenceLoadingType;
import io.superdurable.dex.gen.models.SearchAttributeValueType;
import org.springframework.stereotype.Component;

import java.util.Arrays;
import java.util.List;

import static io.superdurable.dex.integ.RpcTest.HARDCODED_STR;
import static io.superdurable.dex.integ.RpcTest.RPC_OUTPUT;

@Component
public class RpcLockingWorkflow implements ObjectWorkflow {

    public static final String RPC_INTERNAL_CHANNEL_NAME = "rpc-channel-1";
    public static final String TEST_DATA_OBJECT_KEY = "data-obj-1";
    public static final String TEST_SEARCH_ATTRIBUTE_KEYWORD = "CustomKeywordField";
    public static final String TEST_SEARCH_ATTRIBUTE_INT = "CustomIntField";

    @Override
    public List<CommunicationMethodDef> getCommunicationSchema() {
        return Arrays.asList(
                InternalChannelDef.create(Void.class, RPC_INTERNAL_CHANNEL_NAME)
        );
    }

    @Override
    public List<StateDef> getWorkflowStates() {
        return Arrays.asList(
                StateDef.startingState(new RpcLockingWorkflowState1()),
                StateDef.nonStartingState(new RpcLockingWorkflowState2())
        );
    }

    @Override
    public List<PersistenceFieldDef> getPersistenceSchema() {
        return Arrays.asList(
                DataAttributeDef.create(String.class, TEST_DATA_OBJECT_KEY),
                SearchAttributeDef.create(SearchAttributeValueType.INT, TEST_SEARCH_ATTRIBUTE_INT),
                SearchAttributeDef.create(SearchAttributeValueType.KEYWORD, TEST_SEARCH_ATTRIBUTE_KEYWORD)
        );
    }

    @RPC(
            dataAttributesLoadingType = PersistenceLoadingType.PARTIAL_WITH_EXCLUSIVE_LOCK,
            dataAttributesPartialLoadingKeys = {TEST_DATA_OBJECT_KEY}
    )
    public void testRpcWithLocking(Context context, Persistence persistence, Communication communication) {
        if (context.getWorkflowId().isEmpty() || context.getWorkflowRunId().isEmpty()) {
            throw new RuntimeException("invalid context");
        }
        persistence.setDataAttribute(TEST_DATA_OBJECT_KEY, HARDCODED_STR);
        persistence.setSearchAttributeKeyword(TEST_SEARCH_ATTRIBUTE_KEYWORD, HARDCODED_STR);
        persistence.setSearchAttributeInt64(TEST_SEARCH_ATTRIBUTE_INT, RPC_OUTPUT);
        communication.publishInternalChannel(RPC_INTERNAL_CHANNEL_NAME, null);
        communication.triggerStateMovements(StateMovement.create(RpcLockingWorkflowState2.class));
    }

    @RPC
    public void testRpcWithoutLocking(Context context, Persistence persistence, Communication communication) {
        if (context.getWorkflowId().isEmpty() || context.getWorkflowRunId().isEmpty()) {
            throw new RuntimeException("invalid context");
        }
        persistence.setDataAttribute(TEST_DATA_OBJECT_KEY, HARDCODED_STR);
        persistence.setSearchAttributeKeyword(TEST_SEARCH_ATTRIBUTE_KEYWORD, HARDCODED_STR);
        persistence.setSearchAttributeInt64(TEST_SEARCH_ATTRIBUTE_INT, RPC_OUTPUT);
        communication.publishInternalChannel(RPC_INTERNAL_CHANNEL_NAME, null);
        communication.triggerStateMovements(StateMovement.create(RpcLockingWorkflowState2.class));
    }
}
