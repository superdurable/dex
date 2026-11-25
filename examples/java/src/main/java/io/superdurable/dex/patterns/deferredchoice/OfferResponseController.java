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

    /** Answers 409 when the response window is already closed. */
    private static ResponseEntity<String> respond(final String outcome) {
        if (OfferResponseFlow.OFFER_RESPONSE_WINDOW_CLOSED.equals(outcome)) {
            return ResponseEntity.status(HttpStatus.CONFLICT).body("offer response window closed");
        }
        return ResponseEntity.ok("offer response recorded");
    }

    /**
     * Retries a lock conflict with bounded backoff. Retrying is safe: a conflicting call
     * committed nothing.
     *
     * @return the RPC outcome, or OFFER_RESPONSE_WINDOW_CLOSED when the Flow already closed
     * @throws RpcLockConflictException when every attempt conflicts
     * @throws DexServiceException when the Flow never existed or the call fails otherwise
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
            } catch (final FlowNotActiveOrNotFoundException | FlowNotFoundException
                    | FlowClosingException terminal) {
                if (isClosedFlow(workflowId)) {
                    return OfferResponseFlow.OFFER_RESPONSE_WINDOW_CLOSED;
                }
                throw terminal;
            }
        }
    }

    /** Tells a closed Flow, whose window is closed too, from one that never existed. */
    private boolean isClosedFlow(final String workflowId) {
        try {
            return client.describeFlow(workflowId).getStatus() != FlowStatus.RUNNING;
        } catch (final FlowNotFoundException | FlowNotActiveOrNotFoundException notFound) {
            return false;
        }
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
