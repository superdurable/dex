/*
 * Copyright (c) 2026 Super Durable, Inc.
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

import io.superdurable.dex.StreamMessage;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;

import java.time.Duration;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;

@ExtendWith(SharedIntegExtension.class)
public class StreamIntegTest {
    @Test
    void streamResumesAfterStepAndClientWrites() {
        final IntegEnvironment environment = SharedIntegExtension.environment();
        final String flowId = environment.newFlowId("stream");
        environment.client().startFlow(
                environment.streamFlow(),
                flowId,
                "invoice",
                environment.startOptions());

        final StreamMessage<String> stepMessage = environment.client().readStream(
                flowId,
                environment.streamFlow().progress,
                "",
                Duration.ofSeconds(20));
        assertEquals("Rendering preview for invoice", stepMessage.getValue());
        assertFalse(stepMessage.getResumeToken().isEmpty());

        environment.client().writeStream(
                flowId,
                environment.streamFlow().progress,
                "browser/complete",
                "Preview displayed");
        final StreamMessage<String> clientMessage = environment.client().readStream(
                flowId,
                environment.streamFlow().progress,
                stepMessage.getResumeToken(),
                Duration.ofSeconds(20));
        assertEquals("Preview displayed", clientMessage.getValue());
        assertEquals("browser/complete", clientMessage.getIdempotencyKey());
    }
}
