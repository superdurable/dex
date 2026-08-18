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

package io.superdurable.dex.integ.smoke;

import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;

import static org.junit.jupiter.api.Assertions.assertFalse;

@ExtendWith(SharedFlowSmokeExtension.class)
public class FlowSmokeTest {
    @Test
    void allRegisteredFlowsViaController() throws Exception {
        final FlowSmokeEnvironment environment = SharedFlowSmokeExtension.environment();
        final var catalog = FlowSmokeCatalog.entries(environment);
        assertFalse(catalog.isEmpty(), "flow smoke catalog is empty");
        for (final FlowSmokeEntry entry : catalog) {
            final FlowSmokeTriggerResult result = entry.trigger.trigger(environment);
            if (result.flowId == null || result.flowId.isEmpty()) {
                throw new AssertionError(entry.name + ": controller response did not include flowID");
            }
            FlowSmokeHelper.assertStartStep(entry, result.flowId, result.runId);
            FlowSmokeHelper.assertNoUnexpectedFailures(entry, result.flowId, result.runId);
        }
    }
}
