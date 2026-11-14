// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package dex

import (
	"fmt"
	"strings"
)

// DefaultTaskQueue names the default task queue explicitly.
//
// Use it in SubFlowOptions.TaskQueue to run a SubFlow on the default task queue when its parent
// runs on a named task queue. An empty SubFlowOptions.TaskQueue keeps the parent's task queue
// instead. In StartFlowOptions.TaskQueue it routes like an empty name, but as a non-empty name
// it requires Server protocol version 5 or later, while an empty name also works with protocol 4.
// A Worker cannot register it; leave PullWorkerOptions.TaskQueue empty to serve the default task queue.
//
//	return dex.AllOf(dex.SubFlow(auditFlow{}, input, dex.SubFlowOptions{TaskQueue: dex.DefaultTaskQueue})), nil
//
// Since 1.46.0.
const DefaultTaskQueue = "dex.default"

const (
	maximumTaskQueueNameBytes = 63
	reservedTaskQueuePrefix   = "dex."
)

// validateRegisteredTaskQueueName checks a Worker's task queue; registration has no default alias.
func validateRegisteredTaskQueueName(taskQueue string) error {
	if taskQueue == "" {
		return nil
	}
	if strings.HasPrefix(taskQueue, reservedTaskQueuePrefix) {
		return fmt.Errorf("dex: task queue %q is invalid: names starting with %q are reserved", taskQueue, reservedTaskQueuePrefix)
	}
	return validateTaskQueueCharacters(taskQueue)
}

// validateTargetTaskQueueName checks the task queue of a started Flow or SubFlow.
func validateTargetTaskQueueName(taskQueue string) error {
	if taskQueue == DefaultTaskQueue {
		return nil
	}
	return validateRegisteredTaskQueueName(taskQueue)
}

func validateTaskQueueCharacters(taskQueue string) error {
	if len(taskQueue) > maximumTaskQueueNameBytes {
		return fmt.Errorf("dex: task queue %q is invalid: names have at most %d characters", taskQueue, maximumTaskQueueNameBytes)
	}
	for index := 0; index < len(taskQueue); index++ {
		character := taskQueue[index]
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' ||
			character == '-' || character == '_' || character == '.' {
			continue
		}
		return fmt.Errorf("dex: task queue %q is invalid: names use only A-Z, a-z, 0-9, '.', '_', and '-'", taskQueue)
	}
	return nil
}
