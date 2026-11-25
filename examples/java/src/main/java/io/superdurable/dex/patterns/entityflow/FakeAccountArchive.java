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

package io.superdurable.dex.patterns.entityflow;

import java.util.HashMap;
import java.util.HashSet;
import java.util.Map;
import java.util.Set;
import org.springframework.stereotype.Component;

/** In-memory stand-in for the system that keeps records of closed accounts. */
@Component
public class FakeAccountArchive {
    private final Set<String> unavailableFlowIds = new HashSet<>();
    private final Map<String, Integer> archiveCounts = new HashMap<>();

    public synchronized void archiveAccount(final String flowId) {
        if (unavailableFlowIds.contains(flowId)) {
            throw new IllegalStateException("account archive is unavailable");
        }
        archiveCounts.merge(flowId, 1, Integer::sum);
    }

    public synchronized void makeUnavailable(final String flowId) {
        unavailableFlowIds.add(flowId);
    }

    public synchronized void makeAvailable(final String flowId) {
        unavailableFlowIds.remove(flowId);
    }

    public synchronized int archiveCount(final String flowId) {
        return archiveCounts.getOrDefault(flowId, 0);
    }
}
