/*
 * Copyright (c) 2026 Super Durable, Inc.
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

package io.superdurable.dex.primitives.attribute;

import com.fasterxml.jackson.annotation.JsonInclude;
import io.superdurable.dex.AttributeMapInstance;
import io.superdurable.dex.AttributeValues;
import io.superdurable.dex.Client;
import io.superdurable.dex.exceptions.FlowNotFoundException;
import io.superdurable.dex.shared.ExampleFlows;
import org.springframework.http.HttpStatus;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RestController;

import java.util.LinkedHashMap;
import java.util.Map;

@RestController
@RequestMapping("/primitives/attribute")
public final class AttributeController {
    private final Client client;
    private final AttributeFlow flow;

    public AttributeController(final Client client, final AttributeFlow flow) {
        this.client = client;
        this.flow = flow;
    }

    @GetMapping("/start")
    public ResponseEntity<Map<String, String>> start(
            @RequestParam final String workflowId,
            @RequestParam final String message) {
        final String runId =
                client.startFlow(flow, workflowId, message, ExampleFlows.startOptions());
        final Map<String, String> response = new LinkedHashMap<String, String>();
        response.put("flowID", workflowId);
        response.put("runID", runId);
        return ResponseEntity.ok(response);
    }

    @GetMapping("/read")
    public ResponseEntity<?> readAttributes(
            @RequestParam(required = false) final String workflowId) {
        if (workflowId == null || workflowId.isEmpty()) {
            return ResponseEntity.badRequest().body(Map.of("error", "workflowId is required"));
        }
        final AttributeMapInstance<String> paymentProgress = flow.progress.instance("payment");
        final AttributeValues values;
        try {
            values = client.getAttributes(workflowId, flow.status, paymentProgress, flow.email);
        } catch (final FlowNotFoundException notFound) {
            return ResponseEntity.status(HttpStatus.NOT_FOUND)
                    .body(Map.of("error", notFound.getMessage()));
        }
        return ResponseEntity.ok(new AttributeReadResponse(
                values.get(flow.status).orElse("not started"),
                values.get(paymentProgress).orElse("not started"),
                // This Flow never sets email, so get returns Optional.empty().
                values.get(flow.email).orElse(null)));
    }

    // Keep email as JSON null, overriding the application's NON_NULL default.
    @JsonInclude(JsonInclude.Include.ALWAYS)
    record AttributeReadResponse(String status, String paymentProgress, String email) {
    }
}
