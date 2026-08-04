/*
 * Legacy Materials in this file remain under their original licenses.
 * See LEGACY_NOTICES.md.
 */

/*
 * Modifications Copyright (c) 2026 Super Durable, Inc.
 *
 * Modifications after the Legacy Cutoff are licensed under the
 * Super Durable Source License 1.0.
 * Legacy Materials remain under their original licenses.
 * See LICENSE and LEGACY_NOTICES.md.
 */

package io.superdurable.dex.core;

import io.superdurable.dex.core.communication.CommunicationMethodDef;
import io.superdurable.dex.core.persistence.PersistenceFieldDef;
import io.superdurable.dex.core.persistence.PersistenceOptions;

import java.util.ArrayList;
import java.util.Collections;
import java.util.List;

/**
 * This is the interface to define an object workflow definition.
 * ObjectWorkflow is a top level concept in Dex. Any object that is long-lasting(at least a few seconds) can be modeled as an "ObjectWorkflow".
 */
public interface ObjectWorkflow {
    /**
     * defines the states of the workflow. A state represents a step of the workflow state machine.
     * A state can execute some commands (signal/timer) and wait for result
     * See more details in the {@link WorkflowState} definition.
     * By default, this return an empty list, meaning no states.
     * There can be at most one startingState in the list.
     * If there is no startingState or with the default empty state list, the workflow
     * will not start any state execution after workflow stated. However, application code can still
     * use RPC to invoke new state execution in the future.
     *
     * @return all the state definitions
     */
    default List<StateDef> getWorkflowStates() {
        return new ArrayList<>();
    }

    /**
     * defines all the persistence fields for this workflow, this includes:
     * 1. Data attributes
     * 2. Search attributes
     * <p>
     * Data attributes can be read/upsert in WorkflowState start/decide API
     * Data attributes  can also be read by RPC/GetDataAttributes API by external applications using {@link Client}
     * <p>
     * Search attributes can be read/upsert in WorkflowState start/decide API
     * Search attributes can also be read by RPC/GetSearchAttributes Client API by external applications
     * External applications can also use "SearchWorkflow" API to find workflows by SQL-like query
     *
     * @return the persistence schema
     */
    default List<PersistenceFieldDef> getPersistenceSchema() {
        return Collections.emptyList();
    }

    default PersistenceOptions getPersistenceOptions() {
        return PersistenceOptions.getDefault();
    }

    /**
     * defines all the communication methods for this workflow, this includes
     * 1. Signal channel
     * 2. Interstate channel
     * <p>
     * Signal channel is for external applications to send signal to workflow execution.
     * Workflow execution can listen on the signal in the WorkflowState start API and receive in
     * the WorkflowState decide API
     * <p>
     * InternalChannel is for synchronization communications within the workflow execution internally.
     * E.g. WorkflowStateA will continue after receiving a value from WorkflowStateB, or from an RPC
     *
     * @return the communication schema
     */
    default List<CommunicationMethodDef> getCommunicationSchema() {
        return Collections.emptyList();
    }

    /**
     * Define the workflowType of this workflow definition. By default, it's the simple name of the workflow instance,
     * which should be the case for most scenarios.
     * <p>
     * Implement this to provide a different workflowType than default to avoid breaking changes when changing workflow class name.
     * However, {@link Client} must then use String to provide workflow type to start workflows.
     *
     * @return the workflow type
     */
    default String getWorkflowType() {
        return this.getClass().getSimpleName();
    }
}

