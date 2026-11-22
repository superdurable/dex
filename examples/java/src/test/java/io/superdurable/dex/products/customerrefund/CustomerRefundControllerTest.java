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

package io.superdurable.dex.products.customerrefund;

import io.superdurable.dex.FlowStatus;
import org.junit.jupiter.api.Test;

import static org.junit.jupiter.api.Assertions.assertEquals;

class CustomerRefundControllerTest {
    @Test
    void describesFlowStatusesUsingTheRouteVocabulary() {
        assertEquals("running", CustomerRefundController.flowStatusName(FlowStatus.RUNNING));
        assertEquals("completed", CustomerRefundController.flowStatusName(FlowStatus.COMPLETED));
        assertEquals("failed", CustomerRefundController.flowStatusName(FlowStatus.FAILED));
        assertEquals("canceled", CustomerRefundController.flowStatusName(FlowStatus.CANCELED));
        assertEquals("terminated", CustomerRefundController.flowStatusName(FlowStatus.TERMINATED));
        assertEquals("timeout", CustomerRefundController.flowStatusName(FlowStatus.SERVER_SIDE_TIMEOUT_INTERNAL_ONLY));
        assertEquals("unknown", CustomerRefundController.flowStatusName(null));
    }
}
