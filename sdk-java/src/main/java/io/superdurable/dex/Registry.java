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

import io.superdurable.dex.exceptions.FlowDefinitionException;

import java.lang.reflect.Method;
import java.lang.reflect.Modifier;
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

/**
 * Validates and indexes the Flow definitions shared by a client or worker.
 *
 * <p>Construct a registry once after assembling all application Flow instances. Construction fails
 * early for duplicate Flow types, Step types, RPC names, or persistence definition names; invalid
 * start-Step typing; missing definitions; unsupported RPC signatures; unregistered locks or
 * transition targets; and final RPC classes or methods. RPC classes must be non-final so the client
 * can create strongly typed stubs; Kotlin users must mark those classes and methods {@code open}.
 *
 * <pre>{@code
 * Registry registry = new Registry(Arrays.<Flow<?>>asList(
 *         new OrderFlow(),
 *         new BillingFlow()));
 * }</pre>
 */
public final class Registry {
    private final List<Flow<?>> flows;
    private final Map<String, RegisteredFlow> registeredFlows;

    /**
     * Builds and validates a registry from application Flow instances.
     *
     * @param flows the nonnull Flow instances available to clients and workers
     * @throws IllegalArgumentException if the list or any Flow definition violates the registry
     *     contract
     */
    public Registry(final List<Flow<?>> flows) {
        if (flows == null) {
            throw new FlowDefinitionException("Flow definitions are required");
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
            throw new FlowDefinitionException("Flow is not registered: " + flowType);
        }
        return flow;
    }

    RegisteredFlow getFlow(final Class<?> flowClass) {
        for (RegisteredFlow flow : registeredFlows.values()) {
            if (flow.getFlow().getClass() == flowClass) {
                return flow;
            }
        }
        throw new FlowDefinitionException(
                "Flow class is not registered: " + flowClass.getName());
    }

    private static Map<String, RegisteredFlow> assemble(final List<Flow<?>> flows) {
        final Map<String, RegisteredFlow> assembled =
                new LinkedHashMap<String, RegisteredFlow>();
        for (Flow<?> flow : flows) {
            if (flow == null) {
                throw new FlowDefinitionException("Flow definition is null");
            }
            final String flowType = requireDefinitionName("Flow type", flow.getFlowType());
            if (assembled.containsKey(flowType)) {
                throw new FlowDefinitionException("Duplicate Flow: " + flowType);
            }
            assembled.put(flowType, assembleFlow(flowType, flow));
        }
        return assembled;
    }

    private static RegisteredFlow assembleFlow(final String flowType, final Flow<?> flow) {
        final StepList<?> definitions = flow.getSteps();
        if (definitions == null) {
            throw new FlowDefinitionException("Flow " + flowType + " requires Steps");
        }
        final Map<String, RegisteredStep> steps =
                new LinkedHashMap<String, RegisteredStep>();
        RegisteredStep startStep = null;
        for (StepDef definition : definitions.getDefinitions()) {
            if (definition == null || definition.getStep() == null) {
                throw new FlowDefinitionException("Flow " + flowType + " has a null Step");
            }
            final Step<?> step = definition.getStep();
            final String stepType = requireDefinitionName(
                    "Flow " + flowType + " Step type",
                    step.getStepType());
            if (step.getInputType() == null) {
                throw new FlowDefinitionException(
                        "Flow " + flowType + " Step " + stepType + " requires an input type");
            }
            if (steps.containsKey(stepType)) {
                throw new FlowDefinitionException(
                        "Flow " + flowType + " has duplicate Step " + stepType);
            }
            final RegisteredStep registered =
                    new RegisteredStep(stepType, step, definition.isStartStep());
            steps.put(stepType, registered);
            if (definition.isStartStep()) {
                if (startStep != null) {
                    throw new FlowDefinitionException(
                            "Flow " + flowType + " must not have multiple start Steps");
                }
                startStep = registered;
            }
        }
        if (startStep != null) {
            validateStartInputType(flow, startStep.getStep().getInputType());
        }

        final PersistenceSchema schema = flow.getPersistenceSchema();
        if (schema == null) {
            throw new FlowDefinitionException(
                    "Flow " + flowType + " requires a persistence schema");
        }
        final Map<String, PersistenceDefinition> persistence =
                assemblePersistence(flowType, schema);
        final Map<String, RegisteredRpc> rpcs = assembleRpcs(flowType, flow, persistence);
        return new RegisteredFlow(flowType, flow, steps, startStep, rpcs, persistence);
    }

    private static Map<String, PersistenceDefinition> assemblePersistence(
            final String flowType,
            final PersistenceSchema schema) {
        final Map<String, PersistenceDefinition> persistence =
                new LinkedHashMap<String, PersistenceDefinition>();
        for (PersistenceDefinition definition : schema.getAttributes()) {
            addPersistence(flowType, persistence, definition);
        }
        for (PersistenceDefinition definition : schema.getChannels()) {
            addPersistence(flowType, persistence, definition);
        }
        return persistence;
    }

    private static void addPersistence(
            final String flowType,
            final Map<String, PersistenceDefinition> persistence,
            final PersistenceDefinition definition) {
        if (definition == null) {
            throw new FlowDefinitionException(
                    "Flow " + flowType + " has a null persistence definition");
        }
        if (persistence.put(definition.getName(), definition) != null) {
            throw new FlowDefinitionException(
                    "Flow " + flowType + " has duplicate persistence definition "
                            + definition.getName());
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
            validateRpcInterceptability(flow.getClass(), method);
            if (annotation.timeoutSeconds() < 0) {
                throw new FlowDefinitionException(
                        "Flow " + flowType + " RPC " + method.getName()
                                + " timeout must not be negative");
            }
            final String name = requireDefinitionName(
                    "Flow " + flowType + " RPC name",
                    annotation.name().isEmpty() ? method.getName() : annotation.name());
            validateRpcSignature(flowType, method);
            final List<String> locks = validateRpcLocks(
                    flowType,
                    name,
                    annotation,
                    persistence);
            method.setAccessible(true);
            final RegisteredRpc rpc = new RegisteredRpc(name, method, annotation, locks);
            if (rpcs.put(name, rpc) != null) {
                throw new FlowDefinitionException(
                        "Flow " + flowType + " has duplicate RPC " + name);
            }
        }
        return rpcs;
    }

    private static void validateRpcInterceptability(
            final Class<?> flowClass,
            final Method method) {
        if (Modifier.isFinal(flowClass.getModifiers())) {
            throw new FlowDefinitionException(
                    "RPC Flow class must not be final because RPC stubs subclass it. "
                            + "In Kotlin, classes are final by default; declare the Flow class "
                            + "with 'open': " + flowClass.getName());
        }
        if (Modifier.isFinal(method.getModifiers())) {
            throw new FlowDefinitionException(
                    "RPC method must not be final because RPC stubs override it. "
                            + "In Kotlin, methods are final by default; declare the RPC method "
                            + "with 'open': " + flowClass.getName() + "." + method.getName());
        }
    }

    private static List<String> validateRpcLocks(
            final String flowType,
            final String rpcName,
            final RPC annotation,
            final Map<String, PersistenceDefinition> persistence) {
        final List<String> locks = new ArrayList<String>();
        final Set<String> seen = new HashSet<String>();
        for (String name : annotation.lockAttributes()) {
            final PersistenceDefinition definition = persistence.get(name);
            if (!(definition instanceof Attribute)) {
                throw new FlowDefinitionException(
                        "Flow " + flowType + " RPC " + rpcName
                                + " lock Attribute is not registered: " + name);
            }
            addLock(flowType, rpcName, locks, seen, name);
        }
        for (RPCAttributeMapLock lock : annotation.lockAttributeMaps()) {
            final PersistenceDefinition definition = persistence.get(lock.attribute());
            if (!(definition instanceof AttributeMap)) {
                throw new FlowDefinitionException(
                        "Flow " + flowType + " RPC " + rpcName
                                + " lock AttributeMap is not registered: " + lock.attribute());
            }
            addLock(
                    flowType,
                    rpcName,
                    locks,
                    seen,
                    physicalName(lock.attribute(), lock.instance()));
        }
        return Collections.unmodifiableList(locks);
    }

    private static void addLock(
            final String flowType,
            final String rpcName,
            final List<String> locks,
            final Set<String> seen,
            final String lock) {
        if (!seen.add(lock)) {
            throw new FlowDefinitionException(
                    "Flow " + flowType + " RPC " + rpcName
                            + " has duplicate Attribute lock " + lock);
        }
        locks.add(lock);
    }

    private static void validateRpcSignature(
            final String flowType,
            final Method method) {
        final String prefix = "Flow " + flowType + " RPC " + method.getName();
        final Class<?>[] parameters = method.getParameterTypes();
        if (parameters.length < 1 || parameters.length > 2 || parameters[0] != Context.class) {
            throw new FlowDefinitionException(
                    prefix + " must accept Context and optional typed input");
        }
        if (method.getReturnType() != RPCResult.class && method.getReturnType() != Void.TYPE) {
            throw new FlowDefinitionException(prefix + " must return RPCResult<O> or void");
        }
        if (method.getReturnType() == RPCResult.class
                && !(method.getGenericReturnType() instanceof ParameterizedType)) {
            throw new FlowDefinitionException(prefix + " must declare its RPCResult output type");
        }
    }

    private static void validateStartInputType(
            final Flow<?> flow,
            final Class<?> registeredType) {
        final Class<?> inputType = findFlowInputType(flow.getClass());
        if (inputType == null) {
            throw new FlowDefinitionException(
                    "Flow must declare a concrete start input type: "
                            + flow.getClass().getName());
        }
        if (!inputType.isAssignableFrom(registeredType)) {
            throw new FlowDefinitionException(
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
                throw new FlowDefinitionException(
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

    static boolean skipsWaitFor(final Step<?> step) {
        try {
            return step.getClass()
                    .getMethod("waitFor", Context.class, Object.class)
                    .getDeclaringClass() == Step.class;
        } catch (NoSuchMethodException exception) {
            throw new FlowDefinitionException(
                    "Step " + step.getStepType() + " waitFor signature is invalid",
                    exception);
        }
    }

    private static String requireDefinitionName(final String description, final String name) {
        try {
            return Attribute.requireName(name);
        } catch (IllegalArgumentException failure) {
            throw new FlowDefinitionException(description + " is invalid", failure);
        }
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
                throw new FlowDefinitionException(
                        "Flow " + this.name + " Step is not registered: " + name);
            }
            return step;
        }

        RegisteredRpc getRpc(final String name) {
            final RegisteredRpc rpc = rpcs.get(name);
            if (rpc == null) {
                throw new FlowDefinitionException(
                        "Flow " + this.name + " RPC is not registered: " + name);
            }
            return rpc;
        }

        RegisteredRpc getRpcByMethod(final String methodName) {
            for (RegisteredRpc rpc : rpcs.values()) {
                if (rpc.getMethod().getName().equals(methodName)) {
                    return rpc;
                }
            }
            throw new FlowDefinitionException(
                    "Flow " + name + " RPC method is not registered: " + methodName);
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
            this.skipWaitFor = Registry.skipsWaitFor(step);
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
