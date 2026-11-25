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

import java.util.ArrayList;
import java.util.HashMap;
import java.util.HashSet;
import java.util.List;
import java.util.Map;
import java.util.Set;
import org.springframework.stereotype.Component;

/**
 * In-memory stand-in for the system that keeps records of closed accounts. Archiving is
 * idempotent per account, as a unique insert keyed by the Flow ID would be.
 */
@Component
public class FakeAccountArchive {
    private final Set<String> unavailableFlowIds = new HashSet<>();
    private final Set<String> lostResponseFlowIds = new HashSet<>();
    private final Map<String, String> archiveRecords = new HashMap<>();
    private final Map<String, List<String>> attemptStepExecutionIds = new HashMap<>();

    public synchronized void archiveAccount(final String flowId, final String stepExecutionId) {
        attemptStepExecutionIds.computeIfAbsent(flowId, key -> new ArrayList<>()).add(stepExecutionId);
        if (archiveRecords.containsKey(flowId)) {
            return;
        }
        if (unavailableFlowIds.contains(flowId)) {
            throw new IllegalStateException("account archive is unavailable");
        }
        archiveRecords.put(flowId, stepExecutionId);
        if (lostResponseFlowIds.remove(flowId)) {
            throw new IllegalStateException("account archive response was lost");
        }
    }

    public synchronized void makeUnavailable(final String flowId) {
        unavailableFlowIds.add(flowId);
    }

    public synchronized void makeAvailable(final String flowId) {
        unavailableFlowIds.remove(flowId);
    }

    public synchronized void loseNextResponse(final String flowId) {
        lostResponseFlowIds.add(flowId);
    }

    public synchronized int archiveRecordCount(final String flowId) {
        return archiveRecords.containsKey(flowId) ? 1 : 0;
    }

    public synchronized List<String> attemptStepExecutionIds(final String flowId) {
        return new ArrayList<>(attemptStepExecutionIds.getOrDefault(flowId, List.of()));
    }
}
