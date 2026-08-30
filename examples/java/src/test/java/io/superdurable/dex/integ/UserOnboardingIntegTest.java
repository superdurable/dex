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

package io.superdurable.dex.integ;

import io.superdurable.dex.products.signup.SignupForm;
import io.superdurable.dex.products.signup.UserOnboardingFlow;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;

import java.time.Duration;

import static org.junit.jupiter.api.Assertions.assertEquals;

@ExtendWith(SharedIntegExtension.class)
public class UserOnboardingIntegTest {
    @Test
    void userOnboardingVerifiesAndCompletesBothTasks() throws Exception {
        final IntegEnvironment environment = SharedIntegExtension.environment();
        final UserOnboardingFlow flow = environment.userOnboardingFlow();
        final String flowId = environment.newFlowId("user-onboarding");
        final SignupForm form = new SignupForm(
                flowId,
                flowId + "@example.com",
                "Test",
                "User");

        environment.client().startFlow(flow, flowId, form, environment.startOptions());
        environment.awaitAttribute(
                flowId,
                flow.status,
                UserOnboardingFlow.WAITING_FOR_VERIFICATION,
                Duration.ofSeconds(20));
        final UserOnboardingFlow stub = environment.client().newRpcStub(UserOnboardingFlow.class, flowId);

        assertEquals("verified", environment.client().invokeRPC(stub::verify));
        environment.awaitAttribute(
                flowId,
                flow.status,
                UserOnboardingFlow.WAITING_FOR_TASK_1,
                Duration.ofSeconds(20));

        assertEquals("task 1 accomplished", environment.client().invokeRPC(stub::accomplishTask1));
        environment.awaitAttribute(
                flowId,
                flow.status,
                UserOnboardingFlow.WAITING_FOR_TASK_2,
                Duration.ofSeconds(20));

        assertEquals("task 2 accomplished", environment.client().invokeRPC(stub::accomplishTask2));
        final String output = environment.client()
                .waitForFlow(flowId, Duration.ofSeconds(45))
                .getSingleOutput(String.class);
        assertEquals("onboarding completed", output);
    }
}
