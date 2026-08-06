/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Super Durable Source License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Super-Durable-1.0
 */

package io.superdurable.dex;

import java.lang.reflect.Method;
import java.lang.reflect.ParameterizedType;
import java.lang.reflect.Type;
import java.util.ArrayList;
import java.util.Collections;
import java.util.HashSet;
import java.util.List;
import java.util.Set;

public final class Registry {
    private final List<Flow<?>> flows;

    public Registry(final List<Flow<?>> flows) {
        if (flows == null) {
            throw new IllegalArgumentException("flows are required");
        }
        validate(flows);
        this.flows = Collections.unmodifiableList(new ArrayList<Flow<?>>(flows));
    }

    List<Flow<?>> getFlows() {
        return flows;
    }

    private static void validate(final List<Flow<?>> flows) {
        final Set<String> flowNames = new HashSet<String>();
        for (Flow<?> flow : flows) {
            if (flow == null || !flowNames.add(Attribute.requireName(flow.getFlowType()))) {
                throw new IllegalArgumentException("duplicate or null Flow definition");
            }
            validateFlow(flow);
        }
    }

    private static void validateFlow(final Flow<?> flow) {
        final Set<String> stepNames = new HashSet<String>();
        if (flow.getSteps() == null) {
            throw new IllegalArgumentException("Flow steps are required");
        }
        boolean hasStartStep = false;
        for (StepDef definition : flow.getSteps()) {
            if (definition == null) {
                throw new IllegalArgumentException(
                        "Flow " + flow.getFlowType() + " has a null Step definition");
            }
            final Step<?> step = definition.getStep();
            if (!stepNames.add(Attribute.requireName(step.getStepType()))) {
                throw new IllegalArgumentException(
                        "Flow " + flow.getFlowType() + " has a duplicate Step");
            }
            if (step.getInputType() == null) {
                throw new IllegalArgumentException("Step input type is required");
            }
            if (definition.isStartStep()) {
                if (hasStartStep) {
                    throw new IllegalArgumentException("Flow must not have multiple start Steps");
                }
                hasStartStep = true;
            }
        }
        if (flow.getPersistenceSchema() == null) {
            throw new IllegalArgumentException("Flow persistence schema is required");
        }
        validateRPCs(flow);
    }

    private static void validateRPCs(final Flow<?> flow) {
        final Set<String> attributeNames = persistenceAttributeNames(flow.getPersistenceSchema());
        final Set<String> attributeMapNames =
                persistenceAttributeMapNames(flow.getPersistenceSchema());
        final Set<String> rpcNames = new HashSet<String>();
        for (Method method : flow.getClass().getMethods()) {
            final RPC annotation = method.getAnnotation(RPC.class);
            if (annotation == null) {
                continue;
            }
            if (annotation.timeoutSeconds() < 0) {
                throw new IllegalArgumentException("RPC timeout must not be negative");
            }
            final String durableName = annotation.name().isEmpty()
                    ? method.getName()
                    : annotation.name();
            Attribute.requireName(durableName);
            if (!rpcNames.add(durableName)) {
                throw new IllegalArgumentException("duplicate RPC " + durableName);
            }
            validateRPCSignature(method);
            validateRPCLocks(annotation, attributeNames, attributeMapNames);
        }
    }

    private static Set<String> persistenceAttributeNames(final PersistenceSchema schema) {
        final Set<String> names = new HashSet<String>();
        for (PersistenceDefinition definition : schema.getAttributes()) {
            if (definition instanceof Attribute) {
                names.add(definition.getName());
            }
        }
        return names;
    }

    private static Set<String> persistenceAttributeMapNames(final PersistenceSchema schema) {
        final Set<String> names = new HashSet<String>();
        for (PersistenceDefinition definition : schema.getAttributes()) {
            if (definition instanceof AttributeMap) {
                names.add(definition.getName());
            }
        }
        return names;
    }

    private static void validateRPCSignature(final Method method) {
        final Class<?>[] parameters = method.getParameterTypes();
        if (parameters.length < 1
                || parameters.length > 2
                || parameters[0] != Context.class) {
            throw new IllegalArgumentException(
                    "RPC must accept Context and optional typed input: " + method.getName());
        }
        final boolean function = method.getReturnType() == RPCResult.class;
        final boolean procedure = method.getReturnType() == Void.TYPE;
        if (!function && !procedure) {
            throw new IllegalArgumentException(
                    "RPC must return RPCResult<O> or void: " + method.getName());
        }
        if (function) {
            validateRPCOutputType(method);
        }
    }

    private static void validateRPCOutputType(final Method method) {
        final Type returnType = method.getGenericReturnType();
        if (!(returnType instanceof ParameterizedType)) {
            throw new IllegalArgumentException(
                    "RPCResult must declare its output type: " + method.getName());
        }
        final Type[] arguments = ((ParameterizedType) returnType).getActualTypeArguments();
        if (arguments.length != 1) {
            throw new IllegalArgumentException(
                    "RPCResult must declare one output type: " + method.getName());
        }
    }

    private static void validateRPCLocks(
            final RPC annotation,
            final Set<String> attributeNames,
            final Set<String> attributeMapNames) {
        final Set<String> locks = new HashSet<String>();
        for (String name : annotation.lockAttributes()) {
            validateLock(name, null, attributeNames, locks);
        }
        for (RPCAttributeMapLock lock : annotation.lockAttributeMaps()) {
            validateLock(lock.attribute(), lock.instance(), attributeMapNames, locks);
        }
    }

    private static void validateLock(
            final String attribute,
            final String instance,
            final Set<String> attributeNames,
            final Set<String> locks) {
        if (!attributeNames.contains(attribute)) {
            throw new IllegalArgumentException("RPC lock attribute is not registered: " + attribute);
        }
        if (instance != null && instance.trim().isEmpty()) {
            throw new IllegalArgumentException("RPC attribute-map lock instance is required");
        }
        final String identity = instance == null ? attribute : attribute + "\u0000" + instance;
        if (!locks.add(identity)) {
            throw new IllegalArgumentException("duplicate RPC attribute lock: " + attribute);
        }
    }
}
