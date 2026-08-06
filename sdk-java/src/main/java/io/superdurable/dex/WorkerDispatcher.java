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

import com.google.protobuf.InvalidProtocolBufferException;
import io.superdurable.gen.ChannelCondition;
import io.superdurable.gen.CloseDecision;
import io.superdurable.gen.CloseDecisionType;
import io.superdurable.gen.ConditionCombination;
import io.superdurable.gen.ExecuteMethodFailurePolicy;
import io.superdurable.gen.InvokeExecuteMethodRequest;
import io.superdurable.gen.InvokeExecuteMethodResponse;
import io.superdurable.gen.InvokeWaitForMethodRequest;
import io.superdurable.gen.InvokeWaitForMethodResponse;
import io.superdurable.gen.InvokeWorkerRPCRequest;
import io.superdurable.gen.InvokeWorkerRPCResponse;
import io.superdurable.gen.StepMovement;
import io.superdurable.gen.TimerCondition;
import io.superdurable.gen.WaitForMethodFailurePolicy;
import io.superdurable.gen.WaitingCondition;
import io.superdurable.gen.WaitingConditionType;

import java.lang.reflect.InvocationTargetException;
import java.lang.reflect.Method;
import java.time.Duration;
import java.util.ArrayList;
import java.util.HashSet;
import java.util.IdentityHashMap;
import java.util.List;
import java.util.Map;
import java.util.Set;

final class WorkerDispatcher {
    private static final String INTERNAL_CONDITION_PREFIX = "__dex_internal_condition_";

    private final Registry registry;
    private final ValueMapper values;

    WorkerDispatcher(final Registry registry, final ValueMapper values) {
        this.registry = registry;
        this.values = values;
    }

    byte[] dispatch(final NativeInvocation invocation) {
        try {
            switch (invocation.getKind()) {
                case NativeInvocation.WAIT_FOR:
                    return invokeWaitFor(InvokeWaitForMethodRequest.parseFrom(
                            invocation.getRequest())).toByteArray();
                case NativeInvocation.EXECUTE:
                    return invokeExecute(InvokeExecuteMethodRequest.parseFrom(
                            invocation.getRequest())).toByteArray();
                case NativeInvocation.WORKER_RPC:
                    return invokeRpc(InvokeWorkerRPCRequest.parseFrom(
                            invocation.getRequest())).toByteArray();
                default:
                    throw new IllegalArgumentException("unsupported invocation kind");
            }
        } catch (InvalidProtocolBufferException exception) {
            throw new IllegalArgumentException("invalid Worker request protobuf", exception);
        }
    }

    private InvokeWaitForMethodResponse invokeWaitFor(
            final InvokeWaitForMethodRequest request) {
        final Registry.RegisteredFlow flow = registry.getFlow(request.getFlowType());
        final Registry.RegisteredStep step = flow.getStep(request.getStepType());
        final InvocationContext context = new InvocationContext(
                NativeInvocation.WAIT_FOR,
                flow,
                request.getContext(),
                values,
                request.getAttributesList(),
                null,
                null,
                null);
        final Object input = values.decode(request.getStepInput(), step.getStep().getInputType());
        final Wait wait = callWaitFor(step.getStep(), context, input);

        final InvokeWaitForMethodResponse.Builder response =
                InvokeWaitForMethodResponse.newBuilder()
                        .addAllUpsertAttributes(context.getAttributeWrites())
                        .addAllUpsertStepExeLocals(context.getLocalWrites())
                        .addAllRecordEvents(context.getEvents())
                        .addAllPublishToChannel(context.getPublications());
        final WaitingCondition waiting = mapWait(flow, wait);
        if (waiting != null) {
            response.setWaitingCondition(waiting);
        }
        return response.build();
    }

    private InvokeExecuteMethodResponse invokeExecute(
            final InvokeExecuteMethodRequest request) {
        final Registry.RegisteredFlow flow = registry.getFlow(request.getFlowType());
        final Registry.RegisteredStep step = flow.getStep(request.getStepType());
        final InvocationContext context = new InvocationContext(
                NativeInvocation.EXECUTE,
                flow,
                request.getContext(),
                values,
                request.getAttributesList(),
                request.getStepExeLocalsList(),
                request.hasConditionResults() ? request.getConditionResults() : null,
                null);
        final Object input = values.decode(request.getStepInput(), step.getStep().getInputType());
        final StepDecision decision = callExecute(step.getStep(), context, input);
        return InvokeExecuteMethodResponse.newBuilder()
                .setStepDecision(mapDecision(flow, decision))
                .addAllUpsertAttributes(context.getAttributeWrites())
                .addAllRecordEvents(context.getEvents())
                .addAllUpsertStepExeLocals(context.getLocalWrites())
                .addAllPublishToChannel(context.getPublications())
                .build();
    }

    private InvokeWorkerRPCResponse invokeRpc(final InvokeWorkerRPCRequest request) {
        final Registry.RegisteredFlow flow = registry.getFlow(request.getFlowType());
        final Registry.RegisteredRpc rpc = flow.getRpc(request.getRpcName());
        final InvocationContext context = new InvocationContext(
                NativeInvocation.WORKER_RPC,
                flow,
                request.getContext(),
                values,
                request.getAttributesList(),
                null,
                null,
                request.getChannelInfosMap());
        final Method method = rpc.getMethod();
        final Object[] arguments;
        if (method.getParameterTypes().length == 2) {
            arguments = new Object[] {
                    context,
                    values.decode(request.getInput(), method.getParameterTypes()[1])
            };
        } else {
            arguments = new Object[] {context};
        }
        final Object returned = invoke(flow.getFlow(), method, arguments);
        final InvokeWorkerRPCResponse.Builder response = InvokeWorkerRPCResponse.newBuilder()
                .addAllUpsertAttributes(context.getAttributeWrites())
                .addAllRecordEvents(context.getEvents())
                .addAllPublishToChannel(context.getPublications());
        if (returned instanceof RPCResult) {
            final RPCResult<?> result = (RPCResult<?>) returned;
            response.setOutput(values.encode(result.getOutput()));
            if (!result.getNextSteps().isEmpty()) {
                response.setStepDecision(io.superdurable.gen.StepDecision.newBuilder()
                        .addAllNextSteps(mapMovements(flow, result.getNextSteps())));
            }
        } else {
            response.setOutput(values.encode(null));
        }
        return response.build();
    }

    @SuppressWarnings("unchecked")
    private static Wait callWaitFor(
            final Step<?> step,
            final InvocationContext context,
            final Object input) {
        return ((Step<Object>) step).waitFor(context, input);
    }

    @SuppressWarnings("unchecked")
    private static StepDecision callExecute(
            final Step<?> step,
            final InvocationContext context,
            final Object input) {
        return ((Step<Object>) step).execute(context, input);
    }

    private static Object invoke(
            final Object target,
            final Method method,
            final Object[] arguments) {
        try {
            return method.invoke(target, arguments);
        } catch (IllegalAccessException exception) {
            throw new IllegalStateException("cannot invoke RPC " + method.getName(), exception);
        } catch (InvocationTargetException exception) {
            final Throwable cause = exception.getCause();
            if (cause instanceof RuntimeException) {
                throw (RuntimeException) cause;
            }
            if (cause instanceof Error) {
                throw (Error) cause;
            }
            throw new IllegalStateException("RPC failed: " + method.getName(), cause);
        }
    }

    private WaitingCondition mapWait(
            final Registry.RegisteredFlow flow,
            final Wait wait) {
        if (wait == null) {
            throw new IllegalArgumentException("WaitFor returned null");
        }
        if (wait.getKind() == Wait.Kind.SKIP_IMMEDIATELY) {
            return null;
        }
        final ConditionMapper mapper = new ConditionMapper(flow);
        final WaitingCondition.Builder waiting = WaitingCondition.newBuilder();
        if (wait.getKind() == Wait.Kind.ALL_OF) {
            waiting.setWaitingConditionType(
                    WaitingConditionType.WAITING_CONDITION_TYPE_ALL_COMPLETED);
            addConditions(mapper, wait.getConditions());
        } else if (wait.getKind() == Wait.Kind.ANY_OF) {
            waiting.setWaitingConditionType(
                    WaitingConditionType.WAITING_CONDITION_TYPE_ANY_COMPLETED);
            addConditions(mapper, wait.getConditions());
        } else if (wait.getKind() == Wait.Kind.ANY_COMBINATION_OF) {
            waiting.setWaitingConditionType(
                    WaitingConditionType.WAITING_CONDITION_TYPE_ANY_COMBINATION_COMPLETED);
            for (io.superdurable.dex.ConditionCombination combination
                    : wait.getCombinations()) {
                final ConditionCombination.Builder mapped = ConditionCombination.newBuilder();
                for (Condition condition : combination.getConditions()) {
                    mapped.addConditionIds(mapper.add(condition));
                }
                waiting.addConditionCombinations(mapped);
            }
        } else {
            throw new IllegalArgumentException("unsupported Wait kind");
        }
        waiting.addAllTimerConditions(mapper.timers);
        waiting.addAllChannelConditions(mapper.channels);
        return waiting.build();
    }

    private static void addConditions(
            final ConditionMapper mapper,
            final List<Condition> conditions) {
        if (conditions.isEmpty()) {
            throw new IllegalArgumentException("Wait requires at least one Condition");
        }
        for (Condition condition : conditions) {
            mapper.add(condition);
        }
    }

    private io.superdurable.gen.StepDecision mapDecision(
            final Registry.RegisteredFlow flow,
            final StepDecision decision) {
        if (decision == null) {
            throw new IllegalArgumentException("Execute returned null");
        }
        final io.superdurable.gen.StepDecision.Builder mapped =
                io.superdurable.gen.StepDecision.newBuilder();
        switch (decision.getKind()) {
            case NEXT:
                if (decision.getMovements().isEmpty()) {
                    throw new IllegalArgumentException("goToMulti requires a movement");
                }
                mapped.addAllNextSteps(mapMovements(flow, decision.getMovements()));
                break;
            case GRACEFUL_COMPLETE:
                mapped.setCloseDecision(close(
                        CloseDecisionType.CLOSE_DECISION_TYPE_GRACEFUL_COMPLETE,
                        decision.getOutput(), null));
                break;
            case FORCE_COMPLETE:
                mapped.setCloseDecision(close(
                        CloseDecisionType.CLOSE_DECISION_TYPE_FORCE_COMPLETE,
                        decision.getOutput(), null));
                break;
            case FORCE_FAIL:
                mapped.setCloseDecision(close(
                        CloseDecisionType.CLOSE_DECISION_TYPE_FORCE_FAIL,
                        decision.getReason(), null));
                break;
            case DEAD_END:
                mapped.setCloseDecision(CloseDecision.newBuilder()
                        .setCloseDecisionType(
                                CloseDecisionType.CLOSE_DECISION_TYPE_DEAD_END));
                break;
            case FORCE_COMPLETE_IF_CHANNELS_EMPTY:
                final CloseDecision.Builder conditional = CloseDecision.newBuilder()
                        .setCloseDecisionType(
                                CloseDecisionType
                                        .CLOSE_DECISION_TYPE_FORCE_COMPLETE_ON_CHANNELS_EMPTY)
                        .setCloseInput(values.encode(decision.getOutput()));
                for (Object channel : decision.getEmptyChannels()) {
                    if (!(channel instanceof Channel)) {
                        throw new IllegalArgumentException(
                                "conditional close requires static Channels");
                    }
                    conditional.addConditionalChannelNames(((Channel<?>) channel).getName());
                }
                mapped.setCloseDecision(conditional);
                mapped.addNextSteps(mapMovement(flow, decision.getFallback()));
                break;
            default:
                throw new IllegalArgumentException("unsupported StepDecision kind");
        }
        return mapped.build();
    }

    private CloseDecision close(
            final CloseDecisionType type,
            final Object output,
            final String ignored) {
        final CloseDecision.Builder close = CloseDecision.newBuilder()
                .setCloseDecisionType(type);
        if (type == CloseDecisionType.CLOSE_DECISION_TYPE_FORCE_FAIL) {
            close.setCloseInput(values.encode(output));
        } else if (type != CloseDecisionType.CLOSE_DECISION_TYPE_DEAD_END) {
            close.setCloseInput(values.encode(output));
        }
        return close.build();
    }

    private List<StepMovement> mapMovements(
            final Registry.RegisteredFlow flow,
            final List<io.superdurable.dex.StepMovement<?>> movements) {
        final List<StepMovement> mapped = new ArrayList<StepMovement>(movements.size());
        for (io.superdurable.dex.StepMovement<?> movement : movements) {
            mapped.add(mapMovement(flow, movement));
        }
        return mapped;
    }

    private StepMovement mapMovement(
            final Registry.RegisteredFlow flow,
            final io.superdurable.dex.StepMovement<?> movement) {
        if (movement == null) {
            throw new IllegalArgumentException("Step movement is required");
        }
        final Registry.RegisteredStep target = flow.getStep(movement.getStep().getStepType());
        if (target.getStep() != movement.getStep()) {
            throw new IllegalArgumentException("Step movement target does not belong to Flow");
        }
        final StepMovement.Builder mapped = StepMovement.newBuilder()
                .setStepType(target.getName())
                .setStepInput(values.encode(movement.getInput()));
        final io.superdurable.gen.StepOptions options = mapStepOptions(
                movement.getOptions() == null
                        ? target.getStep().getStepOptions()
                        : movement.getOptions());
        if (options != null || target.skipsWaitFor()) {
            final io.superdurable.gen.StepOptions.Builder mappedOptions = options == null
                    ? io.superdurable.gen.StepOptions.newBuilder()
                    : options.toBuilder();
            mappedOptions.setSkipWaitFor(target.skipsWaitFor());
            mapped.setStepOptions(mappedOptions);
        }
        return mapped.build();
    }

    io.superdurable.gen.StepOptions mapStepOptions(final StepOptions options) {
        if (options == null) {
            return null;
        }
        final io.superdurable.gen.StepOptions.Builder mapped =
                io.superdurable.gen.StepOptions.newBuilder();
        if (options.getWaitForMethodTimeout() != null) {
            mapped.setWaitForTimeoutSeconds(seconds32(options.getWaitForMethodTimeout()));
        }
        if (options.getExecuteMethodTimeout() != null) {
            mapped.setExecuteTimeoutSeconds(seconds32(options.getExecuteMethodTimeout()));
        }
        if (options.getWaitForRetry() != null) {
            mapped.setWaitForRetryPolicy(mapRetry(options.getWaitForRetry()));
        }
        if (options.getExecuteRetry() != null) {
            mapped.setExecuteRetryPolicy(mapRetry(options.getExecuteRetry()));
        }
        mapped.setWaitForFailurePolicy(options.getWaitForFailure()
                == WaitForFailurePolicy.PROCEED
                ? WaitForMethodFailurePolicy
                        .WAIT_FOR_METHOD_FAILURE_POLICY_PROCEED_ON_FAILURE
                : WaitForMethodFailurePolicy
                        .WAIT_FOR_METHOD_FAILURE_POLICY_FAIL_FLOW_ON_FAILURE);
        if (options.getExecuteFailureTarget() != null) {
            final StepOptions.ExecuteFailureTarget target = options.getExecuteFailureTarget();
            mapped.setExecuteFailurePolicy(ExecuteMethodFailurePolicy
                    .EXECUTE_METHOD_FAILURE_POLICY_PROCEED_TO_CONFIGURED_STEP)
                    .setExecuteFailureProceedStepType(target.getStep().getStepType());
            final io.superdurable.gen.StepOptions targetOptions =
                    mapStepOptions(target.getOptions() == null
                            ? target.getStep().getStepOptions()
                            : target.getOptions());
            if (targetOptions != null || Registry.skipsWaitFor(target.getStep())) {
                final io.superdurable.gen.StepOptions.Builder mappedTarget =
                        targetOptions == null
                                ? io.superdurable.gen.StepOptions.newBuilder()
                                : targetOptions.toBuilder();
                mappedTarget.setSkipWaitFor(Registry.skipsWaitFor(target.getStep()));
                mapped.setExecuteFailureProceedStepOptions(mappedTarget);
            }
        }
        mapped.setWaitForDurabilityOverride(mapDurability(options.getWaitForDurability()));
        mapped.setExecuteDurabilityOverride(mapDurability(options.getExecuteDurability()));
        for (AttributeLock lock : options.getWaitForLocks()) {
            mapped.addWaitForLockAttributeKeys(mapLock(lock));
        }
        for (AttributeLock lock : options.getExecuteLocks()) {
            mapped.addExecuteLockAttributeKeys(mapLock(lock));
        }
        return mapped.build();
    }

    private static io.superdurable.gen.RetryPolicy mapRetry(final RetryPolicy retry) {
        final io.superdurable.gen.RetryPolicy.Builder mapped =
                io.superdurable.gen.RetryPolicy.newBuilder()
                        .setBackoffCoefficient((float) retry.getBackoffCoefficient())
                        .setMaximumAttempts(retry.getMaximumAttempts());
        if (retry.getInitialInterval() != null) {
            mapped.setInitialIntervalSeconds(seconds32(retry.getInitialInterval()));
        }
        if (retry.getMaximumInterval() != null) {
            mapped.setMaximumIntervalSeconds(seconds32(retry.getMaximumInterval()));
        }
        if (retry.getTotalDuration() != null) {
            mapped.setTotalDurationSeconds(seconds32(retry.getTotalDuration()));
        }
        return mapped.build();
    }

    private static io.superdurable.gen.StepDurability mapDurability(
            final StepDurability durability) {
        if (durability == StepDurability.SYNC) {
            return io.superdurable.gen.StepDurability.STEP_DURABILITY_SYNC;
        }
        if (durability == StepDurability.ASYNC) {
            return io.superdurable.gen.StepDurability.STEP_DURABILITY_ASYNC;
        }
        return io.superdurable.gen.StepDurability.STEP_DURABILITY_UNSPECIFIED;
    }

    private static String mapLock(final AttributeLock lock) {
        return lock.getInstance() == null
                ? lock.getAttribute()
                : Registry.physicalName(lock.getAttribute(), lock.getInstance());
    }

    private static int seconds32(final Duration duration) {
        if (duration.isNegative() || duration.getNano() != 0
                || duration.getSeconds() > Integer.MAX_VALUE) {
            throw new IllegalArgumentException(
                    "Duration must be non-negative whole seconds within int32");
        }
        return (int) duration.getSeconds();
    }

    private final class ConditionMapper {
        private final Registry.RegisteredFlow flow;
        private final Map<Condition, String> ids =
                new IdentityHashMap<Condition, String>();
        private final Set<String> used = new HashSet<String>();
        private final List<TimerCondition> timers = new ArrayList<TimerCondition>();
        private final List<ChannelCondition> channels = new ArrayList<ChannelCondition>();
        private int nextId;

        ConditionMapper(final Registry.RegisteredFlow flow) {
            this.flow = flow;
        }

        String add(final Condition condition) {
            if (condition == null) {
                throw new IllegalArgumentException("Condition is required");
            }
            if (ids.containsKey(condition)) {
                return ids.get(condition);
            }
            final String id = condition.getConditionId() == null
                    ? INTERNAL_CONDITION_PREFIX + nextId++
                    : condition.getConditionId();
            if (id.isEmpty() || !used.add(id)) {
                throw new IllegalArgumentException("duplicate or empty Condition ID");
            }
            if (condition.getKind() == Condition.Kind.TIMER) {
                timers.add(TimerCondition.newBuilder()
                        .setConditionId(id)
                        .setDurationSeconds(condition.getDuration().getSeconds())
                        .build());
            } else {
                final io.superdurable.dex.PersistenceDefinition definition =
                        flow.getPersistence().get(condition.getChannelName());
                if (!(definition instanceof Channel) && !(definition instanceof ChannelMap)) {
                    throw new IllegalArgumentException(
                            "Channel is not registered: " + condition.getChannelName());
                }
                final String channelName = definition instanceof ChannelMap
                        ? Registry.physicalName(
                                condition.getChannelName(), condition.getInstance())
                        : condition.getChannelName();
                final ChannelCondition.Builder channel = ChannelCondition.newBuilder()
                        .setConditionId(id)
                        .setChannelName(channelName);
                if (condition.getAtLeast() != null) {
                    channel.setAtLeast(condition.getAtLeast());
                }
                if (condition.getAtMost() != null) {
                    channel.setAtMost(condition.getAtMost());
                }
                channels.add(channel.build());
            }
            ids.put(condition, id);
            return id;
        }
    }
}
