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

package io.superdurable.dex.core.exceptions;

import io.superdurable.dex.core.ClientSideException;

/**
 * A friendly named exception to indicate that the workflow does not exist or exists but not running.
 * It's the same as {@link WorkflowNotExistsException} but with a different name.
 * It's subclass of {@link ClientSideException} with ErrorSubStatus.WORKFLOW_NOT_EXISTS_SUB_STATUS
 */
public class NoRunningWorkflowException extends WorkflowNotExistsException {
    public NoRunningWorkflowException(
            final ClientSideException exception) {
        super(exception);
    }
}
