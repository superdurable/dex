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

package io.superdurable.dex.patterns.parallelsubflows;

import io.superdurable.dex.Client;
import io.superdurable.dex.Flow;
import io.superdurable.dex.shared.ExampleFlows;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RestController;

@RestController
@RequestMapping("/patterns/parallel-subflows")
public final class ParallelSubFlowsController {
    private final Client client;
    private final BasicParentFlow basic;
    private final WaitForHalfParentFlow waitForHalf;
    private final AdvancedLongLiveParentFlow longLive;
    private final AdvancedShortLiveParentFlow shortLive;
    private final SubmitRequestFlow submit;

    public ParallelSubFlowsController(
            final Client client,
            final BasicParentFlow basic,
            final WaitForHalfParentFlow waitForHalf,
            final AdvancedLongLiveParentFlow longLive,
            final AdvancedShortLiveParentFlow shortLive,
            final SubmitRequestFlow submit) {
        this.client = client;
        this.basic = basic;
        this.waitForHalf = waitForHalf;
        this.longLive = longLive;
        this.shortLive = shortLive;
        this.submit = submit;
    }

    @GetMapping("/start/basic")
    ResponseEntity<String> startBasic(@RequestParam final String workflowId) {
        return start(basic, workflowId, new String[] {"one", "two", "three", "four"});
    }

    @GetMapping("/start/wait-for-half")
    ResponseEntity<String> startWaitForHalf(@RequestParam final String workflowId) {
        return start(waitForHalf, workflowId, new String[] {"one", "two", "three", "four"});
    }

    @GetMapping("/start/long-lived-parent")
    ResponseEntity<String> startLongLive(@RequestParam final String workflowId) {
        return start(longLive, workflowId, new ParentInput(new String[] {"one", "two", "three"}, 3));
    }

    @GetMapping("/start/short-lived-parent")
    ResponseEntity<String> startShortLive(@RequestParam final String workflowId) {
        return start(shortLive, workflowId, new ParentInput(new String[] {"one", "two", "three"}, 3));
    }

    @GetMapping("/start/submit")
    ResponseEntity<String> startSubmit(@RequestParam final String workflowId) {
        return start(
                submit,
                workflowId,
                new SubmitRequestInput("one", new String[] {"parallel-parent-0", "parallel-parent-1"}));
    }

    private <T> ResponseEntity<String> start(
            final Flow<T> flow, final String workflowId, final T input) {
        return ResponseEntity.ok(
                client.startFlow(flow, workflowId, input, ExampleFlows.startOptions()));
    }
}
