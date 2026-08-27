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

package io.superdurable.dex.patterns.entitystore;

import io.superdurable.dex.Client;
import io.superdurable.dex.FlowConfig;
import io.superdurable.dex.StartFlowOptions;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.RequestBody;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RestController;

import java.time.Duration;

@RestController
@RequestMapping("/patterns/entity-store")
public class EntityStoreController {
    private final Client client;
    private final UserProfileFlow userProfileFlow;

    public EntityStoreController(final Client client, final UserProfileFlow userProfileFlow) {
        this.client = client;
        this.userProfileFlow = userProfileFlow;
    }

    @PostMapping("/profile")
    ResponseEntity<String> createUserProfile(@RequestBody final UserProfileRequest request) {
        final UserProfile profile = request.toProfile();
        final StartFlowOptions options = StartFlowOptions.newBuilder()
                .timeout(Duration.ofHours(1))
                .addAttribute(userProfileFlow.displayName, profile.displayName)
                .addAttribute(userProfileFlow.email, profile.email)
                .addAttribute(userProfileFlow.marketingOptIn, profile.marketingOptIn)
                .addAttribute(userProfileFlow.credits, profile.credits)
                .addAttribute(userProfileFlow.weight, profile.weight)
                .addAttribute(userProfileFlow.lastLoggedInTime, profile.lastLoggedInTime)
                .addAttribute(userProfileFlow.metadata, profile.metadata)
                .configOverride(FlowConfig.newBuilder()
                        .attributeStoreNames(java.util.List.of(UserProfileFlow.STORE_NAME))
                        .build())
                .build();
        final String runId = client.startFlow(
                userProfileFlow,
                request.userId,
                null,
                options);
        return ResponseEntity.ok(runId);
    }

    @PostMapping("/profile/update")
    ResponseEntity<String> updateUserProfile(@RequestBody final UserProfileRequest request) {
        final UserProfileFlow stub =
                client.newRpcStub(UserProfileFlow.class, request.userId);
        client.invokeRPC(stub::updateProfile, request.toProfile());
        return ResponseEntity.ok("Updated user profile");
    }

    @GetMapping("/profile")
    ResponseEntity<UserProfile> getUserProfile(@RequestParam final String userId) {
        final UserProfileFlow stub = client.newRpcStub(UserProfileFlow.class, userId);
        return ResponseEntity.ok(client.invokeRPC(stub::getProfile));
    }

    @PostMapping("/profile/clear")
    ResponseEntity<String> clearUserProfile(@RequestParam final String userId) {
        final UserProfileFlow stub = client.newRpcStub(UserProfileFlow.class, userId);
        client.invokeRPC(stub::clearProfile);
        return ResponseEntity.ok("Cleared user profile");
    }
}
