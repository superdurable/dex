/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Sustainable Use License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0
 */

package io.superdurable.dex;

import io.superdurable.gen.StepMethodHeartbeat;
import io.superdurable.gen.StepStreamWrite;

interface StepOutputEmitter {
    void emitHeartbeat(StepMethodHeartbeat heartbeat);

    void emitStreamWrite(StepStreamWrite streamWrite);
}
