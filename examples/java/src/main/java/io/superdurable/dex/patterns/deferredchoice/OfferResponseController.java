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

package io.superdurable.dex.patterns.deferredchoice;

import io.superdurable.dex.Client;
import io.superdurable.dex.FlowStatus;
import io.superdurable.dex.StartFlowOptions;
import io.superdurable.dex.exceptions.DexServiceException;
import io.superdurable.dex.exceptions.FlowClosingException;
import io.superdurable.dex.exceptions.FlowNotActiveOrNotFoundException;
import io.superdurable.dex.exceptions.FlowNotFoundException;
import io.superdurable.dex.exceptions.RpcLockConflictException;
import org.springframework.http.HttpStatus;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RestController;

import java.util.function.Supplier;

@RestController
@RequestMapping("/patterns/deferred-choice")
public class OfferResponseController {
    private static final int RESPONSE_LOCK_CONFLICT_ATTEMPTS = 5;
    private static final long RESPONSE_LOCK_CONFLICT_BACKOFF_MILLIS = 200;
    // A route-only outcome; the RPC never returns it.
    private static final String OFFER_RESPONSE_FLOW_NOT_FOUND = "flow not found";

    private final Client client;
    private final OfferResponseFlow offerResponseFlow;

    public OfferResponseController(final Client client, final OfferResponseFlow offerResponseFlow) {
        this.client = client;
        this.offerResponseFlow = offerResponseFlow;
    }

    @GetMapping("/start")
    ResponseEntity<String> startOfferResponse(
            @RequestParam final String workflowId,
            @RequestParam final String offerId) {
        final String runId = client.startFlow(
                offerResponseFlow,
                workflowId,
                offerId,
                StartFlowOptions.newBuilder().build());
        return ResponseEntity.ok(runId);
    }

    @GetMapping("/accept")
    ResponseEntity<String> acceptCustomerOffer(
            @RequestParam final String workflowId,
            @RequestParam final String note) {
        final OfferResponseFlow stub = client.newRpcStub(OfferResponseFlow.class, workflowId);
        return respond(invokeResponseRpc(
                workflowId, () -> client.invokeRPC(stub::acceptCustomerOffer, note)));
    }

    @GetMapping("/reject")
    ResponseEntity<String> rejectCustomerOffer(
            @RequestParam final String workflowId,
            @RequestParam final String note) {
        final OfferResponseFlow stub = client.newRpcStub(OfferResponseFlow.class, workflowId);
        return respond(invokeResponseRpc(
                workflowId, () -> client.invokeRPC(stub::rejectCustomerOffer, note)));
    }

    /** Answers 409 when the response window is closed and 404 when the offer never existed. */
    private static ResponseEntity<String> respond(final String outcome) {
        if (OfferResponseFlow.OFFER_RESPONSE_WINDOW_CLOSED.equals(outcome)) {
            return ResponseEntity.status(HttpStatus.CONFLICT).body("offer response window closed");
        }
        if (OFFER_RESPONSE_FLOW_NOT_FOUND.equals(outcome)) {
            return ResponseEntity.status(HttpStatus.NOT_FOUND).body("offer response flow not found");
        }
        return ResponseEntity.ok("offer response recorded");
    }

    /**
     * Retries a lock conflict with bounded backoff. Retrying is safe: a conflicting call
     * committed nothing.
     *
     * @return the RPC outcome; OFFER_RESPONSE_WINDOW_CLOSED when the Flow is closing or closed;
     *     OFFER_RESPONSE_FLOW_NOT_FOUND when the Flow never existed
     * @throws RpcLockConflictException when every attempt conflicts
     * @throws DexServiceException when the call fails otherwise
     */
    private String invokeResponseRpc(final String workflowId, final Supplier<String> invocation) {
        for (int attempt = 1; ; attempt++) {
            try {
                return invocation.get();
            } catch (final RpcLockConflictException conflict) {
                if (attempt == RESPONSE_LOCK_CONFLICT_ATTEMPTS) {
                    throw conflict;
                }
                sleepBeforeRetry(attempt * RESPONSE_LOCK_CONFLICT_BACKOFF_MILLIS);
            } catch (final FlowClosingException closing) {
                // DescribeFlow may still report RUNNING while the Flow closes.
                return OfferResponseFlow.OFFER_RESPONSE_WINDOW_CLOSED;
            } catch (final FlowNotActiveOrNotFoundException | FlowNotFoundException terminal) {
                return describeTerminalFlow(workflowId, terminal);
            }
        }
    }

    /**
     * Tells a closed Flow, whose window is closed too, from one that never existed.
     *
     * @return OFFER_RESPONSE_WINDOW_CLOSED for a closed Flow, or OFFER_RESPONSE_FLOW_NOT_FOUND
     * @throws DexServiceException the original error when the Flow is still running
     */
    private String describeTerminalFlow(final String workflowId, final DexServiceException terminal) {
        final FlowStatus status;
        try {
            status = client.describeFlow(workflowId).getStatus();
        } catch (final FlowNotFoundException | FlowNotActiveOrNotFoundException notFound) {
            return OFFER_RESPONSE_FLOW_NOT_FOUND;
        }
        if (status != FlowStatus.RUNNING) {
            return OfferResponseFlow.OFFER_RESPONSE_WINDOW_CLOSED;
        }
        throw terminal;
    }

    private static void sleepBeforeRetry(final long millis) {
        try {
            Thread.sleep(millis);
        } catch (final InterruptedException interrupted) {
            Thread.currentThread().interrupt();
            throw new IllegalStateException("interrupted while retrying a lock conflict", interrupted);
        }
    }
}
