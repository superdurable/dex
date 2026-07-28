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

import io.superdurable.dex.SpringMainApplication;
import io.superdurable.dex.core.Client;
import io.superdurable.dex.workflow.money.transfer.ImmutableTransferRequest;
import io.superdurable.dex.workflow.money.transfer.MoneyTransferWorkflow;
import io.superdurable.dex.workflow.money.transfer.TransferRequest;
import org.junit.jupiter.api.Assertions;
import org.junit.jupiter.api.Test;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.boot.test.context.SpringBootTest;

@SpringBootTest(
        classes = SpringMainApplication.class,
        webEnvironment = SpringBootTest.WebEnvironment.DEFINED_PORT
)
public class MoneyTransferIntegTest {

    @Autowired
    private Client client;

    @Test
    public void testMoneyTransferCompletes() {
        final String wfId = "examples/java-moneytransfer-" + System.nanoTime();
        final TransferRequest request = ImmutableTransferRequest.builder()
                .fromAccountId("from-ci")
                .toAccountId("to-ci")
                .amount(42)
                .notes("examples/java integ")
                .build();

        final String runId = client.startWorkflow(MoneyTransferWorkflow.class, wfId, 60, request);
        Assertions.assertNotNull(runId);
        Assertions.assertFalse(runId.isEmpty());

        final String result = client.getSimpleWorkflowResultWithWait(String.class, wfId);
        Assertions.assertTrue(result.contains("transfer is done"));
        Assertions.assertTrue(result.contains("from-ci"));
        Assertions.assertTrue(result.contains("to-ci"));
    }
}
