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

package io.superdurable.dex.controller;

import io.superdurable.dex.StartFlowOptions;

import java.time.Duration;

final class ExampleFlows {
    private static final Duration DEFAULT_TIMEOUT = Duration.ofHours(24);

    private ExampleFlows() {
    }

    static StartFlowOptions startOptions() {
        return StartFlowOptions.newBuilder().timeout(DEFAULT_TIMEOUT).build();
    }
}
