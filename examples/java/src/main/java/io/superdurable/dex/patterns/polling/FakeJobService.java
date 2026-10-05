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

package io.superdurable.dex.patterns.polling;

import org.springframework.stereotype.Component;

import java.time.Duration;
import java.time.Instant;
import java.util.HashMap;
import java.util.HashSet;
import java.util.Map;
import java.util.NoSuchElementException;
import java.util.Set;

/** Stands in for an external job API, such as a cloud build service. */
@Component
public class FakeJobService {
    private static final Duration FAKE_JOB_DURATION = Duration.ofSeconds(6);

    private final Map<String, Instant> startedAt = new HashMap<>();
    private final Set<String> completedJobIds = new HashSet<>();

    /** Starts a job idempotently per job ID, so a retried Step does not start a second job. */
    public synchronized void startJob(final String jobId, final Duration requestTimeout) {
        startedAt.putIfAbsent(jobId, Instant.now());
    }

    public synchronized JobStatus getJobStatus(final String jobId, final Duration requestTimeout) {
        final Duration elapsed = Duration.between(requireStartedAt(jobId), Instant.now());
        if (completedJobIds.contains(jobId) || elapsed.compareTo(FAKE_JOB_DURATION) >= 0) {
            return new JobStatus(jobId, JobState.SUCCEEDED, "artifact for " + jobId);
        }
        if (elapsed.compareTo(FAKE_JOB_DURATION.dividedBy(3)) >= 0) {
            return new JobStatus(jobId, JobState.RUNNING, null);
        }
        return new JobStatus(jobId, JobState.QUEUED, null);
    }

    /** Finishes a job early so callers do not wait for its full duration. */
    public synchronized void completeJob(final String jobId) {
        requireStartedAt(jobId);
        completedJobIds.add(jobId);
    }

    private Instant requireStartedAt(final String jobId) {
        final Instant jobStartedAt = startedAt.get(jobId);
        if (jobStartedAt == null) {
            throw new NoSuchElementException("job " + jobId + " not found");
        }
        return jobStartedAt;
    }
}
