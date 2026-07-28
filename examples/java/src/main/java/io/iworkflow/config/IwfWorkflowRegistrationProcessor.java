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

package io.iworkflow.config;

import io.iworkflow.core.ObjectWorkflow;
import io.iworkflow.core.Registry;
import org.springframework.beans.factory.SmartInitializingSingleton;
import org.springframework.stereotype.Component;

import java.util.List;

@Component
public class IwfWorkflowRegistrationProcessor implements SmartInitializingSingleton {
    private final Registry workflowRegistry;
    private final List<ObjectWorkflow> workflows;

    public IwfWorkflowRegistrationProcessor(final Registry workflowRegistry, final List<ObjectWorkflow> workflows) {
        this.workflowRegistry = workflowRegistry;
        this.workflows = workflows;
    }

    @Override
    public void afterSingletonsInstantiated() {
        workflows.forEach(workflowRegistry::addWorkflow);
    }
}