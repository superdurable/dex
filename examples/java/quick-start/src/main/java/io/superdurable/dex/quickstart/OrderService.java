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
package io.superdurable.dex.quickstart;

import com.sun.net.httpserver.HttpExchange;
import com.sun.net.httpserver.HttpServer;
import io.superdurable.dex.Client;
import io.superdurable.dex.StartFlowOptions;
import io.superdurable.dex.StepExecutionId;
import io.superdurable.dex.WaitForStepCompletionOptions;
import io.superdurable.dex.exceptions.FlowAlreadyStartedException;

import java.io.IOException;
import java.io.OutputStream;
import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;
import java.time.Duration;
import java.util.regex.Matcher;
import java.util.regex.Pattern;

/** Starts orders over HTTP: POST /orders with a JSON body such as {"orderId": "order-1"}. */
public final class OrderService {
    private static final Pattern ORDER_ID = Pattern.compile("\"orderId\"\\s*:\\s*\"([^\"]+)\"");

    private OrderService() {
    }

    /** Starts the order's Flow once, however often the request is retried. Returns after Charge. */
    public static void startOrder(final Client client, final OrderFlow orderFlow, final String orderId) {
        final StartFlowOptions options = StartFlowOptions.newBuilder()
                .requestId("start-" + orderId)
                .ignoreAlreadyStarted(true)
                .build();
        client.startFlow(orderFlow, orderId, orderId, options);
        client.waitForStepCompletion(
                orderId,
                StepExecutionId.of("Charge"),
                WaitForStepCompletionOptions.newBuilder().requestTimeout(Duration.ofSeconds(30)).build());
    }

    /** Creates an HTTP server for POST /orders on the given port; 0 picks a free port. */
    public static HttpServer createServer(final Client client, final OrderFlow orderFlow, final int port)
            throws IOException {
        final HttpServer server = HttpServer.create(new InetSocketAddress("127.0.0.1", port), 0);
        server.createContext("/orders", exchange -> handleOrder(client, orderFlow, exchange));
        return server;
    }

    private static void handleOrder(final Client client, final OrderFlow orderFlow,
            final HttpExchange exchange) throws IOException {
        if (!"POST".equals(exchange.getRequestMethod())) {
            reply(exchange, 405, "{\"error\": \"use POST\"}");
            return;
        }
        final String body = new String(exchange.getRequestBody().readAllBytes(), StandardCharsets.UTF_8);
        final Matcher match = ORDER_ID.matcher(body);
        if (!match.find()) {
            reply(exchange, 400, "{\"error\": \"orderId is required\"}");
            return;
        }
        final String orderId = match.group(1);
        try {
            startOrder(client, orderFlow, orderId);
        } catch (final FlowAlreadyStartedException alreadyStarted) {
            reply(exchange, 409, "{\"error\": \"order " + orderId + " was already started\"}");
            return;
        }
        reply(exchange, 200, "{\"flowId\": \"" + orderId + "\", \"charged\": true}");
    }

    private static void reply(final HttpExchange exchange, final int status, final String json)
            throws IOException {
        final byte[] data = json.getBytes(StandardCharsets.UTF_8);
        exchange.getResponseHeaders().set("Content-Type", "application/json");
        exchange.sendResponseHeaders(status, data.length);
        try (OutputStream output = exchange.getResponseBody()) {
            output.write(data);
        }
    }
}
