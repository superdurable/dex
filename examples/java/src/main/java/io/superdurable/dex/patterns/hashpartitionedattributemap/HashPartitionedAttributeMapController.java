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

package io.superdurable.dex.patterns.hashpartitionedattributemap;

import io.superdurable.dex.Client;
import io.superdurable.dex.RPCInvokeOptions;
import io.superdurable.dex.StartFlowOptions;
import java.util.Map;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.PutMapping;
import org.springframework.web.bind.annotation.RequestBody;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RestController;

@RestController
@RequestMapping("/patterns/hash-partitioned-attribute-map")
public class HashPartitionedAttributeMapController {
    private final Client client;
    private final CustomerDirectoryFlow flow;

    public HashPartitionedAttributeMapController(
            final Client client,
            final CustomerDirectoryFlow flow) {
        this.client = client;
        this.flow = flow;
    }

    @PostMapping("/start")
    ResponseEntity<Map<String, String>> start() {
        final String runId = client.startFlow(
                flow,
                CustomerDirectoryFlow.FLOW_ID,
                null,
                StartFlowOptions.newBuilder().ignoreAlreadyStarted(true).build());
        return ResponseEntity.ok(Map.of(
                "flowID", CustomerDirectoryFlow.FLOW_ID,
                "runID", runId));
    }

    @PutMapping("/customer-profile")
    ResponseEntity<CustomerDirectoryFlow.CustomerProfile> upsertCustomerProfile(
            @RequestBody final CustomerDirectoryFlow.CustomerProfile profile) {
        final CustomerDirectoryFlow.EmailPartition partition =
                CustomerDirectoryFlow.emailPartition(profile.emailAddress);
        final RPCInvokeOptions options = RPCInvokeOptions.newBuilder()
                .addLockAttributeMapInstance(
                        flow.customerProfilesByEmailPartition,
                        partition.partitionName)
                .addLoadAttributeMapInstance(
                        flow.customerProfilesByEmailPartition,
                        partition.partitionName)
                .build();
        final CustomerDirectoryFlow stub = client.newRpcStub(
                CustomerDirectoryFlow.class,
                CustomerDirectoryFlow.FLOW_ID);
        return ResponseEntity.ok(client.invokeRPC(
                stub::upsertCustomerProfile,
                profile,
                options));
    }

    @GetMapping("/customer-profile")
    ResponseEntity<CustomerDirectoryFlow.CustomerProfile> getCustomerProfile(
            @RequestParam final String emailAddress) {
        final CustomerDirectoryFlow.EmailPartition partition =
                CustomerDirectoryFlow.emailPartition(emailAddress);
        final RPCInvokeOptions options = RPCInvokeOptions.newBuilder()
                .addLoadAttributeMapInstance(
                        flow.customerProfilesByEmailPartition,
                        partition.partitionName)
                .build();
        final CustomerDirectoryFlow stub = client.newRpcStub(
                CustomerDirectoryFlow.class,
                CustomerDirectoryFlow.FLOW_ID);
        return ResponseEntity.ok(client.invokeRPC(
                stub::getCustomerProfileByEmail,
                emailAddress,
                options));
    }
}
