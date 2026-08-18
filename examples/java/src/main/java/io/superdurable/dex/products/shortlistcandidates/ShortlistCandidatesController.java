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

package io.superdurable.dex.products.shortlistcandidates;

import io.superdurable.dex.Client;
import io.superdurable.dex.shared.ExampleFlows;
import io.superdurable.dex.exceptions.FlowAlreadyStartedException;
import io.superdurable.dex.exceptions.FlowNotActiveException;
import org.springframework.http.ResponseEntity;
import org.springframework.stereotype.Controller;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.RequestBody;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestParam;

import java.util.Map;

@Controller
@RequestMapping("/products/shortlist-candidates")
public class ShortlistCandidatesController {
    private final Client client;
    private final EmployerOptInFlow employerOptInFlow;
    private final ShortlistFlow shortlistFlow;

    public ShortlistCandidatesController(
            final Client client,
            final EmployerOptInFlow employerOptInFlow,
            final ShortlistFlow shortlistFlow) {
        this.client = client;
        this.employerOptInFlow = employerOptInFlow;
        this.shortlistFlow = shortlistFlow;
    }

    @PostMapping("/opt_in")
    public ResponseEntity<String> optIn(@RequestBody final Map<String, String> requestBody) {
        final String employerId = requestBody.get("employerId");
        final String workflowId = WorkflowIds.employerOptIn(employerId);
        final EmployerOptInInput input = new EmployerOptInInput(employerId);

        try {
            client.startFlow(
                    employerOptInFlow,
                    workflowId,
                    input,
                    ExampleFlows.startOptions());
        } catch (final FlowAlreadyStartedException alreadyStarted) {
            return ResponseEntity.ok(
                    String.format("Employer %s has already opted in", employerId));
        }

        return ResponseEntity.ok(String.format("Started workflowId: %s", workflowId));
    }

    @PostMapping("/opt_out")
    public ResponseEntity<String> optOut(@RequestBody final Map<String, String> requestBody) {
        final String employerId = requestBody.get("employerId");
        final String workflowId = WorkflowIds.employerOptIn(employerId);
        final EmployerOptInFlow stub =
                client.newRpcStub(EmployerOptInFlow.class, workflowId);
        try {
            client.invokeRPC(stub::optOut);
        } catch (final FlowNotActiveException inactive) {
            return ResponseEntity.ok(
                    String.format("Employer %s is not in the opt-in status", employerId));
        }
        return ResponseEntity.ok(String.format("Employer %s has opted out", employerId));
    }

    @GetMapping("/is_opted_in")
    public ResponseEntity<Boolean> isOptedIn(
            @RequestParam(defaultValue = "test-employer") final String employerId) {
        return ResponseEntity.ok(
                WorkflowIds.isOptedIn(client, employerOptInFlow, employerId));
    }

    @PostMapping("/shortlist")
    public ResponseEntity<String> shortlist(@RequestBody final Map<String, String> requestBody) {
        final String employerId = requestBody.get("employerId");
        final String candidateId = requestBody.get("candidateId");

        if (!WorkflowIds.isOptedIn(client, employerOptInFlow, employerId)) {
            return ResponseEntity.ok(String.format(
                    "Do nothing for %s because of no opt-in",
                    employerId + "-" + candidateId));
        }

        final String workflowId = WorkflowIds.shortlist(employerId, candidateId);
        final ShortlistInput input = new ShortlistInput(employerId, candidateId);

        try {
            client.startFlow(
                    shortlistFlow,
                    workflowId,
                    input,
                    ExampleFlows.startOptions());
        } catch (final FlowAlreadyStartedException alreadyStarted) {
            return ResponseEntity.ok(
                    String.format("Already running workflowId: %s", workflowId));
        }

        return ResponseEntity.ok(String.format("Started workflowId: %s", workflowId));
    }

    @PostMapping("/revoke_shortlist")
    public ResponseEntity<String> revokeShortlist(
            @RequestBody final Map<String, String> requestBody) {
        final String employerId = requestBody.get("employerId");
        final String candidateId = requestBody.get("candidateId");
        final String workflowId = WorkflowIds.shortlist(employerId, candidateId);

        try {
            client.publish(workflowId, shortlistFlow.revokeShortlist, (Void) null);
        } catch (final FlowNotActiveException inactive) {
            return ResponseEntity.ok(String.format(
                    "No running workflow to revoke for %s",
                    employerId + "-" + candidateId));
        }

        return ResponseEntity.ok(String.format(
                "Revoked shortlist for %s",
                employerId + "-" + candidateId));
    }

    @GetMapping("/email_sent_timestamp")
    public ResponseEntity<Long> getEmailSentTimestamp(
            @RequestParam(defaultValue = "test-employer") final String employerId,
            @RequestParam(defaultValue = "test-candidate") final String candidateId) {
        final String workflowId = WorkflowIds.shortlist(employerId, candidateId);
        final ShortlistFlow stub = client.newRpcStub(ShortlistFlow.class, workflowId);
        try {
            return ResponseEntity.ok(client.invokeRPC(stub::getEmailSentTimestamp));
        } catch (final FlowNotActiveException inactive) {
            return ResponseEntity.notFound().build();
        }
    }
}
