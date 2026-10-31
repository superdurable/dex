/*
 * Copyright (c) 2022-2026 Super Durable, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package io.superdurable.dex.integ.smoke;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import io.superdurable.dex.FlowStatus;
import io.superdurable.dex.primitives.attribute.AttributeFlow;
import io.superdurable.dex.shared.ExampleFlows;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;

import java.net.http.HttpResponse;
import java.time.Duration;
import java.util.ArrayList;
import java.util.List;
import java.util.Map;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

@ExtendWith(SharedFlowSmokeExtension.class)
public class AttributeReadTest {
    private static final ObjectMapper OBJECT_MAPPER = new ObjectMapper();
    private static final String READ_PATH = "/primitives/attribute/read";

    @Test
    void attributeReadReturnsCommittedValuesOfClosedRun() throws Exception {
        final FlowSmokeEnvironment environment = SharedFlowSmokeExtension.environment();
        final String flowId = environment.newFlowId("primitive-attribute-read");
        environment.client().startFlow(
                environment.bean(AttributeFlow.class),
                flowId,
                "read",
                ExampleFlows.startOptions());
        assertEquals(
                FlowStatus.COMPLETED,
                environment.client().waitForFlow(flowId, Duration.ofSeconds(30)).getStatus());

        final HttpResponse<String> response =
                environment.get(READ_PATH, Map.of("workflowId", flowId));
        assertEquals(200, response.statusCode(), response.body());
        assertEquals(
                OBJECT_MAPPER.readTree(
                        "{\"status\":\"completed\",\"paymentProgress\":\"authorized\","
                                + "\"email\":null}"),
                OBJECT_MAPPER.readTree(response.body()));
    }

    @Test
    void attributeReadReturnsNotFoundForMissingFlow() throws Exception {
        final FlowSmokeEnvironment environment = SharedFlowSmokeExtension.environment();
        final String flowId = environment.newFlowId("primitive-attribute-read-missing");
        final HttpResponse<String> response =
                environment.get(READ_PATH, Map.of("workflowId", flowId));
        assertEquals(404, response.statusCode(), response.body());
        final JsonNode errorBody = OBJECT_MAPPER.readTree(response.body());
        final List<String> fieldNames = new ArrayList<String>();
        errorBody.fieldNames().forEachRemaining(fieldNames::add);
        assertEquals(List.of("error"), fieldNames, response.body());
        assertTrue(errorBody.get("error").asText().contains(flowId), response.body());
    }

    @Test
    void attributeReadRequiresWorkflowId() throws Exception {
        final FlowSmokeEnvironment environment = SharedFlowSmokeExtension.environment();
        final HttpResponse<String> response = environment.get(READ_PATH, Map.of());
        assertEquals(400, response.statusCode(), response.body());
        assertEquals(
                OBJECT_MAPPER.readTree("{\"error\":\"workflowId is required\"}"),
                OBJECT_MAPPER.readTree(response.body()));
    }
}
