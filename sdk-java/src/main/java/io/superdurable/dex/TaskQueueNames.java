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

import java.util.regex.Pattern;

final class TaskQueueNames {
    static final String DEFAULT_TASK_QUEUE_ALIAS = "dex.default";
    private static final String RESERVED_TASK_QUEUE_PREFIX = "dex.";
    private static final Pattern TASK_QUEUE_NAME_PATTERN = Pattern.compile("[A-Za-z0-9._-]{1,63}");

    private TaskQueueNames() {
    }

    static String validatePullWorkerTaskQueue(final String taskQueue) {
        if (taskQueue == null) {
            throw new IllegalArgumentException("taskQueue must not be null");
        }
        if (taskQueue.isEmpty()) {
            return taskQueue;
        }
        if (taskQueue.startsWith(RESERVED_TASK_QUEUE_PREFIX)) {
            throw new IllegalArgumentException("taskQueue \"" + taskQueue
                    + "\" is invalid: names starting with \"dex.\" are reserved");
        }
        return requireTaskQueueCharacters(taskQueue);
    }

    // A Flow's Worker target may also name the default task queue explicitly.
    static String validateFlowTaskQueue(final String taskQueue) {
        if (DEFAULT_TASK_QUEUE_ALIAS.equals(taskQueue)) {
            return taskQueue;
        }
        return validatePullWorkerTaskQueue(taskQueue);
    }

    private static String requireTaskQueueCharacters(final String taskQueue) {
        if (!TASK_QUEUE_NAME_PATTERN.matcher(taskQueue).matches()) {
            throw new IllegalArgumentException("taskQueue \"" + taskQueue
                    + "\" is invalid: use 1-63 ASCII letters, digits, periods, underscores, or hyphens");
        }
        return taskQueue;
    }
}
