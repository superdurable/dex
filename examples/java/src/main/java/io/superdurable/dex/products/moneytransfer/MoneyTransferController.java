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

package io.superdurable.dex.products.moneytransfer;

import io.superdurable.dex.Client;
import io.superdurable.dex.shared.ExampleFlows;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RestController;

import java.util.LinkedHashMap;
import java.util.Map;

@RestController
@RequestMapping("/products/money-transfer")
public class MoneyTransferController {
    private final Client client;
    private final MoneyTransferFlow flow;

    public MoneyTransferController(
            final Client client,
            final MoneyTransferFlow flow) {
        this.client = client;
        this.flow = flow;
    }

    @GetMapping("/start")
    public ResponseEntity<Map<String, String>> start(
            @RequestParam final String fromAccount,
            @RequestParam final String toAccount,
            @RequestParam final int amount,
            @RequestParam(defaultValue = "") final String notes) {
        final String flowId = "money-transfer-" + System.nanoTime();
        final TransferRequest request = new TransferRequest(
                fromAccount,
                toAccount,
                amount,
                notes);
        final String runId = client.startFlow(flow, flowId, request, ExampleFlows.startOptions());
        final Map<String, String> response = new LinkedHashMap<String, String>();
        response.put("flowID", flowId);
        response.put("runID", runId);
        return ResponseEntity.ok(response);
    }
}
