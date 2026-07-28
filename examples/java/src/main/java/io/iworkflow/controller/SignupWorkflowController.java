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

package io.iworkflow.controller;

import io.iworkflow.core.Client;
import io.iworkflow.core.exceptions.WorkflowAlreadyStartedException;
import io.iworkflow.workflow.microservices.ImmutableSignupForm;
import io.iworkflow.workflow.signup.UserSignupWorkflow;
import org.springframework.http.ResponseEntity;
import org.springframework.stereotype.Controller;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestParam;

@Controller
@RequestMapping("/signup")
public class SignupWorkflowController {

    private final Client client;

    public SignupWorkflowController(
            final Client client
    ) {
        this.client = client;
    }

    @GetMapping("/submit")
    public ResponseEntity<String> start(
            @RequestParam String username,
            @RequestParam String email
    ) {
        try {
            final ImmutableSignupForm form = ImmutableSignupForm.builder()
                    .username(username)
                    .email(email)
                    .firstName("Test")
                    .lastName("Test")
                    .build();
            client.startWorkflow(UserSignupWorkflow.class, username, 3600, form);
        } catch (WorkflowAlreadyStartedException e) {
            return ResponseEntity.ok("username already started registry");
        }
        return ResponseEntity.ok("success");
    }

    @GetMapping("/verify")
    ResponseEntity<String> verify(
            @RequestParam String username) {
        final UserSignupWorkflow rpcStub = client.newRpcStub(UserSignupWorkflow.class, username);
        String result = client.invokeRPC(rpcStub::verify);
        return ResponseEntity.ok(result);
    }
}