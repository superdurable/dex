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

package io.superdurable.dex.patterns.scalableparallel;

import io.superdurable.dex.Client;
import io.superdurable.dex.IdReusePolicy;
import io.superdurable.dex.StartFlowOptions;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RestController;

import java.time.Duration;

@RestController
@RequestMapping("/patterns/scalable-parallel")
public class ScalableParallelController {
    private final Client client;
    private final RequestReceiverFlow requestReceiverFlow;

    public ScalableParallelController(
            final Client client,
            final RequestReceiverFlow requestReceiverFlow) {
        this.client = client;
        this.requestReceiverFlow = requestReceiverFlow;
    }

    @GetMapping("/start")
    ResponseEntity<String> start(
            @RequestParam final String workflowId,
            @RequestParam final int numOfChildWfs) {
        client.startFlow(
                requestReceiverFlow,
                workflowId,
                numOfChildWfs,
                StartFlowOptions.newBuilder()
                        .timeout(Duration.ofHours(1))
                        .idReusePolicy(IdReusePolicy.ALLOW_IF_PREVIOUS_FAILED)
                        .build());
        return ResponseEntity.ok("success");
    }
}
