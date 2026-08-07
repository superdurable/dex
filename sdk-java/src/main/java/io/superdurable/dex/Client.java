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

import com.google.protobuf.Empty;
import com.google.protobuf.InvalidProtocolBufferException;
import com.google.protobuf.Timestamp;
import io.grpc.ManagedChannel;
import io.grpc.ManagedChannelBuilder;
import io.grpc.Status;
import io.grpc.StatusRuntimeException;
import io.grpc.protobuf.StatusProto;
import io.superdurable.gen.AttributeWrite;
import io.superdurable.gen.ErrorResponse;
import io.superdurable.gen.FlowAlreadyStartedOptions;
import io.superdurable.gen.FlowExecutionID;
import io.superdurable.gen.FlowResetType;
import io.superdurable.gen.FlowServiceGrpc;
import io.superdurable.gen.FlowStartOptions;
import io.superdurable.gen.GetAttributesRequest;
import io.superdurable.gen.GetAttributesResponse;
import io.superdurable.gen.GetFlowSummaryRequest;
import io.superdurable.gen.GetFlowSummaryResponse;
import io.superdurable.gen.InvokeRPCRequest;
import io.superdurable.gen.KV;
import io.superdurable.gen.PublishToChannelRequest;
import io.superdurable.gen.ResetFlowRequest;
import io.superdurable.gen.SetAttributesRequest;
import io.superdurable.gen.SkipTimerRequest;
import io.superdurable.gen.StartFlowRequest;
import io.superdurable.gen.StopFlowRequest;
import io.superdurable.gen.TriggerContinueAsNewRequest;
import io.superdurable.gen.UpdateFlowConfigRequest;
import io.superdurable.gen.WaitForFlowRequest;
import io.superdurable.gen.WaitForFlowResponse;
import io.superdurable.gen.WaitForStepCompletionRequest;
import net.bytebuddy.ByteBuddy;
import net.bytebuddy.dynamic.loading.ClassLoadingStrategy;
import net.bytebuddy.dynamic.scaffold.subclass.ConstructorStrategy;
import net.bytebuddy.implementation.InvocationHandlerAdapter;
import net.bytebuddy.matcher.ElementMatchers;
import org.objenesis.ObjenesisStd;

import java.lang.reflect.InvocationHandler;
import java.lang.reflect.Method;
import java.lang.reflect.Modifier;
import java.lang.reflect.ParameterizedType;
import java.lang.reflect.Type;
import java.time.Duration;
import java.time.Instant;
import java.util.ArrayList;
import java.util.Collections;
import java.util.List;
import java.util.UUID;
import java.util.concurrent.TimeUnit;

public final class Client implements AutoCloseable {
    private final Registry registry;
    private final BlobCache blobCache;
    private final ClientOptions options;
    private final ManagedChannel channel;
    private final FlowServiceGrpc.FlowServiceBlockingStub service;
    private final ValueMapper values;
    private final ValueHydrator hydrator;
    private final WorkerDispatcher mappings;

    public Client(final Registry registry, final BlobCache blobCache) {
        this(registry, blobCache, new ClientOptions());
    }

    public Client(
            final Registry registry,
            final BlobCache blobCache,
            final ClientOptions options) {
        if (registry == null || blobCache == null || options == null) {
            throw new IllegalArgumentException("registry, blobCache, and options are required");
        }
        this.registry = registry;
        this.blobCache = blobCache;
        this.options = options;
        this.values = new ValueMapper(options.getObjectMapper());
        this.channel = ManagedChannelBuilder.forTarget(options.getServerAddress())
                .usePlaintext()
                .build();
        this.service = FlowServiceGrpc.newBlockingStub(channel);
        this.hydrator = new ValueHydrator(service, blobCache);
        this.mappings = new WorkerDispatcher(registry, values, hydrator);
    }

    Registry getRegistry() {
        return registry;
    }

    BlobCache getBlobCache() {
        return blobCache;
    }

    ClientOptions getOptions() {
        return options;
    }

    public <I> String startFlow(
            final Flow<I> flow,
            final String flowId,
            final I input) {
        return startFlow(flow, flowId, input, new StartFlowOptions());
    }

    public <I> String startFlow(
            final Flow<I> flow,
            final String flowId,
            final I input,
            final StartFlowOptions startOptions) {
        final Registry.RegisteredFlow registered = registry.getFlow(flow.getFlowType());
        if (registered.getFlow() != flow) {
            throw new IllegalArgumentException("Flow instance is not registered");
        }
        final StartFlowRequest.Builder request = StartFlowRequest.newBuilder()
                .setFlowId(Attribute.requireName(flowId))
                .setFlowType(registered.getName())
                .setRequestId(startOptions.getRequestId() == null
                        ? UUID.randomUUID().toString()
                        : startOptions.getRequestId())
                .setFlowStartOptions(mapStartOptions(startOptions));
        if (registered.getStartStep() != null) {
            final Class<?> inputType = registered.getStartStep().getStep().getInputType();
            if (input != null && !inputType.isInstance(input)) {
                throw new IllegalArgumentException(
                        "Flow input must be " + inputType.getName()
                                + ", got " + input.getClass().getName());
            }
            request.setStartStepType(registered.getStartStep().getName())
                    .setStepInput(values.encode(input));
            final io.superdurable.gen.StepOptions stepOptions = mappings.mapStepOptions(
                    registered.getStartStep().getStep().getStepOptions());
            final io.superdurable.gen.StepOptions.Builder mappedStep = stepOptions == null
                    ? io.superdurable.gen.StepOptions.newBuilder()
                    : stepOptions.toBuilder();
            mappedStep.setSkipWaitFor(registered.getStartStep().skipsWaitFor());
            request.setStepOptions(mappedStep);
        } else if (input != null) {
            throw new IllegalArgumentException("Flow without a start Step requires null input");
        }
        if (startOptions.getTimeout() != null) {
            request.setFlowTimeoutSeconds(seconds32(startOptions.getTimeout()));
        }
        return call(() -> service.startFlow(request.build())).getRunId();
    }

    public <T> T newRpcStub(final Class<T> rpcClass, final String flowId) {
        return newRpcStub(rpcClass, flowId, "");
    }

    public <T> T newRpcStub(
            final Class<T> rpcClass,
            final String flowId,
            final String runId) {
        final Registry.RegisteredFlow flow = registry.getFlow(rpcClass);
        validateRpcStubClass(rpcClass, flow);
        try {
            final Class<? extends T> stubClass = new ByteBuddy()
                    .subclass(rpcClass, ConstructorStrategy.Default.NO_CONSTRUCTORS)
                    .method(ElementMatchers.isAnnotatedWith(RPC.class))
                    .intercept(InvocationHandlerAdapter.of(
                            new RpcInvocationHandler(new RpcTarget(flow, flowId, runId))))
                    .make()
                    .load(rpcClass.getClassLoader(), ClassLoadingStrategy.Default.INJECTION)
                    .getLoaded();
            return new ObjenesisStd().newInstance(stubClass);
        } catch (RuntimeException exception) {
            throw new IllegalArgumentException(
                    "RPC stub could not subclass " + rpcClass.getName(), exception);
        }
    }

    private static void validateRpcStubClass(
            final Class<?> rpcClass,
            final Registry.RegisteredFlow flow) {
        if (Modifier.isFinal(rpcClass.getModifiers())) {
            throw new IllegalArgumentException(
                    "RPC stub Flow class must not be final: " + rpcClass.getName());
        }
        for (Registry.RegisteredRpc rpc : flow.getRpcs().values()) {
            if (Modifier.isFinal(rpc.getMethod().getModifiers())) {
                throw new IllegalArgumentException(
                        "RPC stub method must not be final: " + rpc.getMethod().getName());
            }
        }
    }

    public <I, O> O invokeRPC(
            final RpcDefinitions.RpcFunc1<I, O> rpcStubMethod,
            final I input) {
        return rpcStubMethod.execute(null, input).getOutput();
    }

    public <O> O invokeRPC(final RpcDefinitions.RpcFunc0<O> rpcStubMethod) {
        return rpcStubMethod.execute(null).getOutput();
    }

    public <I> void invokeRPC(
            final RpcDefinitions.RpcProc1<I> rpcStubMethod,
            final I input) {
        rpcStubMethod.execute(null, input);
    }

    public void invokeRPC(final RpcDefinitions.RpcProc0 rpcStubMethod) {
        rpcStubMethod.execute(null);
    }

    public <T> T getAttribute(
            final String flowId,
            final String runId,
            final Attribute<T> attribute) {
        return getAttributeValue(flowId, runId, attribute, null, attribute.getValueType());
    }

    public <T> T getAttribute(final String flowId, final Attribute<T> attribute) {
        return getAttribute(flowId, "", attribute);
    }

    public <T> T getAttribute(
            final String flowId,
            final AttributeMap<T> attribute,
            final String instance) {
        return getAttributeValue(flowId, "", attribute, instance, attribute.getValueType());
    }

    public <T> void setAttribute(
            final String flowId,
            final String runId,
            final Attribute<T> attribute,
            final T value) {
        setAttributeValue(flowId, runId, attribute, null, value, attribute.getIndex());
    }

    public <T> void setAttribute(
            final String flowId,
            final Attribute<T> attribute,
            final T value) {
        setAttribute(flowId, "", attribute, value);
    }

    public <T> void setAttribute(
            final String flowId,
            final AttributeMap<T> attribute,
            final String instance,
            final T value) {
        setAttributeValue(flowId, "", attribute, instance, value, attribute.getIndex());
    }

    public <T> void publish(
            final String flowId,
            final String runId,
            final Channel<T> channel,
            final T value) {
        publishValues(flowId, runId, channel.getName(), Collections.singletonList(value));
    }

    @SafeVarargs
    public final <T> void publish(
            final String flowId,
            final Channel<T> channel,
            final T... values) {
        publishValues(flowId, "", channel.getName(), java.util.Arrays.asList(values));
    }

    @SafeVarargs
    public final <T> void publish(
            final String flowId,
            final ChannelMap<T> channel,
            final String instance,
            final T... values) {
        publishValues(
                flowId,
                "",
                Registry.physicalName(channel.getName(), instance),
                java.util.Arrays.asList(values));
    }

    public <T> void publish(
            final String flowId,
            final Channel<T> channel,
            final List<T> values) {
        publishValues(flowId, "", channel.getName(), values);
    }

    public void stopFlow(final String flowId) {
        stopFlow(flowId, new StopFlowOptions());
    }

    public void stopFlow(final String flowId, final StopFlowOptions stopOptions) {
        call(() -> service.stopFlow(StopFlowRequest.newBuilder()
                .setFlowId(flowId)
                .setReason(stopOptions.getReason() == null ? "" : stopOptions.getReason())
                .setStopType(mapStopType(stopOptions.getType()))
                .build()));
    }

    public void waitForFlow(final String flowId) {
        waitForFlowResponse(flowId, null);
    }

    public <O> O waitForFlow(final String flowId, final Class<O> outputType) {
        return waitForFlow(flowId, outputType, null);
    }

    public <O> O waitForFlow(
            final String flowId,
            final Class<O> outputType,
            final Duration timeout) {
        final WaitForFlowResponse response = waitForFlowResponse(flowId, timeout);
        for (int index = response.getResultsCount() - 1; index >= 0; index--) {
            if (response.getResults(index).hasCompletedStepOutput()) {
                return values.decode(
                        hydrator.hydrate(response.getResults(index).getCompletedStepOutput()),
                        outputType);
            }
        }
        return null;
    }

    public FlowInfo describeFlow(final String flowId) {
        final GetFlowSummaryResponse response = call(() -> service.getFlowSummary(
                GetFlowSummaryRequest.newBuilder().setFlowId(flowId).build()));
        final FlowExecutionID execution = response.getFlowExecutionId();
        return new FlowInfo(
                execution.getFlowId(),
                execution.getRunId(),
                response.getFlowType(),
                mapFlowStatus(response.getFlowStatus()),
                instant(response.getStartTime()));
    }

    public String resetFlow(final String flowId, final ResetFlowOptions options) {
        final ResetFlowRequest.Builder request = ResetFlowRequest.newBuilder()
                .setFlowId(flowId)
                .setResetType(mapResetType(options.getType()))
                .setReason(options.getReason() == null ? "" : options.getReason())
                .setSkipChannelMessagesReapply(options.isSkipChannelMessagesReapply())
                .setSkipLockingRpcReapply(options.isSkipLockingRpcReapply());
        if (options.getHistoryEventId() != null) {
            request.setHistoryEventId(Math.toIntExact(options.getHistoryEventId()));
        }
        if (options.getHistoryEventTime() != null) {
            request.setHistoryEventTime(options.getHistoryEventTime().toString());
        }
        if (options.getStepType() != null) {
            request.setStepType(options.getStepType());
        }
        if (options.getStepExecutionId() != null) {
            request.setStepExecutionId(options.getStepExecutionId());
        }
        return call(() -> service.resetFlow(request.build())).getRunId();
    }

    public void skipTimer(
            final String flowId,
            final StepExecutionId stepExecutionId,
            final TimerId timerId) {
        final SkipTimerRequest.Builder request = SkipTimerRequest.newBuilder()
                .setFlowId(flowId)
                .setStepExecutionId(stepExecutionId.getStepType()
                        + "-" + stepExecutionId.getExecutionNumber());
        if (timerId.getConditionId() != null) {
            request.setTimerConditionId(timerId.getConditionId());
        }
        if (timerId.getIndex() != null) {
            request.setTimerConditionIndex(timerId.getIndex());
        }
        call(() -> service.skipTimer(request.build()));
    }

    public void waitForStepCompletion(
            final String flowId,
            final StepExecutionId stepExecutionId,
            final Duration timeout) {
        call(() -> service.waitForStepCompletion(WaitForStepCompletionRequest.newBuilder()
                .setFlowId(flowId)
                .setStepType(stepExecutionId.getStepType())
                .setStepExecutionNumber(Integer.toString(stepExecutionId.getExecutionNumber()))
                .setWaitTimeSeconds(seconds32(timeout))
                .setRequestId(UUID.randomUUID().toString())
                .build()));
    }

    public void updateFlowConfig(final String flowId, final FlowConfig config) {
        call(() -> service.updateFlowConfig(UpdateFlowConfigRequest.newBuilder()
                .setFlowId(flowId)
                .setFlowConfig(mapFlowConfig(config))
                .build()));
    }

    public void triggerContinueAsNew(final String flowId) {
        call(() -> service.triggerContinueAsNew(
                TriggerContinueAsNewRequest.newBuilder().setFlowId(flowId).build()));
    }

    boolean healthCheck() {
        call(() -> service.withDeadlineAfter(5, TimeUnit.SECONDS)
                .healthCheck(Empty.getDefaultInstance()));
        return true;
    }

    @Override
    public void close() {
        channel.shutdown();
        try {
            if (!channel.awaitTermination(5, TimeUnit.SECONDS)) {
                channel.shutdownNow();
            }
        } catch (InterruptedException exception) {
            channel.shutdownNow();
            Thread.currentThread().interrupt();
        }
    }

    private Object invokeRpc(
            final RpcTarget target,
            final Method method,
            final Object input) {
        final Registry.RegisteredRpc rpc = target.flow.getRpcByMethod(method.getName());
        final InvokeRPCRequest request = InvokeRPCRequest.newBuilder()
                .setFlowId(target.flowId)
                .setRunId(target.runId)
                .setRpcName(rpc.getName())
                .setInput(values.encode(input))
                .setTimeoutSeconds(rpc.getAnnotation().timeoutSeconds())
                .addAllLockAttributeKeys(rpc.getLocks())
                .setRequestId(UUID.randomUUID().toString())
                .build();
        final io.superdurable.gen.Value output = hydrator.hydrate(
                call(() -> service.invokeRPC(request)).getOutput());
        if (rpc.getMethod().getReturnType() == Void.TYPE) {
            return null;
        }
        final Type returnType = rpc.getMethod().getGenericReturnType();
        final Type outputType = ((ParameterizedType) returnType).getActualTypeArguments()[0];
        if (!(outputType instanceof Class)) {
            throw new IllegalArgumentException("RPC output must be a concrete Class");
        }
        return values.decode(output, (Class<?>) outputType);
    }

    private <T> T getAttributeValue(
            final String flowId,
            final String runId,
            final PersistenceDefinition definition,
            final String instance,
            final Class<T> valueType) {
        final String key = instance == null
                ? definition.getName()
                : Registry.physicalName(definition.getName(), instance);
        final GetAttributesResponse response = call(() -> service.getAttributes(
                GetAttributesRequest.newBuilder()
                        .setFlowId(flowId)
                        .setRunId(runId)
                        .addKeys(key)
                        .build()));
        if (response.getAttributesCount() == 0) {
            return null;
        }
        return values.decode(
                hydrator.hydrate(response.getAttributes(0).getValue()),
                valueType);
    }

    private void setAttributeValue(
            final String flowId,
            final String runId,
            final PersistenceDefinition definition,
            final String instance,
            final Object value,
            final AttributeIndex index) {
        final String key = instance == null
                ? definition.getName()
                : Registry.physicalName(definition.getName(), instance);
        final AttributeWrite.Builder write = AttributeWrite.newBuilder()
                .setKey(key)
                .setValue(values.encode(value));
        final io.superdurable.gen.IndexConfig indexConfig =
                values.indexConfig(index, instance != null);
        if (indexConfig != null) {
            write.setIndexConfig(indexConfig);
        }
        call(() -> service.setAttributes(SetAttributesRequest.newBuilder()
                .setFlowId(flowId)
                .setRunId(runId)
                .addAttributes(write)
                .setRequestId(UUID.randomUUID().toString())
                .build()));
    }

    private void publishValues(
            final String flowId,
            final String runId,
            final String channelName,
            final List<?> payloads) {
        final PublishToChannelRequest.Builder request = PublishToChannelRequest.newBuilder()
                .setFlowId(flowId)
                .setRunId(runId);
        for (Object payload : payloads) {
            request.addMessages(io.superdurable.gen.ChannelMessage.newBuilder()
                    .setChannelName(channelName)
                    .setValue(values.encode(payload)));
        }
        call(() -> service.publishToChannel(request.build()));
    }

    private WaitForFlowResponse waitForFlowResponse(
            final String flowId,
            final Duration timeout) {
        final WaitForFlowRequest.Builder request = WaitForFlowRequest.newBuilder()
                .setFlowId(flowId)
                .setNeedsResults(true);
        if (timeout != null) {
            request.setWaitTimeSeconds(seconds32(timeout));
        }
        final WaitForFlowResponse response;
        try {
            response = service.waitForFlow(request.build());
        } catch (StatusRuntimeException exception) {
            if (exception.getStatus().getCode() == Status.Code.DEADLINE_EXCEEDED) {
                throw new LongPollTimeoutException(flowId, exception);
            }
            throw translate(exception);
        }
        if (response.getFlowStatus() != io.superdurable.gen.FlowStatus.FLOW_STATUS_COMPLETED) {
            final FlowInfo flow = describeFlow(flowId);
            throw new FlowUncompletedException(
                    flow.getRunId(),
                    mapFlowStatus(response.getFlowStatus()),
                    mapFlowErrorType(response.getErrorType()),
                    response.getErrorMessage().isEmpty() ? null : response.getErrorMessage(),
                    hydrator.hydrateStepOutputs(response.getResultsList()),
                    values);
        }
        return response;
    }

    private FlowStartOptions mapStartOptions(final StartFlowOptions options) {
        final FlowStartOptions.Builder mapped = FlowStartOptions.newBuilder()
                .setIdReusePolicy(mapIdReuse(options.getIdReusePolicy()))
                .setCronSchedule(options.getCronSchedule() == null ? "" : options.getCronSchedule())
                .setFlowAlreadyStartedOptions(FlowAlreadyStartedOptions.newBuilder()
                        .setIgnoreAlreadyStartedError(options.isIgnoreAlreadyStarted()));
        if (options.getStartDelay() != null) {
            mapped.setFlowStartDelaySeconds(seconds32(options.getStartDelay()));
        }
        if (options.getRetryPolicy() != null) {
            mapped.setRetryPolicy(mapFlowRetry(options.getRetryPolicy()));
        }
        for (StartFlowOptions.AttributeInitialization initialization : options.getAttributes()) {
            final PersistenceDefinition definition = initialization.getDefinition();
            final String key = initialization.getInstance() == null
                    ? definition.getName()
                    : Registry.physicalName(definition.getName(), initialization.getInstance());
            mapped.addAttributes(AttributeWrite.newBuilder()
                    .setKey(key)
                    .setValue(values.encode(initialization.getValue())));
        }
        final FlowConfig config = options.getConfigOverride();
        if (config != null || this.options.getWorkerTarget() != null) {
            mapped.setFlowConfigOverride(mapFlowConfig(config));
        }
        return mapped.build();
    }

    private io.superdurable.gen.FlowConfig mapFlowConfig(final FlowConfig config) {
        final io.superdurable.gen.FlowConfig.Builder mapped =
                io.superdurable.gen.FlowConfig.newBuilder();
        if (config != null) {
            if (config.getActiveStepSearchMode() != null) {
                mapped.setActiveStepSearchMode(mapSearchMode(config.getActiveStepSearchMode()));
            }
            if (config.getContinueAsNewThreshold() != null) {
                mapped.setContinueAsNewThreshold(config.getContinueAsNewThreshold());
            }
            if (config.getContinueAsNewPageSizeBytes() != null) {
                mapped.setContinueAsNewPageSizeInBytes(
                        config.getContinueAsNewPageSizeBytes());
            }
            if (config.getStepDurability() != null) {
                mapped.setStepDurability(mapDurability(config.getStepDurability()));
            }
        }
        final WorkerTarget target = config != null && config.getWorkerTarget() != null
                ? config.getWorkerTarget()
                : options.getWorkerTarget();
        if (target != null) {
            mapped.setWorkerTarget(io.superdurable.gen.WorkerTarget.newBuilder()
                    .setAddress(target.getAddress())
                    .setIsHeadlessAddress(target.isHeadless()));
        }
        return mapped.build();
    }

    private static io.superdurable.gen.FlowRetryPolicy mapFlowRetry(final RetryPolicy retry) {
        final io.superdurable.gen.FlowRetryPolicy.Builder mapped =
                io.superdurable.gen.FlowRetryPolicy.newBuilder()
                        .setBackoffCoefficient((float) retry.getBackoffCoefficient())
                        .setMaximumAttempts(retry.getMaximumAttempts());
        if (retry.getInitialInterval() != null) {
            mapped.setInitialIntervalSeconds(seconds32(retry.getInitialInterval()));
        }
        if (retry.getMaximumInterval() != null) {
            mapped.setMaximumIntervalSeconds(seconds32(retry.getMaximumInterval()));
        }
        return mapped.build();
    }

    private static int seconds32(final Duration duration) {
        if (duration == null || duration.isNegative() || duration.getNano() != 0
                || duration.getSeconds() > Integer.MAX_VALUE) {
            throw new IllegalArgumentException("Duration must be whole seconds within int32");
        }
        return (int) duration.getSeconds();
    }

    private static Instant instant(final Timestamp timestamp) {
        return Instant.ofEpochSecond(timestamp.getSeconds(), timestamp.getNanos());
    }

    private static io.superdurable.gen.IdReusePolicy mapIdReuse(final IdReusePolicy policy) {
        switch (policy) {
            case ALLOW_IF_PREVIOUS_FAILED:
                return io.superdurable.gen.IdReusePolicy
                        .ID_REUSE_POLICY_ALLOW_IF_PREVIOUS_EXISTS_ABNORMALLY;
            case ALLOW_IF_NOT_RUNNING:
                return io.superdurable.gen.IdReusePolicy.ID_REUSE_POLICY_ALLOW_IF_NO_RUNNING;
            case DISALLOW:
                return io.superdurable.gen.IdReusePolicy.ID_REUSE_POLICY_DISALLOW_REUSE;
            case TERMINATE_IF_RUNNING:
                return io.superdurable.gen.IdReusePolicy
                        .ID_REUSE_POLICY_ALLOW_TERMINATE_IF_RUNNING;
            default:
                return io.superdurable.gen.IdReusePolicy.ID_REUSE_POLICY_UNSPECIFIED;
        }
    }

    private static io.superdurable.gen.StopType mapStopType(final StopType type) {
        switch (type) {
            case TERMINATE:
                return io.superdurable.gen.StopType.STOP_TYPE_TERMINATE;
            case FAIL:
                return io.superdurable.gen.StopType.STOP_TYPE_FAIL;
            default:
                return io.superdurable.gen.StopType.STOP_TYPE_CANCEL;
        }
    }

    private static FlowResetType mapResetType(final ResetType type) {
        switch (type) {
            case HISTORY_EVENT_ID:
                return FlowResetType.FLOW_RESET_TYPE_HISTORY_EVENT_ID;
            case BEGINNING:
                return FlowResetType.FLOW_RESET_TYPE_BEGINNING;
            case HISTORY_EVENT_TIME:
                return FlowResetType.FLOW_RESET_TYPE_HISTORY_EVENT_TIME;
            case STEP_TYPE:
                return FlowResetType.FLOW_RESET_TYPE_STEP_TYPE;
            case STEP_EXECUTION_ID:
                return FlowResetType.FLOW_RESET_TYPE_STEP_EXECUTION_ID;
            default:
                return FlowResetType.FLOW_RESET_TYPE_UNSPECIFIED;
        }
    }

    private static FlowStatus mapFlowStatus(final io.superdurable.gen.FlowStatus status) {
        switch (status) {
            case FLOW_STATUS_RUNNING:
                return FlowStatus.RUNNING;
            case FLOW_STATUS_COMPLETED:
                return FlowStatus.COMPLETED;
            case FLOW_STATUS_FAILED:
                return FlowStatus.FAILED;
            case FLOW_STATUS_TIMEOUT:
                return FlowStatus.TIMED_OUT;
            case FLOW_STATUS_TERMINATED:
                return FlowStatus.TERMINATED;
            case FLOW_STATUS_CANCELED:
                return FlowStatus.CANCELED;
            case FLOW_STATUS_CONTINUED_AS_NEW:
                return FlowStatus.CONTINUED_AS_NEW;
            default:
                throw new IllegalArgumentException("unknown Flow status " + status);
        }
    }

    private static FlowErrorType mapFlowErrorType(
            final io.superdurable.gen.FlowErrorType type) {
        switch (type) {
            case FLOW_ERROR_TYPE_STEP_DECISION_FAILING_FLOW:
                return FlowErrorType.STEP_DECISION_FAILED;
            case FLOW_ERROR_TYPE_CLIENT_API_FAILING_FLOW:
                return FlowErrorType.CLIENT_API_FAILED;
            case FLOW_ERROR_TYPE_WORKER_API_FAIL:
                return FlowErrorType.WORKER_API_FAILED;
            case FLOW_ERROR_TYPE_INVALID_USER_FLOW_CODE:
                return FlowErrorType.INVALID_USER_FLOW_CODE;
            case FLOW_ERROR_TYPE_INTERNAL:
                return FlowErrorType.INTERNAL;
            default:
                return null;
        }
    }

    private static <T> T call(final RpcCall<T> rpc) {
        try {
            return rpc.invoke();
        } catch (StatusRuntimeException exception) {
            throw translate(exception);
        }
    }

    private static DexException translate(final StatusRuntimeException exception) {
        ErrorResponse details = null;
        final com.google.rpc.Status status = StatusProto.fromThrowable(exception);
        if (status != null) {
            for (com.google.protobuf.Any value : status.getDetailsList()) {
                if (!value.is(ErrorResponse.class)) {
                    continue;
                }
                try {
                    details = value.unpack(ErrorResponse.class);
                } catch (InvalidProtocolBufferException malformed) {
                    throw new IllegalStateException("Dex returned malformed error details", malformed);
                }
                break;
            }
        }
        final String detail = details == null || details.getDetail().isEmpty()
                ? exception.getStatus().getDescription()
                : details.getDetail();
        return new DexException(
                exception.getStatus().getCode(),
                details == null ? null : mapErrorSubStatus(details.getSubStatus()),
                detail,
                details == null ? "" : details.getOriginalWorkerErrorType(),
                details == null ? "" : details.getOriginalWorkerErrorDetail(),
                exception);
    }

    private static ErrorSubStatus mapErrorSubStatus(
            final io.superdurable.gen.ErrorSubStatus subStatus) {
        switch (subStatus) {
            case ERROR_SUB_STATUS_UNCATEGORIZED:
                return ErrorSubStatus.UNCATEGORIZED;
            case ERROR_SUB_STATUS_FLOW_ALREADY_STARTED:
                return ErrorSubStatus.FLOW_ALREADY_STARTED;
            case ERROR_SUB_STATUS_FLOW_NOT_EXISTS:
                return ErrorSubStatus.FLOW_NOT_EXISTS;
            case ERROR_SUB_STATUS_WORKER_API_ERROR:
                return ErrorSubStatus.WORKER_API_ERROR;
            case ERROR_SUB_STATUS_LONG_POLL_TIME_OUT:
                return ErrorSubStatus.LONG_POLL_TIMEOUT;
            default:
                return null;
        }
    }

    private static io.superdurable.gen.ActiveStepSearchMode mapSearchMode(
            final ActiveStepSearchMode mode) {
        switch (mode) {
            case ALL:
                return io.superdurable.gen.ActiveStepSearchMode
                        .ACTIVE_STEP_SEARCH_MODE_ENABLED_FOR_ALL;
            case WITH_WAIT_FOR:
                return io.superdurable.gen.ActiveStepSearchMode
                        .ACTIVE_STEP_SEARCH_MODE_ENABLED_FOR_STEPS_WITH_WAIT_FOR;
            case DISABLED:
                return io.superdurable.gen.ActiveStepSearchMode
                        .ACTIVE_STEP_SEARCH_MODE_DISABLED;
            default:
                return io.superdurable.gen.ActiveStepSearchMode
                        .ACTIVE_STEP_SEARCH_MODE_UNSPECIFIED;
        }
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

    private final class RpcInvocationHandler implements InvocationHandler {
        private final RpcTarget target;

        private RpcInvocationHandler(final RpcTarget target) {
            this.target = target;
        }

        @Override
        public Object invoke(
                final Object stub,
                final Method method,
                final Object[] arguments) {
            final Object input = arguments.length == 2 ? arguments[1] : null;
            final Object output = invokeRpc(target, method, input);
            return method.getReturnType() == Void.TYPE ? null : RPCResult.of(output);
        }
    }

    private static final class RpcTarget {
        private final Registry.RegisteredFlow flow;
        private final String flowId;
        private final String runId;

        private RpcTarget(
                final Registry.RegisteredFlow flow,
                final String flowId,
                final String runId) {
            this.flow = flow;
            this.flowId = flowId;
            this.runId = runId;
        }
    }

    private interface RpcCall<T> {
        T invoke();
    }
}
