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

package io.superdurable.dex.products.signup;

import io.superdurable.dex.Client;
import io.superdurable.dex.shared.ExampleFlows;
import io.superdurable.dex.exceptions.FlowAlreadyStartedException;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RestController;

@RestController
@RequestMapping("/products/signup")
public class SignupController {
    private final Client client;
    private final UserOnboardingFlow flow;

    public SignupController(
            final Client client,
            final UserOnboardingFlow flow) {
        this.client = client;
        this.flow = flow;
    }

    @GetMapping("/submit")
    public ResponseEntity<String> submit(
            @RequestParam final String username,
            @RequestParam final String email) {
        final SignupForm form = new SignupForm(username, email, "Test", "Test");
        try {
            client.startFlow(flow, username, form, ExampleFlows.startOptions());
        } catch (final FlowAlreadyStartedException alreadyStarted) {
            return ResponseEntity.ok("username already started registry");
        }
        return ResponseEntity.ok("success");
    }

    @GetMapping("/verify")
    public ResponseEntity<String> verify(@RequestParam final String username) {
        final UserOnboardingFlow stub = client.newRpcStub(UserOnboardingFlow.class, username);
        return ResponseEntity.ok(client.invokeRPC(stub::verify));
    }

    @GetMapping("/accomplish-task-1")
    public ResponseEntity<String> accomplishTask1(@RequestParam final String username) {
        final UserOnboardingFlow stub = client.newRpcStub(UserOnboardingFlow.class, username);
        return ResponseEntity.ok(client.invokeRPC(stub::accomplishTask1));
    }

    @GetMapping("/accomplish-task-2")
    public ResponseEntity<String> accomplishTask2(@RequestParam final String username) {
        final UserOnboardingFlow stub = client.newRpcStub(UserOnboardingFlow.class, username);
        return ResponseEntity.ok(client.invokeRPC(stub::accomplishTask2));
    }
}
