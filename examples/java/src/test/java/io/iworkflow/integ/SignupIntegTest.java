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

package io.iworkflow.integ;

import io.iworkflow.SpringMainApplication;
import io.iworkflow.core.Client;
import io.iworkflow.workflow.microservices.ImmutableSignupForm;
import io.iworkflow.workflow.microservices.SignupForm;
import io.iworkflow.workflow.signup.UserSignupWorkflow;
import org.junit.jupiter.api.Assertions;
import org.junit.jupiter.api.Test;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.boot.test.context.SpringBootTest;

@SpringBootTest(
        classes = SpringMainApplication.class,
        webEnvironment = SpringBootTest.WebEnvironment.DEFINED_PORT
)
public class SignupIntegTest {

    @Autowired
    private Client client;

    @Test
    public void testSignupSubmitAndVerify() {
        final String username = "ci-user-" + System.nanoTime();
        final SignupForm form = ImmutableSignupForm.builder()
                .username(username)
                .email("ci@example.com")
                .firstName("Ci")
                .lastName("User")
                .build();

        final String runId = client.startWorkflow(UserSignupWorkflow.class, username, 60, form);
        Assertions.assertNotNull(runId);
        Assertions.assertFalse(runId.isEmpty());

        final UserSignupWorkflow rpcStub = client.newRpcStub(UserSignupWorkflow.class, username);
        final String verifyResult = client.invokeRPC(rpcStub::verify);
        Assertions.assertEquals("done", verifyResult);

        final String output = client.getSimpleWorkflowResultWithWait(String.class, username);
        Assertions.assertEquals("done", output);
    }
}
