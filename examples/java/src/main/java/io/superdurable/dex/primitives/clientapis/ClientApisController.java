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

package io.superdurable.dex.primitives.clientapis;

import io.superdurable.dex.Client;
import io.superdurable.dex.SearchFlowEntry;
import io.superdurable.dex.SearchFlowsPage;
import io.superdurable.dex.shared.ExampleFlows;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RestController;

import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;

@RestController
@RequestMapping("/primitives/client-apis")
public final class ClientApisController {
    private final Client client;
    private final ClientApisFlow flow;

    public ClientApisController(final Client client, final ClientApisFlow flow) {
        this.client = client;
        this.flow = flow;
    }

    @GetMapping("/start")
    public ResponseEntity<Map<String, String>> start(
            @RequestParam final String workflowId,
            @RequestParam final String keyword) {
        final String runId =
                client.startFlow(flow, workflowId, keyword, ExampleFlows.startOptions());
        final Map<String, String> response = new LinkedHashMap<String, String>();
        response.put("flowID", workflowId);
        response.put("runID", runId);
        return ResponseEntity.ok(response);
    }

    @GetMapping("/search")
    public ResponseEntity<Map<String, Object>> search(@RequestParam final String query) {
        final SearchFlowsPage page = client.searchFlows(query, 20, "");
        final List<String> flowIds = new ArrayList<String>();
        for (final SearchFlowEntry entry : page.getFlows()) {
            flowIds.add(entry.getFlowId());
        }
        final Map<String, Object> response = new LinkedHashMap<String, Object>();
        response.put("flowIDs", flowIds);
        response.put("nextPageToken", page.getNextPageToken());
        return ResponseEntity.ok(response);
    }
}
