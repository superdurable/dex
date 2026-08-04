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

package io.superdurable.dex.spring.controller;

import io.superdurable.dex.core.ObjectWorkflow;
import io.superdurable.dex.core.Registry;
import org.springframework.stereotype.Component;

import java.util.List;

@Component
public class WorkflowRegistry {
    // NOTE: using static field so that the test doesn't need to run with springboot
    public static final Registry registry = new Registry();

    public WorkflowRegistry(List<ObjectWorkflow> workflows) {
        registry.addWorkflows(workflows);
    }

    public Registry getRegistry() {
        return registry;
    }
}