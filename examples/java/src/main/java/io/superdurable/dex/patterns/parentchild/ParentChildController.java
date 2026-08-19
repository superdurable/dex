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

package io.superdurable.dex.patterns.parentchild;

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
@RequestMapping("/patterns/parent-child")
public class ParentChildController {
    private final Client client;
    private final ParentFlowV2 parentFlowV2;

    public ParentChildController(final Client client, final ParentFlowV2 parentFlowV2) {
        this.client = client;
        this.parentFlowV2 = parentFlowV2;
    }

    @GetMapping("/start")
    ResponseEntity<String> start(
            @RequestParam final String workflowId,
            @RequestParam final int numOfChildWfs) {
        client.startFlow(
                parentFlowV2,
                workflowId,
                numOfChildWfs,
                StartFlowOptions.newBuilder()
                        .timeout(Duration.ofHours(1))
                        .idReusePolicy(IdReusePolicy.ALLOW_IF_PREVIOUS_FAILED)
                        .build());
        return ResponseEntity.ok("success");
    }
}
