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

package io.iworkflow.patterns.workflow.cron;

import io.iworkflow.core.Context;
import io.iworkflow.core.ObjectWorkflow;
import io.iworkflow.core.StateDecision;
import io.iworkflow.core.StateDef;
import io.iworkflow.core.WorkflowState;
import io.iworkflow.core.command.CommandRequest;
import io.iworkflow.core.command.CommandResults;
import io.iworkflow.core.communication.Communication;
import io.iworkflow.core.persistence.Persistence;
import io.iworkflow.core.persistence.PersistenceFieldDef;

import java.util.List;

public class CronScheduleWorkflow implements ObjectWorkflow {
    private final List<StateDef> stateDefs;

    public CronScheduleWorkflow() {
        this.stateDefs = List.of(StateDef.startingState(new CronScheduleState()));
    }

    @Override
    public List<StateDef> getWorkflowStates() {
        return stateDefs;
    }

    @Override
    public List<PersistenceFieldDef> getPersistenceSchema() {
        return List.of();
    }
}

class CronScheduleState implements WorkflowState<Void> {

    public CronScheduleState() {
        // empty constructor
    }

    @Override
    public Class<Void> getInputType() {
        return Void.class;
    }

    /**
     * Wait for either a timeout or an opt-out signal.
     */
    @Override
    public CommandRequest waitUntil(
            final Context context,
            final Void input,
            final Persistence persistence,
            final Communication communication) {
        return CommandRequest.empty;
    }

    /**
     * Executes the state and returns a StateDecision.
     */
    @Override
    public StateDecision execute(
            final Context context,
            final Void input,
            final CommandResults commandResults,
            final Persistence persistence,
            final Communication communication) {
        return StateDecision.gracefulCompleteWorkflow();
    }
}
