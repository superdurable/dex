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

package io.superdurable.dex.patterns.childflows;

import io.superdurable.dex.Client;
import io.superdurable.dex.FlowInfo;
import io.superdurable.dex.StartFlowOptions;
import io.superdurable.dex.StopFlowOptions;
import io.superdurable.dex.StopType;
import io.superdurable.dex.exceptions.FlowNotFoundException;
import io.superdurable.dex.patterns.childflows.OrderFulfillmentFlow.Order;
import org.springframework.http.HttpStatus;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RestController;

import java.time.Duration;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;

/** Serves the order routes and the shipment routes that find a shipment by its number. */
@RestController
@RequestMapping("/patterns/child-flows-business-identity")
public final class ChildFlowsController {
    private static final Duration ORDER_TIMEOUT = Duration.ofDays(40);

    private final Client client;
    private final OrderFulfillmentFlow orderFlow;

    public ChildFlowsController(final Client client, final OrderFulfillmentFlow orderFlow) {
        this.client = client;
        this.orderFlow = orderFlow;
    }

    /** Starts an order with two shipments: one delivers in seconds, one takes a day. */
    @GetMapping("/start")
    ResponseEntity<String> startOrder(@RequestParam final String workflowId) {
        final Order order = new Order(
                List.of(
                        new Shipment(workflowId + "-1", "Seattle", 5),
                        new Shipment(workflowId + "-2", "Portland", 86400)),
                60);
        return ResponseEntity.ok(client.startFlow(
                orderFlow,
                workflowId,
                order,
                StartFlowOptions.newBuilder().timeout(ORDER_TIMEOUT).build()));
    }

    /** Looks a shipment up by its shipment number, which is its Flow ID. */
    @GetMapping("/shipment")
    ResponseEntity<Map<String, String>> getShipment(@RequestParam final String shipmentNumber) {
        final FlowInfo info;
        try {
            info = client.describeFlow(shipmentNumber);
        } catch (final FlowNotFoundException notFound) {
            return ResponseEntity.status(HttpStatus.NOT_FOUND)
                    .body(Map.of("error", "shipment " + shipmentNumber + " not found"));
        }
        final String outcome = client.getAttributes(shipmentNumber, ShipmentFlow.shipmentOutcome)
                .get(ShipmentFlow.shipmentOutcome)
                .orElse("");
        final Map<String, String> response = new LinkedHashMap<String, String>();
        response.put("shipmentNumber", shipmentNumber);
        response.put("status", OrderFulfillmentFlow.statusName(info.getStatus()));
        response.put("outcome", outcome);
        return ResponseEntity.ok(response);
    }

    /** Stops a shipment by its number. Its order records it at the next reconcile read. */
    @GetMapping("/cancel-shipment")
    ResponseEntity<String> cancelShipment(@RequestParam final String shipmentNumber) {
        client.stopFlow(shipmentNumber, new StopFlowOptions(StopType.CANCEL, "shipment canceled"));
        return ResponseEntity.ok("canceled");
    }
}
