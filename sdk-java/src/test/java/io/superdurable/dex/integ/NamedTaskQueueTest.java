/*
 * Portions of this file are derived from indeedeng/iwf-java-sdk.
 * Those portions are licensed under the Apache License, Version 2.0.
 * See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
 *
 * Modifications Copyright (c) 2026 Super Durable, Inc.
 *
 * Modifications are licensed under the Sustainable Use License 1.0.
 * Third-Party Materials remain under the Apache License, Version 2.0.
 * See LICENSE and LEGACY_NOTICES.md.
 */

package io.superdurable.dex.integ;

import io.superdurable.dex.BlobCache;
import io.superdurable.dex.BlobCacheConfig;
import io.superdurable.dex.Client;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PullQueueRegistrationState;
import io.superdurable.dex.PullRegistrationState;
import io.superdurable.dex.PullWorkerOptions;
import io.superdurable.dex.Registry;
import io.superdurable.dex.StartFlowOptions;
import io.superdurable.dex.Worker;
import io.superdurable.dex.WorkerOptions;
import io.superdurable.dex.testing.DexDevTestEnvironment;
import org.junit.jupiter.api.Tag;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

import java.nio.file.Files;
import java.nio.file.Path;
import java.time.Duration;
import java.util.Collections;
import java.util.UUID;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

@Tag("dex-dev")
final class NamedTaskQueueTest {
    private static final String BLUE_LABEL = "blue";
    private static final String GREEN_LABEL = "green";
    private static final String DEFAULT_LABEL = "default";

    @TempDir
    Path cacheDirectory;

    @Test
    void routesEachFlowOnlyToWorkersOfItsTaskQueue() throws Exception {
        final String suffix = UUID.randomUUID().toString().substring(0, 8);
        final String flowType = "JavaNamedTaskQueueFlow-" + suffix;
        final String parentFlowType = "JavaNamedTaskQueueParentFlow-" + suffix;
        final String blueTaskQueue = "java-blue-" + suffix;
        final String greenTaskQueue = "java-green-" + suffix;
        final NamedTaskQueueWorkflow blueFlow = new NamedTaskQueueWorkflow(flowType, BLUE_LABEL);
        final NamedTaskQueueParentWorkflow blueParent = new NamedTaskQueueParentWorkflow(parentFlowType, BLUE_LABEL);
        final NamedTaskQueueWorkflow greenFlow = new NamedTaskQueueWorkflow(flowType, GREEN_LABEL);
        final NamedTaskQueueWorkflow defaultFlow = new NamedTaskQueueWorkflow(flowType, DEFAULT_LABEL);
        final NamedTaskQueueParentWorkflow defaultParent =
                new NamedTaskQueueParentWorkflow(parentFlowType, DEFAULT_LABEL);
        try (DexDevTestEnvironment blue = DexDevTestEnvironment.start(
                        Files.createDirectory(cacheDirectory.resolve(BLUE_LABEL)),
                        workerOptions(blueTaskQueue), blueFlow, blueParent);
                DexDevTestEnvironment green = DexDevTestEnvironment.start(
                        Files.createDirectory(cacheDirectory.resolve(GREEN_LABEL)),
                        workerOptions(greenTaskQueue), greenFlow,
                        new NamedTaskQueueParentWorkflow(parentFlowType, GREEN_LABEL));
                DexDevTestEnvironment defaultQueue = DexDevTestEnvironment.start(
                        Files.createDirectory(cacheDirectory.resolve(DEFAULT_LABEL)),
                        workerOptions(""), defaultFlow, defaultParent)) {
            for (int round = 0; round < 3; round++) {
                assertEquals(BLUE_LABEL, run(blue.client(), blueFlow, blueTaskQueue, "blue-" + round));
                assertEquals(GREEN_LABEL, run(green.client(), greenFlow, greenTaskQueue, "green-" + round));
                assertEquals(DEFAULT_LABEL, run(defaultQueue.client(), defaultFlow, "", "default-" + round));
                assertEquals(DEFAULT_LABEL,
                        run(defaultQueue.client(), defaultFlow, "dex.default", "explicit-default-" + round));
            }
            assertEquals(BLUE_LABEL + ">" + BLUE_LABEL,
                    runParent(blue.client(), blueParent, blueTaskQueue, "", "inherit"));
            assertEquals(BLUE_LABEL + ">" + DEFAULT_LABEL,
                    runParent(blue.client(), blueParent, blueTaskQueue, "dex.default", "to-default"));
            assertEquals(DEFAULT_LABEL + ">" + GREEN_LABEL,
                    runParent(defaultQueue.client(), defaultParent, "", greenTaskQueue, "to-green"));
        }
    }

    @Test
    void registrationStateCarriesTheTaskQueue() throws Exception {
        final String taskQueue = "java-state-" + UUID.randomUUID().toString().substring(0, 8);
        final Registry registry = new Registry(Collections.<Flow<?>>singletonList(
                new NamedTaskQueueWorkflow("JavaNamedTaskQueueStateFlow-" + taskQueue, BLUE_LABEL)));
        try (BlobCache cache = BlobCache.open(new BlobCacheConfig(cacheDirectory.toString(), 64L << 20));
                Worker worker = new Worker(registry, cache, workerOptions(taskQueue)
                        .serverAddress(DexDevTestEnvironment.serverAddress()).build())) {
            final Thread starting = new Thread(worker::start, "dex-java-named-task-queue-worker");
            starting.start();
            try {
                worker.waitPullRegistered(Duration.ofSeconds(10));
                final PullRegistrationState state = worker.getPullRegistrationState();
                assertTrue(state.isReady());
                assertFalse(state.getQueues().isEmpty());
                for (PullQueueRegistrationState registration : state.getQueues()) {
                    assertEquals(taskQueue, registration.getTaskQueue());
                    assertTrue(registration.hasRegisteredStream());
                }
            } finally {
                worker.stop();
                starting.join(5000L);
            }
            assertFalse(starting.isAlive());
        }
        assertThrows(IllegalArgumentException.class,
                () -> PullWorkerOptions.newBuilder().taskQueue("dex.default"));
        assertThrows(IllegalArgumentException.class,
                () -> StartFlowOptions.newBuilder().taskQueue("blue queue"));
    }

    private static WorkerOptions.Builder workerOptions(final String taskQueue) {
        return WorkerOptions.newBuilder().pull(PullWorkerOptions.newBuilder()
                .taskQueue(taskQueue).stopDrainTimeout(Duration.ofSeconds(1)).build());
    }

    private static String run(final Client client, final NamedTaskQueueWorkflow flow,
            final String taskQueue, final String flowIdPrefix) {
        final String flowId = "java-named-task-queue-" + flowIdPrefix + "-" + UUID.randomUUID();
        client.startFlow(flow, flowId, "input", StartFlowOptions.newBuilder().taskQueue(taskQueue).build());
        return client.waitForFlow(flowId, Duration.ofSeconds(30)).getSingleOutput(String.class);
    }

    private static String runParent(final Client client, final NamedTaskQueueParentWorkflow flow,
            final String taskQueue, final String subFlowTaskQueue, final String flowIdPrefix) {
        final String flowId = "java-named-task-queue-parent-" + flowIdPrefix + "-" + UUID.randomUUID();
        client.startFlow(flow, flowId, subFlowTaskQueue,
                StartFlowOptions.newBuilder().taskQueue(taskQueue).build());
        return client.waitForFlow(flowId, Duration.ofSeconds(30)).getSingleOutput(String.class);
    }
}
