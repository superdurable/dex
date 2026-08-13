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
import com.google.protobuf.Timestamp;
import io.grpc.ManagedChannel;
import io.grpc.ManagedChannelBuilder;
import io.grpc.StatusRuntimeException;
import io.superdurable.dex.GrpcExceptionTranslator.FlowTargetRequirement;
import io.superdurable.dex.exceptions.DexServiceException;
import io.superdurable.dex.exceptions.FlowAlreadyStartedException;
import io.superdurable.dex.exceptions.FlowDefinitionException;
import io.superdurable.dex.exceptions.FlowNotActiveException;
import io.superdurable.dex.exceptions.FlowNotFoundException;
import io.superdurable.dex.exceptions.FlowUncompletedException;
import io.superdurable.dex.exceptions.LongPollTimeoutException;
import io.superdurable.dex.exceptions.RpcLockConflictException;
import io.superdurable.dex.exceptions.WorkerInvocationException;
import io.superdurable.gen.AttributeSyncConfig;
import io.superdurable.gen.AttributeWrite;
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
import io.superdurable.gen.SearchFlowsRequest;
import io.superdurable.gen.SearchFlowsResponse;
import io.superdurable.gen.SearchFlowsResponseEntry;
import io.superdurable.gen.SetAttributesRequest;
import io.superdurable.gen.SkipTimerRequest;
import io.superdurable.gen.StartFlowRequest;
import io.superdurable.gen.StopFlowRequest;
import io.superdurable.gen.TriggerContinueAsNewRequest;
import io.superdurable.gen.UpdateFlowConfigRequest;
import io.superdurable.gen.Value;
import io.superdurable.gen.WaitForFlowRequest;
import io.superdurable.gen.WaitForFlowResponse;
import io.superdurable.gen.WaitForStepCompletionRequest;
import io.superdurable.gen.WaitForAttributeCondition;
import io.superdurable.gen.WaitForAttributeEqual;
import io.superdurable.gen.WaitForAttributeRequest;
import net.bytebuddy.ByteBuddy;
import net.bytebuddy.dynamic.loading.ClassLoadingStrategy;
import net.bytebuddy.dynamic.scaffold.subclass.ConstructorStrategy;
import net.bytebuddy.implementation.InvocationHandlerAdapter;
import net.bytebuddy.matcher.ElementMatchers;
import org.objenesis.ObjenesisStd;

import java.lang.reflect.InvocationHandler;
import java.lang.reflect.Method;
import java.lang.reflect.ParameterizedType;
import java.lang.reflect.Type;
import java.time.Duration;
import java.time.Instant;
import java.util.ArrayList;
import java.util.Collections;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.UUID;
import java.util.concurrent.TimeUnit;

/**
 * Provides synchronous, strongly typed operations for starting and controlling Dex Flows.
 *
 * <p>All network methods block the calling thread until the gRPC request completes. Create one
 * client for a registered set of Flow definitions and reuse it across calls; the underlying gRPC
 * channel supports concurrent callers. The supplied {@link BlobCache} is borrowed and is not closed
 * by the client. Service failures use typed {@link DexServiceException} subclasses. Long-poll
 * operations can throw {@link LongPollTimeoutException}, while {@link #waitForFlow(String)} can
 * throw {@link FlowUncompletedException} after observing a terminal status other than
 * {@link FlowStatus#COMPLETED}.
 *
 * <pre>{@code
 * Registry registry = new Registry(Collections.<Flow<?>>singletonList(orderFlow));
 * try (Client client = new Client(registry, blobCache)) {
 *     String runId = client.startFlow(orderFlow, "order-123", input);
 *     OrderFlow rpc = client.newRpcStub(OrderFlow.class, "order-123", runId);
 *     OrderStatus status = client.invokeRPC(rpc::getStatus);
 *     OrderOutput output = client.waitForFlow("order-123").getSingleOutput(OrderOutput.class);
 * }
 * }</pre>
 */
public final class Client implements AutoCloseable {
    private final Registry registry;
    private final BlobCache blobCache;
    private final ClientOptions options;
    private final ManagedChannel channel;
    private final FlowServiceGrpc.FlowServiceBlockingStub service;
    private final ValueMapper values;
    private final ValueHydrator hydrator;
    private final WorkerDispatcher mappings;

    /**
     * Creates a client using local development defaults.
     *
     * @param registry the nonnull registry of Flow definitions used for typed mapping
     * @param blobCache the nonnull cache used to hydrate blob-backed values
     * @throws IllegalArgumentException if either argument is {@code null}
     */
    public Client(final Registry registry, final BlobCache blobCache) {
        this(registry, blobCache, new ClientOptions());
    }

    /**
     * Creates a client with explicit connection, routing, and serialization options.
     *
     * @param registry the nonnull registry of Flow definitions used for typed mapping
     * @param blobCache the nonnull cache used to hydrate blob-backed values
     * @param options the nonnull client options
     * @throws IllegalArgumentException if any argument is {@code null}
     */
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

    /**
     * Starts a registered Flow using default start options.
     *
     * @param flow the exact Flow instance registered with this client
     * @param flowId the nonblank application-assigned Flow ID
     * @param input the typed start-Step input, or {@code null} for a Flow without a start Step
     * @param <I> the Flow start input type
     * @return the server-assigned run ID
     * @throws IllegalArgumentException if the Flow is unregistered, the ID is invalid, or the input
     *     does not match the registered start Step
     * @throws FlowAlreadyStartedException if the Flow ID conflicts with an existing execution
     * @throws DexServiceException if Dex otherwise rejects or cannot complete the request
     */
    public <I> String startFlow(
            final Flow<I> flow,
            final String flowId,
            final I input) {
        return startFlow(flow, flowId, input, new StartFlowOptions());
    }

    /**
     * Starts a registered Flow with explicit start options.
     *
     * @param flow the exact Flow instance registered with this client
     * @param flowId the nonblank application-assigned Flow ID
     * @param input the typed start-Step input, or {@code null} for a Flow without a start Step
     * @param startOptions timeout, scheduling, initial state, routing, and idempotency settings
     * @param <I> the Flow start input type
     * @return the server-assigned run ID
     * @throws IllegalArgumentException if the Flow, ID, input, or duration settings are invalid
     * @throws FlowAlreadyStartedException if the Flow ID conflicts with an existing execution
     * @throws DexServiceException if Dex otherwise rejects or cannot complete the request
     */
    public <I> String startFlow(
            final Flow<I> flow,
            final String flowId,
            final I input,
            final StartFlowOptions startOptions) {
        final Registry.RegisteredFlow registered = registry.getFlow(flow.getFlowType());
        if (registered.getFlow() != flow) {
            throw new FlowDefinitionException(
                    "Flow instance is not registered: " + flow.getFlowType());
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

    /**
     * Creates a strongly typed RPC stub targeting the current run of a Flow.
     *
     * @param rpcClass the non-final registered Flow class containing {@link RPC} methods
     * @param flowId the target Flow ID
     * @param <T> the concrete Flow class
     * @return a constructor-free stub whose RPC methods perform client requests
     * @throws IllegalArgumentException if the class is unregistered, final, or cannot be subclassed
     */
    public <T> T newRpcStub(final Class<T> rpcClass, final String flowId) {
        return newRpcStub(rpcClass, flowId, "");
    }

    /**
     * Creates a strongly typed RPC stub targeting a specific Flow run.
     *
     * <p>Dex subclasses the class with ByteBuddy and allocates the stub without invoking a
     * constructor. RPC methods and the containing class must therefore be non-final. Kotlin classes
     * and methods are final by default and must be declared {@code open}.
     *
     * @param rpcClass the non-final registered Flow class containing {@link RPC} methods
     * @param flowId the target Flow ID
     * @param runId the target run ID, or an empty string for the current run
     * @param <T> the concrete Flow class
     * @return a constructor-free typed RPC stub
     * @throws IllegalArgumentException if the class is unregistered, final, or cannot be subclassed
     */
    public <T> T newRpcStub(
            final Class<T> rpcClass,
            final String flowId,
            final String runId) {
        final Registry.RegisteredFlow flow = registry.getFlow(rpcClass);
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
            throw new FlowDefinitionException(
                    "RPC stub could not subclass " + rpcClass.getName(), exception);
        }
    }

    /**
     * Invokes a function-style RPC with one input and returns its typed output.
     *
     * @param rpcStubMethod a direct method reference from a stub created by this client
     * @param input the typed RPC input
     * @param <I> the RPC input type
     * @param <O> the RPC output type
     * @return the decoded RPC output
     * @throws FlowNotActiveException if the target Flow has no active execution
     * @throws RpcLockConflictException if the RPC cannot acquire its Attribute locks
     * @throws WorkerInvocationException if worker code fails while executing the RPC
     * @throws DexServiceException if Dex otherwise rejects or cannot complete the RPC
     */
    public <I, O> O invokeRPC(
            final RpcDefinitions.RpcFunc1<I, O> rpcStubMethod,
            final I input) {
        return rpcStubMethod.execute(null, input).getOutput();
    }

    /**
     * Invokes a function-style RPC without application input.
     *
     * @param rpcStubMethod a direct method reference from a stub created by this client
     * @param <O> the RPC output type
     * @return the decoded RPC output
     * @throws FlowNotActiveException if the target Flow has no active execution
     * @throws RpcLockConflictException if the RPC cannot acquire its Attribute locks
     * @throws WorkerInvocationException if worker code fails while executing the RPC
     * @throws DexServiceException if Dex otherwise rejects or cannot complete the RPC
     */
    public <O> O invokeRPC(final RpcDefinitions.RpcFunc0<O> rpcStubMethod) {
        return rpcStubMethod.execute(null).getOutput();
    }

    /**
     * Invokes a procedure-style RPC with one input.
     *
     * @param rpcStubMethod a direct method reference from a stub created by this client
     * @param input the typed RPC input
     * @param <I> the RPC input type
     * @throws FlowNotActiveException if the target Flow has no active execution
     * @throws RpcLockConflictException if the RPC cannot acquire its Attribute locks
     * @throws WorkerInvocationException if worker code fails while executing the RPC
     * @throws DexServiceException if Dex otherwise rejects or cannot complete the RPC
     */
    public <I> void invokeRPC(
            final RpcDefinitions.RpcProc1<I> rpcStubMethod,
            final I input) {
        rpcStubMethod.execute(null, input);
    }

    /**
     * Invokes a procedure-style RPC without application input.
     *
     * @param rpcStubMethod a direct method reference from a stub created by this client
     * @throws FlowNotActiveException if the target Flow has no active execution
     * @throws RpcLockConflictException if the RPC cannot acquire its Attribute locks
     * @throws WorkerInvocationException if worker code fails while executing the RPC
     * @throws DexServiceException if Dex otherwise rejects or cannot complete the RPC
     */
    public void invokeRPC(final RpcDefinitions.RpcProc0 rpcStubMethod) {
        rpcStubMethod.execute(null);
    }

    /**
     * Reads an Attribute from a specific Flow run.
     *
     * @param flowId the target Flow ID
     * @param runId the target run ID
     * @param attribute the typed Attribute definition
     * @param <T> the Attribute value type
     * @return the decoded value, or {@code null} when absent
     * @throws FlowNotFoundException if no matching Flow execution exists
     * @throws DexServiceException if Dex otherwise cannot complete the read
     */
    public <T> T getAttribute(
            final String flowId,
            final String runId,
            final Attribute<T> attribute) {
        return getAttributeValue(flowId, runId, attribute, null, attribute.getValueType());
    }

    /**
     * Reads an Attribute from the current Flow run.
     *
     * @param flowId the target Flow ID
     * @param attribute the typed Attribute definition
     * @param <T> the Attribute value type
     * @return the decoded value, or {@code null} when absent
     * @throws FlowNotFoundException if no matching Flow execution exists
     * @throws DexServiceException if Dex otherwise cannot complete the read
     */
    public <T> T getAttribute(final String flowId, final Attribute<T> attribute) {
        return getAttribute(flowId, "", attribute);
    }

    /**
     * Reads one Attribute-map instance from the current Flow run.
     *
     * @param flowId the target Flow ID
     * @param attribute the typed Attribute-map definition
     * @param instance the map instance
     * @param <T> the Attribute value type
     * @return the decoded value, or {@code null} when absent
     * @throws FlowNotFoundException if no matching Flow execution exists
     * @throws DexServiceException if Dex otherwise cannot complete the read
     */
    public <T> T getAttribute(
            final String flowId,
            final AttributeMap<T> attribute,
            final String instance) {
        return getAttributeValue(flowId, "", attribute, instance, attribute.getValueType());
    }

    /**
     * Writes an Attribute in a specific Flow run.
     *
     * @param flowId the target Flow ID
     * @param runId the target run ID
     * @param attribute the typed Attribute definition
     * @param value the value to persist
     * @param <T> the Attribute value type
     * @throws FlowNotActiveException if the target Flow has no active execution
     * @throws DexServiceException if Dex otherwise cannot complete the write
     */
    public <T> void setAttribute(
            final String flowId,
            final String runId,
            final Attribute<T> attribute,
            final T value) {
        setAttributeValue(flowId, runId, attribute, null, value, attribute.getIndex());
    }

    /**
     * Writes an Attribute in the current Flow run.
     *
     * @param flowId the target Flow ID
     * @param attribute the typed Attribute definition
     * @param value the value to persist
     * @param <T> the Attribute value type
     * @throws FlowNotActiveException if the target Flow has no active execution
     * @throws DexServiceException if Dex otherwise cannot complete the write
     */
    public <T> void setAttribute(
            final String flowId,
            final Attribute<T> attribute,
            final T value) {
        setAttribute(flowId, "", attribute, value);
    }

    /**
     * Writes one Attribute-map instance in the current Flow run.
     *
     * @param flowId the target Flow ID
     * @param attribute the typed Attribute-map definition
     * @param instance the map instance
     * @param value the value to persist
     * @param <T> the Attribute value type
     * @throws FlowNotActiveException if the target Flow has no active execution
     * @throws DexServiceException if Dex otherwise cannot complete the write
     */
    public <T> void setAttribute(
            final String flowId,
            final AttributeMap<T> attribute,
            final String instance,
            final T value) {
        setAttributeValue(flowId, "", attribute, instance, value, attribute.getIndex());
    }

    /**
     * Publishes one message to a Channel in a specific Flow run.
     *
     * @param flowId the target Flow ID
     * @param runId the target run ID
     * @param channel the typed Channel definition
     * @param value the message value
     * @param <T> the Channel message type
     * @throws FlowNotActiveException if the target Flow has no active execution
     * @throws DexServiceException if Dex otherwise cannot publish the message
     */
    public <T> void publish(
            final String flowId,
            final String runId,
            final Channel<T> channel,
            final T value) {
        publishValues(flowId, runId, channel.getName(), Collections.singletonList(value));
    }

    /**
     * Publishes one or more messages to a Channel in the current run.
     *
     * @param flowId the target Flow ID
     * @param channel the typed Channel definition
     * @param values message values published in argument order
     * @param <T> the Channel message type
     * @throws FlowNotActiveException if the target Flow has no active execution
     * @throws DexServiceException if Dex otherwise cannot publish the messages
     */
    @SafeVarargs
    public final <T> void publish(
            final String flowId,
            final Channel<T> channel,
            final T... values) {
        publishValues(flowId, "", channel.getName(), java.util.Arrays.asList(values));
    }

    /**
     * Publishes one or more messages to a Channel-map instance in the current run.
     *
     * @param flowId the target Flow ID
     * @param channel the typed Channel-map definition
     * @param instance the map instance
     * @param values message values published in argument order
     * @param <T> the Channel message type
     * @throws FlowNotActiveException if the target Flow has no active execution
     * @throws DexServiceException if Dex otherwise cannot publish the messages
     */
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

    /**
     * Publishes a list of messages to a Channel in the current run.
     *
     * @param flowId the target Flow ID
     * @param channel the typed Channel definition
     * @param values message values published in list order
     * @param <T> the Channel message type
     * @throws FlowNotActiveException if the target Flow has no active execution
     * @throws DexServiceException if Dex otherwise cannot publish the messages
     */
    public <T> void publish(
            final String flowId,
            final Channel<T> channel,
            final List<T> values) {
        publishValues(flowId, "", channel.getName(), values);
    }

    /**
     * Cancels a running Flow without an explicit reason.
     *
     * @param flowId the target Flow ID
     * @throws FlowNotActiveException if the target Flow has no active execution
     * @throws DexServiceException if Dex otherwise cannot stop the Flow
     */
    public void stopFlow(final String flowId) {
        stopFlow(flowId, new StopFlowOptions());
    }

    /**
     * Stops a running Flow with explicit terminal behavior.
     *
     * @param flowId the target Flow ID
     * @param stopOptions the stop mode and optional reason
     * @throws FlowNotActiveException if the target Flow has no active execution
     * @throws DexServiceException if Dex otherwise cannot stop the Flow
     */
    public void stopFlow(final String flowId, final StopFlowOptions stopOptions) {
        call(() -> service.stopFlow(StopFlowRequest.newBuilder()
                .setFlowId(flowId)
                .setReason(stopOptions.getReason() == null ? "" : stopOptions.getReason())
                .setStopType(mapStopType(stopOptions.getType()))
                .build()), FlowTargetRequirement.ACTIVE, flowId);
    }

    /**
     * Blocks until a Flow reaches a terminal status and returns normally only for
     * {@link FlowStatus#COMPLETED}.
     *
     * @param flowId the target Flow ID
     * @return all output-bearing Step completions in server collection order
     * @throws FlowUncompletedException if the terminal status is not {@link FlowStatus#COMPLETED}
     * @throws FlowNotFoundException if no matching Flow execution exists
     * @throws DexServiceException if Dex otherwise cannot complete the wait request
     */
    public WaitForFlowResult waitForFlow(final String flowId) {
        return waitForFlow(flowId, null);
    }

    /**
     * Blocks for a bounded duration until a Flow reaches a terminal status and returns every
     * output-bearing Step completion only for {@link FlowStatus#COMPLETED}.
     *
     * @param flowId the target Flow ID
     * @param timeout the nonnegative whole-second long-poll duration, or {@code null} for no bound
     * @return all output-bearing Step completions in server collection order
     * @throws LongPollTimeoutException if {@code timeout} expires while the Flow remains running
     * @throws FlowUncompletedException if the terminal status is not {@link FlowStatus#COMPLETED}
     * @throws IllegalArgumentException if {@code timeout} is not a supported whole-second duration
     * @throws FlowNotFoundException if no matching Flow execution exists
     * @throws DexServiceException if Dex otherwise cannot complete the wait request
     */
    public WaitForFlowResult waitForFlow(
            final String flowId,
            final Duration timeout) {
        final WaitForFlowResponse response = waitForFlowResponse(flowId, timeout);
        return new WaitForFlowResult(mapStepCompletions(response.getResultsList()));
    }

    /**
     * Returns a {@link FlowInfo} summary of the current Flow run.
     *
     * <p>The summary contains the Flow ID, run ID, Flow type, current status, and start time. It
     * reflects the execution when Dex handles this request; a running Flow may change afterward.
     *
     * @param flowId the target Flow ID
     * @return the current Flow run summary
     * @throws FlowNotFoundException if no matching Flow execution exists
     * @throws DexServiceException if Dex otherwise cannot describe the Flow
     */
    public FlowInfo describeFlow(final String flowId) {
        final GetFlowSummaryResponse response = call(() -> service.getFlowSummary(
                GetFlowSummaryRequest.newBuilder().setFlowId(flowId).build()),
                FlowTargetRequirement.EXISTING,
                flowId);
        final FlowExecutionID execution = response.getFlowExecutionId();
        return new FlowInfo(
                execution.getFlowId(),
                execution.getRunId(),
                response.getFlowType(),
                mapFlowStatus(response.getFlowStatus()),
                instant(response.getStartTime()));
    }

    /**
     * Searches Flow executions and returns the first page.
     *
     * @param query the server search expression, or {@code null} for an unfiltered search
     * @param pageSize the nonnegative requested page size; zero uses the server default
     * @return the first immutable search-results page
     * @throws IllegalArgumentException if {@code pageSize} is negative
     * @throws DexServiceException if Dex cannot execute the search
     */
    public SearchFlowsPage searchFlows(final String query, final int pageSize) {
        return searchFlows(query, pageSize, "");
    }

    /**
     * Searches Flow executions from a continuation token.
     *
     * @param query the same server search expression used for the previous page
     * @param pageSize the nonnegative requested page size; zero uses the server default
     * @param nextPageToken the prior page's token, or {@code null} for the first page
     * @return an immutable search-results page
     * @throws IllegalArgumentException if {@code pageSize} is negative
     * @throws DexServiceException if Dex cannot execute the search
     */
    public SearchFlowsPage searchFlows(
            final String query,
            final int pageSize,
            final String nextPageToken) {
        if (pageSize < 0) {
            throw new IllegalArgumentException("search page size must not be negative");
        }
        final SearchFlowsResponse response = call(() -> service.searchFlows(
                SearchFlowsRequest.newBuilder()
                        .setQuery(query == null ? "" : query)
                        .setPageSize(pageSize)
                        .setNextPageToken(nextPageToken == null ? "" : nextPageToken)
                        .build()));
        final List<SearchFlowEntry> flows =
                new ArrayList<SearchFlowEntry>(response.getFlowRunsCount());
        for (final SearchFlowsResponseEntry entry : response.getFlowRunsList()) {
            flows.add(mapSearchEntry(entry));
        }
        return new SearchFlowsPage(flows, response.getNextPageToken());
    }

    private SearchFlowEntry mapSearchEntry(final SearchFlowsResponseEntry entry) {
        final Map<String, Object> attributes = new LinkedHashMap<String, Object>();
        for (final KV attribute : entry.getIndexedAttributesList()) {
            attributes.put(
                    attribute.getKey(),
                    values.decodeToObject(hydrator.hydrate(attribute.getValue())));
        }
        return new SearchFlowEntry(
                entry.getFlowId(),
                entry.getRunId(),
                entry.getFlowType(),
                mapFlowStatus(entry.getFlowStatus()),
                entry.hasStartTime() ? instant(entry.getStartTime()) : null,
                entry.hasCloseTime() ? instant(entry.getCloseTime()) : null,
                attributes);
    }

    /**
     * Resets a Flow and returns the new run ID.
     *
     * @param flowId the target Flow ID
     * @param options the reset point and replay behavior
     * @return the server-assigned run ID of the reset execution
     * @throws FlowNotFoundException if no matching Flow execution exists
     * @throws DexServiceException if Dex otherwise rejects or cannot perform the reset
     */
    public String resetFlow(final String flowId, final ResetFlowOptions options) {
        final ResetFlowRequest.Builder request = ResetFlowRequest.newBuilder()
                .setFlowId(flowId)
                .setResetType(mapResetType(options.getType()))
                .setReason(options.getReason() == null ? "" : options.getReason())
                .setSkipWritesReapply(options.isSkipWritesReapply());
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
        return call(
                () -> service.resetFlow(request.build()),
                FlowTargetRequirement.EXISTING,
                flowId).getRunId();
    }

    /**
     * Makes one durable timer condition fire immediately.
     *
     * @param flowId the target Flow ID
     * @param stepExecutionId the Step execution containing the timer
     * @param timerId the timer selected by condition ID or index
     * @throws FlowNotActiveException if the target Flow has no active execution
     * @throws DexServiceException if Dex otherwise cannot find or skip the timer
     */
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
        call(
                () -> service.skipTimer(request.build()),
                FlowTargetRequirement.ACTIVE,
                flowId);
    }

    /**
     * Blocks until a specific Step execution completes or the wait duration expires.
     *
     * @param flowId the target Flow ID
     * @param stepExecutionId the Step execution to observe
     * @param timeout the nonnegative whole-second wait duration
     * @throws IllegalArgumentException if {@code timeout} is not a supported whole-second duration
     * @throws LongPollTimeoutException if {@code timeout} expires before the Step completes
     * @throws FlowNotActiveException if the target Flow has no active execution
     * @throws DexServiceException if Dex otherwise cannot complete the wait request
     */
    public void waitForStepCompletion(
            final String flowId,
            final StepExecutionId stepExecutionId,
            final Duration timeout) {
        call(
                () -> service.waitForStepCompletion(WaitForStepCompletionRequest.newBuilder()
                        .setFlowId(flowId)
                        .setStepType(stepExecutionId.getStepType())
                        .setStepExecutionNumber(
                                Integer.toString(stepExecutionId.getExecutionNumber()))
                        .setWaitTimeSeconds(seconds32(timeout))
                        .setRequestId(UUID.randomUUID().toString())
                        .build()),
                FlowTargetRequirement.ACTIVE,
                flowId);
    }

    /**
     * Blocks until a singleton Attribute equals the expected value or the wait duration expires.
     *
     * @param flowId the target Flow ID
     * @param attribute the registered Attribute definition
     * @param expected the expected string, boolean, integer, or floating-point value
     * @param timeout the nonnegative whole-second wait duration
     * @param <T> the Attribute value type
     * @throws IllegalArgumentException if the expected value is not a string, Boolean, or number
     * @throws LongPollTimeoutException if the timeout expires before the value matches
     * @throws FlowNotActiveException if the target Flow has no active execution
     * @throws DexServiceException if Dex otherwise cannot complete the wait
     */
    public <T> void waitForAttributeEqual(
            final String flowId,
            final Attribute<T> attribute,
            final T expected,
            final Duration timeout) {
        waitForAttributeValue(flowId, attribute, null, expected, timeout);
    }

    /**
     * Blocks until an AttributeMap instance equals the expected value.
     *
     * @param flowId the target Flow ID
     * @param attribute the registered Attribute-map definition
     * @param instance the map instance
     * @param expected the expected string, boolean, integer, or floating-point value
     * @param timeout the nonnegative whole-second wait duration
     * @param <T> the Attribute value type
     * @throws IllegalArgumentException if the expected value is not a string, Boolean, or number
     * @throws LongPollTimeoutException if the timeout expires before the value matches
     * @throws FlowNotActiveException if the target Flow has no active execution
     * @throws DexServiceException if Dex otherwise cannot complete the wait
     */
    public <T> void waitForAttributeEqual(
            final String flowId,
            final AttributeMap<T> attribute,
            final String instance,
            final T expected,
            final Duration timeout) {
        waitForAttributeValue(flowId, attribute, instance, expected, timeout);
    }

    private void waitForAttributeValue(
            final String flowId,
            final PersistenceDefinition attribute,
            final String instance,
            final Object expected,
            final Duration timeout) {
        final Value encoded = values.encode(expected);
        if (!isPrimitiveValue(encoded)) {
            throw new IllegalArgumentException(
                    "waitForAttributeEqual supports only string, Boolean, or number values");
        }
        final String key = instance == null
                ? attribute.getName()
                : Registry.physicalName(attribute.getName(), instance);
        call(
                () -> service.waitForAttribute(WaitForAttributeRequest.newBuilder()
                        .setFlowId(flowId)
                        .setCondition(WaitForAttributeCondition.newBuilder()
                                .setEqual(WaitForAttributeEqual.newBuilder()
                                        .setKey(key)
                                        .setValue(encoded)))
                        .setWaitTimeSeconds(seconds32(timeout))
                        .setRequestId(UUID.randomUUID().toString())
                        .build()),
                FlowTargetRequirement.ACTIVE,
                flowId);
    }

    private static boolean isPrimitiveValue(final Value value) {
        switch (value.getKindCase()) {
            case STRING_VALUE:
            case BOOL_VALUE:
            case INT_VALUE:
            case DOUBLE_VALUE:
                return true;
            default:
                return false;
        }
    }

    /**
     * Replaces the mutable Flow configuration for the current run.
     *
     * @param flowId the target Flow ID
     * @param config the new Flow configuration
     * @throws FlowNotActiveException if the target Flow has no active execution
     * @throws DexServiceException if Dex otherwise cannot update the Flow
     */
    public void updateFlowConfig(final String flowId, final FlowConfig config) {
        call(
                () -> service.updateFlowConfig(UpdateFlowConfigRequest.newBuilder()
                        .setFlowId(flowId)
                        .setFlowConfig(mapFlowConfig(config))
                        .build()),
                FlowTargetRequirement.ACTIVE,
                flowId);
    }

    /**
     * Requests that the current Flow run continue as new.
     *
     * @param flowId the target Flow ID
     * @throws FlowNotActiveException if the target Flow has no active execution
     * @throws DexServiceException if Dex otherwise cannot apply the request
     */
    public void triggerContinueAsNew(final String flowId) {
        call(
                () -> service.triggerContinueAsNew(
                        TriggerContinueAsNewRequest.newBuilder().setFlowId(flowId).build()),
                FlowTargetRequirement.ACTIVE,
                flowId);
    }

    boolean healthCheck() {
        call(() -> service.withDeadlineAfter(5, TimeUnit.SECONDS)
                .healthCheck(Empty.getDefaultInstance()));
        return true;
    }

    /**
     * Shuts down the client's gRPC channel.
     *
     * <p>The method waits up to five seconds for channel termination and preserves interruption
     * status. It does not close the borrowed {@link BlobCache}.
     */
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
                call(
                        () -> service.invokeRPC(request),
                        FlowTargetRequirement.ACTIVE,
                        target.flowId).getOutput());
        if (rpc.getMethod().getReturnType() == Void.TYPE) {
            return null;
        }
        final Type returnType = rpc.getMethod().getGenericReturnType();
        final Type outputType = ((ParameterizedType) returnType).getActualTypeArguments()[0];
        if (!(outputType instanceof Class)) {
            throw new FlowDefinitionException("RPC output must be a concrete Class");
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
        final GetAttributesResponse response = call(
                () -> service.getAttributes(GetAttributesRequest.newBuilder()
                        .setFlowId(flowId)
                        .setRunId(runId)
                        .addKeys(key)
                        .build()),
                FlowTargetRequirement.EXISTING,
                flowId);
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
        applyAttributeSync(write, definition);
        call(
                () -> service.setAttributes(SetAttributesRequest.newBuilder()
                        .setFlowId(flowId)
                        .setRunId(runId)
                        .addAttributes(write)
                        .setRequestId(UUID.randomUUID().toString())
                        .build()),
                FlowTargetRequirement.ACTIVE,
                flowId);
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
        call(
                () -> service.publishToChannel(request.build()),
                FlowTargetRequirement.ACTIVE,
                flowId);
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
        final WaitForFlowResponse response = call(
                () -> service.waitForFlow(request.build()),
                FlowTargetRequirement.EXISTING,
                flowId);
        if (response.getFlowStatus() != io.superdurable.gen.FlowStatus.FLOW_STATUS_COMPLETED) {
            final FlowInfo flow = describeFlow(flowId);
            throw new FlowUncompletedException(
                    flow.getRunId(),
                    mapFlowStatus(response.getFlowStatus()),
                    mapFlowErrorType(response.getErrorType()),
                    response.getErrorMessage().isEmpty() ? null : response.getErrorMessage(),
                    mapStepCompletions(response.getResultsList()));
        }
        return response;
    }

    private List<StepCompletion> mapStepCompletions(
            final List<io.superdurable.gen.StepCompletionOutput> outputs) {
        final List<StepCompletion> completions = new ArrayList<StepCompletion>();
        for (io.superdurable.gen.StepCompletionOutput completion
                : hydrator.hydrateStepOutputs(outputs)) {
            completions.add(new StepCompletion(
                    completion,
                    (value, outputType) -> values.decode(value, outputType)));
        }
        return completions;
    }

    FlowStartOptions mapStartOptions(final StartFlowOptions options) {
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
            final AttributeWrite.Builder write = AttributeWrite.newBuilder()
                    .setKey(key)
                    .setValue(values.encode(initialization.getValue()));
            applyAttributeSync(write, definition);
            mapped.addAttributes(write);
        }
        final FlowConfig config = options.getConfigOverride();
        if (config != null || this.options.getWorkerTarget() != null) {
            mapped.setFlowConfigOverride(mapFlowConfig(config));
        }
        return mapped.build();
    }

    io.superdurable.gen.FlowConfig mapFlowConfig(final FlowConfig config) {
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
            if (config.getAttributeStoreName() != null) {
                mapped.setAttributeSyncConfigName(config.getAttributeStoreName());
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

    private static void applyAttributeSync(
            final AttributeWrite.Builder write,
            final PersistenceDefinition definition) {
        if (definition.isSyncToAttributeStore()) {
            write.setSyncConfig(AttributeSyncConfig.newBuilder().setEnabled(true));
        }
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
        return call(rpc, FlowTargetRequirement.NONE, null);
    }

    private static <T> T call(
            final RpcCall<T> rpc,
            final FlowTargetRequirement requirement,
            final String flowId) {
        try {
            return rpc.invoke();
        } catch (StatusRuntimeException exception) {
            throw GrpcExceptionTranslator.translate(exception, requirement, flowId);
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
