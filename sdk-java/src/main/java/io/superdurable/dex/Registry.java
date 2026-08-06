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

package io.superdurable.dex;

import com.fasterxml.jackson.core.JsonProcessingException;
import com.fasterxml.jackson.databind.ObjectMapper;

import java.lang.reflect.Method;
import java.lang.reflect.ParameterizedType;
import java.lang.reflect.Type;
import java.util.ArrayList;
import java.util.Collections;
import java.util.HashMap;
import java.util.HashSet;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Set;

public final class Registry {
    private final List<Flow<?>> flows;
    private final Map<String, RegisteredFlow> registeredFlows;

    public Registry(final List<Flow<?>> flows) {
        if (flows == null) {
            throw new IllegalArgumentException("flows are required");
        }
        final Map<String, RegisteredFlow> assembled = assemble(flows);
        this.flows = Collections.unmodifiableList(new ArrayList<Flow<?>>(flows));
        this.registeredFlows = Collections.unmodifiableMap(assembled);
    }

    List<Flow<?>> getFlows() {
        return flows;
    }

    RegisteredFlow getFlow(final String flowType) {
        final RegisteredFlow flow = registeredFlows.get(flowType);
        if (flow == null) {
            throw new IllegalArgumentException("Flow is not registered: " + flowType);
        }
        return flow;
    }

    RegisteredFlow getFlow(final Class<?> flowClass) {
        for (RegisteredFlow flow : registeredFlows.values()) {
            if (flow.getFlow().getClass() == flowClass) {
                return flow;
            }
        }
        throw new IllegalArgumentException("Flow class is not registered: " + flowClass.getName());
    }

    String nativeSpecJson(final ObjectMapper objectMapper) {
        final Map<String, Object> root = new LinkedHashMap<String, Object>();
        final List<Map<String, Object>> flowSpecs = new ArrayList<Map<String, Object>>();
        for (RegisteredFlow flow : registeredFlows.values()) {
            final Map<String, Object> flowSpec = new LinkedHashMap<String, Object>();
            flowSpec.put("name", flow.getName());

            final List<Map<String, Object>> steps = new ArrayList<Map<String, Object>>();
            for (RegisteredStep step : flow.getSteps().values()) {
                final Map<String, Object> stepSpec = new LinkedHashMap<String, Object>();
                stepSpec.put("name", step.getName());
                stepSpec.put("starting", step.isStarting());
                steps.add(stepSpec);
            }
            flowSpec.put("steps", steps);
            flowSpec.put("rpcs", new ArrayList<String>(flow.getRpcs().keySet()));

            final List<Map<String, Object>> persistence =
                    new ArrayList<Map<String, Object>>();
            for (PersistenceDefinition definition : flow.getPersistence().values()) {
                final Map<String, Object> persistenceSpec =
                        new LinkedHashMap<String, Object>();
                persistenceSpec.put("name", definition.getName());
                persistenceSpec.put("kind", persistenceKind(definition));
                persistence.add(persistenceSpec);
            }
            flowSpec.put("persistence", persistence);
            flowSpecs.add(flowSpec);
        }
        root.put("flows", flowSpecs);
        try {
            return objectMapper.writeValueAsString(root);
        } catch (JsonProcessingException exception) {
            throw new IllegalArgumentException("cannot encode Registry specification", exception);
        }
    }

    private static Map<String, RegisteredFlow> assemble(final List<Flow<?>> flows) {
        final Map<String, RegisteredFlow> assembled =
                new LinkedHashMap<String, RegisteredFlow>();
        for (Flow<?> flow : flows) {
            if (flow == null) {
                throw new IllegalArgumentException("Flow definition is null");
            }
            final String flowType = Attribute.requireName(flow.getFlowType());
            if (assembled.containsKey(flowType)) {
                throw new IllegalArgumentException("duplicate Flow " + flowType);
            }
            assembled.put(flowType, assembleFlow(flowType, flow));
        }
        return assembled;
    }

    private static RegisteredFlow assembleFlow(final String flowType, final Flow<?> flow) {
        final StepList<?> definitions = flow.getSteps();
        if (definitions == null) {
            throw new IllegalArgumentException("Flow steps are required");
        }
        final Map<String, RegisteredStep> steps =
                new LinkedHashMap<String, RegisteredStep>();
        RegisteredStep startStep = null;
        for (StepDef definition : definitions.getDefinitions()) {
            if (definition == null || definition.getStep() == null) {
                throw new IllegalArgumentException("Flow " + flowType + " has a null Step");
            }
            final Step<?> step = definition.getStep();
            final String stepType = Attribute.requireName(step.getStepType());
            if (step.getInputType() == null) {
                throw new IllegalArgumentException("Step input type is required: " + stepType);
            }
            if (steps.containsKey(stepType)) {
                throw new IllegalArgumentException("duplicate Step " + stepType);
            }
            final RegisteredStep registered =
                    new RegisteredStep(stepType, step, definition.isStartStep());
            steps.put(stepType, registered);
            if (definition.isStartStep()) {
                if (startStep != null) {
                    throw new IllegalArgumentException("Flow must not have multiple start Steps");
                }
                startStep = registered;
            }
        }
        if (startStep != null) {
            validateStartInputType(flow, startStep.getStep().getInputType());
        }

        final PersistenceSchema schema = flow.getPersistenceSchema();
        if (schema == null) {
            throw new IllegalArgumentException("Flow persistence schema is required");
        }
        final Map<String, PersistenceDefinition> persistence = assemblePersistence(schema);
        final Map<String, RegisteredRpc> rpcs = assembleRpcs(flowType, flow, persistence);
        return new RegisteredFlow(flowType, flow, steps, startStep, rpcs, persistence);
    }

    private static Map<String, PersistenceDefinition> assemblePersistence(
            final PersistenceSchema schema) {
        final Map<String, PersistenceDefinition> persistence =
                new LinkedHashMap<String, PersistenceDefinition>();
        for (PersistenceDefinition definition : schema.getAttributes()) {
            addPersistence(persistence, definition);
        }
        for (PersistenceDefinition definition : schema.getChannels()) {
            addPersistence(persistence, definition);
        }
        return persistence;
    }

    private static void addPersistence(
            final Map<String, PersistenceDefinition> persistence,
            final PersistenceDefinition definition) {
        if (definition == null) {
            throw new IllegalArgumentException("persistence definition is null");
        }
        if (persistence.put(definition.getName(), definition) != null) {
            throw new IllegalArgumentException(
                    "duplicate persistence definition " + definition.getName());
        }
    }

    private static Map<String, RegisteredRpc> assembleRpcs(
            final String flowType,
            final Flow<?> flow,
            final Map<String, PersistenceDefinition> persistence) {
        final Map<String, RegisteredRpc> rpcs = new LinkedHashMap<String, RegisteredRpc>();
        for (Method method : flow.getClass().getMethods()) {
            final RPC annotation = method.getAnnotation(RPC.class);
            if (annotation == null) {
                continue;
            }
            if (annotation.timeoutSeconds() < 0) {
                throw new IllegalArgumentException("RPC timeout must not be negative");
            }
            final String name = Attribute.requireName(
                    annotation.name().isEmpty() ? method.getName() : annotation.name());
            validateRpcSignature(method);
            final List<String> locks = validateRpcLocks(annotation, persistence);
            method.setAccessible(true);
            final RegisteredRpc rpc = new RegisteredRpc(name, method, annotation, locks);
            if (rpcs.put(name, rpc) != null) {
                throw new IllegalArgumentException(
                        "Flow " + flowType + " has duplicate RPC " + name);
            }
        }
        return rpcs;
    }

    private static List<String> validateRpcLocks(
            final RPC annotation,
            final Map<String, PersistenceDefinition> persistence) {
        final List<String> locks = new ArrayList<String>();
        final Set<String> seen = new HashSet<String>();
        for (String name : annotation.lockAttributes()) {
            final PersistenceDefinition definition = persistence.get(name);
            if (!(definition instanceof Attribute)) {
                throw new IllegalArgumentException("RPC lock Attribute is not registered: " + name);
            }
            addLock(locks, seen, name);
        }
        for (RPCAttributeMapLock lock : annotation.lockAttributeMaps()) {
            final PersistenceDefinition definition = persistence.get(lock.attribute());
            if (!(definition instanceof AttributeMap)) {
                throw new IllegalArgumentException(
                        "RPC lock AttributeMap is not registered: " + lock.attribute());
            }
            addLock(locks, seen, physicalName(lock.attribute(), lock.instance()));
        }
        return Collections.unmodifiableList(locks);
    }

    private static void addLock(
            final List<String> locks,
            final Set<String> seen,
            final String lock) {
        if (!seen.add(lock)) {
            throw new IllegalArgumentException("duplicate RPC Attribute lock: " + lock);
        }
        locks.add(lock);
    }

    private static void validateRpcSignature(final Method method) {
        final Class<?>[] parameters = method.getParameterTypes();
        if (parameters.length < 1 || parameters.length > 2 || parameters[0] != Context.class) {
            throw new IllegalArgumentException(
                    "RPC must accept Context and optional typed input: " + method.getName());
        }
        if (method.getReturnType() != RPCResult.class && method.getReturnType() != Void.TYPE) {
            throw new IllegalArgumentException(
                    "RPC must return RPCResult<O> or void: " + method.getName());
        }
        if (method.getReturnType() == RPCResult.class
                && !(method.getGenericReturnType() instanceof ParameterizedType)) {
            throw new IllegalArgumentException(
                    "RPCResult must declare its output type: " + method.getName());
        }
    }

    private static void validateStartInputType(
            final Flow<?> flow,
            final Class<?> registeredType) {
        final Class<?> inputType = findFlowInputType(flow.getClass());
        if (inputType == null) {
            throw new IllegalArgumentException(
                    "Flow must declare a concrete start input type: "
                            + flow.getClass().getName());
        }
        if (!inputType.isAssignableFrom(registeredType)) {
            throw new IllegalArgumentException(
                    "Flow input type " + inputType.getName()
                            + " is not assignable from start Step input type "
                            + registeredType.getName());
        }
    }

    private static Class<?> findFlowInputType(final Class<?> flowClass) {
        for (Type interfaceType : flowClass.getGenericInterfaces()) {
            final Class<?> inputType = findFlowInputType(interfaceType);
            if (inputType != null) {
                return inputType;
            }
        }
        final Type superclass = flowClass.getGenericSuperclass();
        return superclass == null ? null : findFlowInputType(superclass);
    }

    private static Class<?> findFlowInputType(final Type candidate) {
        if (candidate instanceof ParameterizedType) {
            final ParameterizedType parameterized = (ParameterizedType) candidate;
            if (parameterized.getRawType() == Flow.class) {
                final Type input = parameterized.getActualTypeArguments()[0];
                if (input instanceof Class) {
                    return (Class<?>) input;
                }
                throw new IllegalArgumentException(
                        "Flow start input must be a concrete Class: " + input.getTypeName());
            }
            if (parameterized.getRawType() instanceof Class) {
                return findFlowInputType((Class<?>) parameterized.getRawType());
            }
        }
        if (candidate instanceof Class && candidate != Object.class) {
            return findFlowInputType((Class<?>) candidate);
        }
        return null;
    }

    static String physicalName(final String name, final String instance) {
        final String value = Attribute.requireName(instance);
        try {
            return name + "/" + java.net.URLEncoder.encode(value, "UTF-8").replace("+", "%20");
        } catch (java.io.UnsupportedEncodingException impossible) {
            throw new IllegalStateException(impossible);
        }
    }

    private static String persistenceKind(final PersistenceDefinition definition) {
        if (definition instanceof Attribute) {
            return "attribute";
        }
        if (definition instanceof AttributeMap) {
            return "attributeMap";
        }
        if (definition instanceof Channel) {
            return "channel";
        }
        if (definition instanceof ChannelMap) {
            return "channelMap";
        }
        throw new IllegalArgumentException("unsupported persistence definition");
    }

    static final class RegisteredFlow {
        private final String name;
        private final Flow<?> flow;
        private final Map<String, RegisteredStep> steps;
        private final RegisteredStep startStep;
        private final Map<String, RegisteredRpc> rpcs;
        private final Map<String, PersistenceDefinition> persistence;

        RegisteredFlow(
                final String name,
                final Flow<?> flow,
                final Map<String, RegisteredStep> steps,
                final RegisteredStep startStep,
                final Map<String, RegisteredRpc> rpcs,
                final Map<String, PersistenceDefinition> persistence) {
            this.name = name;
            this.flow = flow;
            this.steps = Collections.unmodifiableMap(steps);
            this.startStep = startStep;
            this.rpcs = Collections.unmodifiableMap(rpcs);
            this.persistence = Collections.unmodifiableMap(persistence);
        }

        String getName() { return name; }
        Flow<?> getFlow() { return flow; }
        Map<String, RegisteredStep> getSteps() { return steps; }
        RegisteredStep getStartStep() { return startStep; }
        Map<String, RegisteredRpc> getRpcs() { return rpcs; }
        Map<String, PersistenceDefinition> getPersistence() { return persistence; }

        RegisteredStep getStep(final String name) {
            final RegisteredStep step = steps.get(name);
            if (step == null) {
                throw new IllegalArgumentException("Step is not registered: " + name);
            }
            return step;
        }

        RegisteredRpc getRpc(final String name) {
            final RegisteredRpc rpc = rpcs.get(name);
            if (rpc == null) {
                throw new IllegalArgumentException("RPC is not registered: " + name);
            }
            return rpc;
        }

        RegisteredRpc getRpcByMethod(final String methodName) {
            for (RegisteredRpc rpc : rpcs.values()) {
                if (rpc.getMethod().getName().equals(methodName)) {
                    return rpc;
                }
            }
            throw new IllegalArgumentException("RPC method is not registered: " + methodName);
        }
    }

    static final class RegisteredStep {
        private final String name;
        private final Step<?> step;
        private final boolean starting;
        private final boolean skipWaitFor;

        RegisteredStep(final String name, final Step<?> step, final boolean starting) {
            this.name = name;
            this.step = step;
            this.starting = starting;
            try {
                this.skipWaitFor = step.getClass()
                        .getMethod("waitFor", Context.class, Object.class)
                        .getDeclaringClass() == Step.class;
            } catch (NoSuchMethodException exception) {
                throw new IllegalArgumentException("Step waitFor signature is invalid", exception);
            }
        }

        String getName() { return name; }
        Step<?> getStep() { return step; }
        boolean isStarting() { return starting; }
        boolean skipsWaitFor() { return skipWaitFor; }
    }

    static final class RegisteredRpc {
        private final String name;
        private final Method method;
        private final RPC annotation;
        private final List<String> locks;

        RegisteredRpc(
                final String name,
                final Method method,
                final RPC annotation,
                final List<String> locks) {
            this.name = name;
            this.method = method;
            this.annotation = annotation;
            this.locks = locks;
        }

        String getName() { return name; }
        Method getMethod() { return method; }
        RPC getAnnotation() { return annotation; }
        List<String> getLocks() { return locks; }
    }
}
