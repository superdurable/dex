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

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNull;

import com.fasterxml.jackson.databind.ObjectMapper;
import io.superdurable.gen.ChannelInfo;
import io.superdurable.gen.ConditionResults;
import io.superdurable.gen.KV;
import java.util.Collections;
import org.junit.jupiter.api.Test;

final class SubFlowErrorTypeMappingTest {
    @Test
    void subFlowResultsMapAbandonedAndUnrecognizedErrorTypes() {
        final Flow<Void> flow = new Flow<Void>() {
            @Override
            public String getFlowType() {
                return "ParentFlow";
            }

            @Override
            public StepList<Void> getSteps() {
                return StepList.empty();
            }
        };
        final Registry registry = new Registry(Collections.<Flow<?>>singletonList(flow));
        final ConditionResults conditionResults = ConditionResults.newBuilder()
                .addSubFlowResults(io.superdurable.gen.FlowResult.newBuilder()
                        .setFlowStatus(io.superdurable.gen.FlowStatus.FLOW_STATUS_FAILED)
                        .setErrorType(
                                io.superdurable.gen.FlowErrorType.FLOW_ERROR_TYPE_SUB_FLOW_ABANDONED)
                        .setErrorMessage("abandoned by a Time Travel"))
                .addSubFlowResults(io.superdurable.gen.FlowResult.newBuilder()
                        .setFlowStatus(io.superdurable.gen.FlowStatus.FLOW_STATUS_FAILED)
                        .setErrorTypeValue(99))
                .build();
        final InvocationContext context = new InvocationContext(
                InvocationContext.Method.EXECUTE,
                registry.getFlow("ParentFlow"),
                io.superdurable.gen.Context.getDefaultInstance(),
                new ValueMapper(new ObjectMapper()),
                null,
                Collections.<KV>emptyList(),
                Collections.<KV>emptyList(),
                conditionResults,
                Collections.<String, ChannelInfo>emptyMap());

        final FlowResult abandoned = SubFlow.getConditionResults(context, 0);
        assertEquals(FlowStatus.FAILED, abandoned.getStatus());
        assertEquals(FlowErrorType.SUB_FLOW_ABANDONED, abandoned.getErrorType());
        assertEquals("abandoned by a Time Travel", abandoned.getErrorMessage());

        final FlowResult unrecognized = SubFlow.getConditionResults(context, 1);
        assertEquals(FlowStatus.FAILED, unrecognized.getStatus());
        assertNull(unrecognized.getErrorType());
    }
}
