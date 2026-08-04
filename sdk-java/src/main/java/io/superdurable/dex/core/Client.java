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

import io.superdurable.dex.core.exceptions.LongPollTimeoutException;
import io.superdurable.dex.core.exceptions.NoRunningWorkflowException;
import io.superdurable.dex.core.exceptions.WorkflowAlreadyStartedException;
import io.superdurable.dex.core.exceptions.WorkflowNotExistsException;
import io.superdurable.dex.core.persistence.PersistenceOptions;
import io.superdurable.dex.gen.models.*;
import io.superdurable.dex.gen.models.WorkflowStateOptions;
import net.bytebuddy.ByteBuddy;
import net.bytebuddy.implementation.MethodDelegation;
import net.bytebuddy.matcher.ElementMatchers;
import org.objenesis.Objenesis;
import org.objenesis.ObjenesisStd;

import java.lang.reflect.Constructor;
import java.lang.reflect.InvocationTargetException;
import java.util.*;
import java.util.stream.Collectors;

import static io.superdurable.dex.core.WorkflowState.shouldSkipWaitUntil;
import static io.superdurable.dex.core.mapper.StateMovementMapper.autoFillFailureProceedingStateOptions;
import static io.superdurable.dex.core.mapper.StateMovementMapper.toIdlWorkflowStateOptionsWithValidation;

public class Client {
    private final Registry registry;

    private final UnregisteredClient unregisteredClient;

    final ClientOptions clientOptions;

    /**
     * return a full-featured client. If you don't have the workflow Registry, you should use {@link UnregisteredClient} instead
     *
     * @param registry      registry is required so that this client can perform some validation checks (workflow types, channel names)
     * @param clientOptions is for configuring the client
     */
    public Client(final Registry registry, final ClientOptions clientOptions) {
        this.registry = registry;
        this.clientOptions = clientOptions;
        this.unregisteredClient = new UnregisteredClient(clientOptions);
    }

    public Client(final Registry registry, final ClientOptions clientOptions, final UnregisteredClient unregisteredClient) {
        this.registry = registry;
        this.clientOptions = clientOptions;
        this.unregisteredClient = unregisteredClient;
    }

    public UnregisteredClient getUnregisteredClient() {
        return unregisteredClient;
    }

    /**
     * startWorkflow starts a workflow execution
     *
     * @param workflowClass          is required
     * @param workflowId             is required
     * @param workflowTimeoutSeconds is required
     * @return runId
     * @throws WorkflowAlreadyStartedException if the workflow is already started
     */
    public String startWorkflow(
            final Class<? extends ObjectWorkflow> workflowClass,
            final String workflowId,
            final int workflowTimeoutSeconds) {
        return startWorkflow(workflowClass, workflowId, workflowTimeoutSeconds, null, null);
    }

    /**
     * startWorkflow starts a workflow execution
     *
     * @param workflowClass          is required
     * @param workflowId             is required
     * @param workflowTimeoutSeconds is required
     * @param input                  is optional, can be null
     * @return runId
     * @throws WorkflowAlreadyStartedException if the workflow is already started
     */
    public String startWorkflow(
            final Class<? extends ObjectWorkflow> workflowClass,
            final String workflowId,
            final int workflowTimeoutSeconds,
            final Object input) {
        return startWorkflow(workflowClass, workflowId, workflowTimeoutSeconds, input, null);
    }

    /**
     * startWorkflow starts a workflow execution
     *
     * @param workflowClass          is required
     * @param workflowId             is required
     * @param workflowTimeoutSeconds is required
     * @param input                  is optional, can be null
     * @param option                 is optional, can be null
     * @return runId
     * @throws WorkflowAlreadyStartedException if the workflow is already started.
     * If using WorkflowAlreadyStartedOptions in WorkflowOptions, the error will be ignored if ignoreAlreadyStartedError is true.
     * If ignoreAlreadyStartedError is true and also requestId is set, the requestId will be used to identify the request. The error
     * will only be thrown if the requestId is different from the requestId of the existing workflow.
     */
    public String startWorkflow(
            final Class<? extends ObjectWorkflow> workflowClass,
            final String workflowId,
            final int workflowTimeoutSeconds,
            final Object input,
            final WorkflowOptions option) {
        final String wfType = workflowClass.getSimpleName();
        return startWorkflow(wfType, workflowId, workflowTimeoutSeconds, input, option);
    }

    /**
     * startWorkflow starts a workflow execution
     *
     * @param wfType                 is required. It should be the same as the {@link ObjectWorkflow#getWorkflowType()}
     * @param workflowId             is required
     * @param workflowTimeoutSeconds is required
     * @param input                  is optional, can be null
     * @param options                is optional, can be null
     * @return runId
     * @throws WorkflowAlreadyStartedException if the workflow is already started.
     * If using WorkflowAlreadyStartedOptions in WorkflowOptions, the error will be ignored if ignoreAlreadyStartedError is true.
     * If ignoreAlreadyStartedError is true and also requestId is set, the requestId will be used to identify the request. The error
     * will only be thrown if the requestId is different from the requestId of the existing workflow.
     */
    public String startWorkflow(
            final String wfType,
            final String workflowId,
            final int workflowTimeoutSeconds,
            final Object input,
            final WorkflowOptions options) {
        checkWorkflowTypeExists(wfType);

        final ImmutableUnregisteredWorkflowOptions.Builder unregisterWorkflowOptions = ImmutableUnregisteredWorkflowOptions.builder();

        if (options != null) {
            unregisterWorkflowOptions.workflowIdReusePolicy(options.getWorkflowIdReusePolicy());
            unregisterWorkflowOptions.cronSchedule(options.getCronSchedule());
            unregisterWorkflowOptions.workflowStartDelaySeconds(options.getWorkflowStartDelaySeconds());
            unregisterWorkflowOptions.workflowRetryPolicy(options.getWorkflowRetryPolicy());
            unregisterWorkflowOptions.workflowConfigOverride(options.getWorkflowConfigOverride());
            unregisterWorkflowOptions.waitForCompletionStateExecutionIds(options.getWaitForCompletionStateExecutionIds());
            unregisterWorkflowOptions.waitForCompletionStateIds(options.getWaitForCompletionStateIds());
            unregisterWorkflowOptions.workflowAlreadyStartedOptions(options.getWorkflowAlreadyStartedOptions());

            final Map<String, SearchAttributeValueType> saTypes = registry.getSearchAttributeKeyToTypeMap(wfType);
            final List<SearchAttribute> convertedSAs = convertToSearchAttributeList(saTypes, options.getInitialSearchAttribute());
            unregisterWorkflowOptions.initialSearchAttribute(convertedSAs);
            checkInitialDataAttributes(registry.getDataAttributeTypeStore(wfType), options.getInitialDataAttribute());
            unregisterWorkflowOptions.initialDataAttribute(options.getInitialDataAttribute());
        }

        final Optional<StateDef> stateDefOptional = registry.getWorkflowStartingState(wfType);
        String startStateId = null;
        if (stateDefOptional.isPresent()) {
            StateDef stateDef = stateDefOptional.get();
            startStateId = stateDef.getWorkflowState().getStateId();

            final Class registeredInputType = stateDef.getWorkflowState().getInputType();
            if (input != null && !registeredInputType.isAssignableFrom(input.getClass())) {
                throw new WorkflowDefinitionException(String.format("input cannot be assigned to the starting state, input type: %s, starting state input type: %s", input.getClass(), registeredInputType));
            }

            WorkflowStateOptions stateOptions = toIdlWorkflowStateOptionsWithValidation(stateDef);
            if (shouldSkipWaitUntil(stateDef.getWorkflowState())) {
                if (stateOptions == null) {
                    stateOptions = new WorkflowStateOptions().skipWaitUntil(true);
                } else {
                    stateOptions.skipWaitUntil(true);
                }
            }

            autoFillFailureProceedingStateOptions(stateOptions, wfType, registry);

            if (stateOptions != null) {
                unregisterWorkflowOptions.startStateOptions(stateOptions);
            }
        }

        final PersistenceOptions schemaOptions = registry.getPersistenceOptions(wfType);
        if (schemaOptions.getEnableCaching()) {
            unregisterWorkflowOptions.usingMemoForDataAttributes(schemaOptions.getEnableCaching());
        }

        return unregisteredClient.startWorkflow(wfType, startStateId, workflowId, workflowTimeoutSeconds, input, unregisterWorkflowOptions.build());
    }

    private void checkInitialDataAttributes(final TypeStore dataAttributeTypeStore, final Map<String, Object> initialDataAttribute) {
        if (initialDataAttribute.size() > 0) {
            initialDataAttribute.forEach((key, val) -> {
                if (!dataAttributeTypeStore.isValidNameOrPrefix(key)) {
                    throw new IllegalArgumentException(String.format("data attribute %s is not registered", key));
                }
                final Class<?> registeredType = dataAttributeTypeStore.getType(key);
                final Class<?> requestedType = val.getClass();
                if (!requestedType.isAssignableFrom(registeredType)) {
                    throw new IllegalArgumentException(
                            String.format(
                                    "registered type %s is not assignable from %s",
                                    registeredType.getName(),
                                    requestedType.getName()));
                }
            });
        }
    }

    private void checkWorkflowTypeExists(String wfType) {
        final ObjectWorkflow wf = registry.getWorkflow(wfType);
        if (wf == null) {
            throw new IllegalArgumentException("Workflow " + wfType + " is not registered");
        }
    }

    private List<SearchAttribute> convertToSearchAttributeList(final Map<String, SearchAttributeValueType> saTypes, final Map<String, Object> initialSearchAttribute) {
        List<SearchAttribute> convertedSAs = new ArrayList<>();
        if (initialSearchAttribute.size() > 0) {
            initialSearchAttribute.forEach((saKey, val) -> {
                if (!saTypes.containsKey(saKey)) {
                    throw new WorkflowDefinitionException(String.format("key %s is not defined as search attribute, all keys are: %s ", saKey, saTypes.keySet()));
                }
                final SearchAttributeValueType valType = saTypes.get(saKey);
                final SearchAttribute newSa = new SearchAttribute().key(saKey).valueType(valType);
                boolean isValCorrectType = false;
                switch (valType) {
                    case INT:
                        if (val instanceof Integer) {
                            Long lVal = ((Integer) val).longValue();
                            convertedSAs.add(newSa.integerValue(lVal));
                            isValCorrectType = true;
                        }
                        if (val instanceof Long) {
                            convertedSAs.add(newSa.integerValue((Long) val));
                            isValCorrectType = true;
                        }
                        break;
                    case DOUBLE:
                        if (val instanceof Float) {
                            Double lVal = ((Float) val).doubleValue();
                            convertedSAs.add(newSa.doubleValue(lVal));
                            isValCorrectType = true;
                        }
                        if (val instanceof Double) {
                            convertedSAs.add(new SearchAttribute().doubleValue((Double) val));
                            isValCorrectType = true;
                        }
                        break;
                    case BOOL:
                        if (val instanceof Boolean) {
                            convertedSAs.add(newSa.boolValue((Boolean) val));
                            isValCorrectType = true;
                        }
                        break;
                    case KEYWORD:
                    case TEXT:
                    case DATETIME:
                        if (val instanceof String) {
                            convertedSAs.add(newSa.stringValue((String) val));
                            isValCorrectType = true;
                        }
                        break;
                    case KEYWORD_ARRAY:
                        if (val instanceof List) {
                            final List<String> listArr = (List<String>) val;
                            convertedSAs.add(newSa.stringArrayValue(listArr));
                            isValCorrectType = true;
                        }
                        break;
                    default:
                        throw new IllegalStateException("unsupported type");
                }

                if (!isValCorrectType) {
                    throw new IllegalArgumentException(String.format("search attribute value is not set correctly for key %s with value type %s", saKey, valType));
                }
            });
        }
        return convertedSAs;
    }

    /**
     * A long poll API to wait for the workflow completion.
     * Due to the limit of REST API, it will only wait for 30 seconds for the workflow to complete.
     * (configurable in ClientOptions.LongPollApiMaxWaitTimeSeconds)
     *
     * @param workflowId    required, the workflowId
     * @throws WorkflowUncompletedException if the workflow is not COMPLETED
     * @throws WorkflowNotExistsException if the workflow is not existing
     * @throws LongPollTimeoutException if the long poll timeout
     */
    public void waitForWorkflowCompletion(
            final String workflowId) {
        getComplexWorkflowResultWithWait(workflowId);
    }

    /**
     * A long poll API to wait for the workflow completion and return single result
     * This only works for a workflow only has one result(one completion state).
     * If the workflow has multiple completion states, use getComplexWorkflowResultWithWait.
     * This API to retrieve the output of the state with waiting for the workflow to complete.
     * If the workflow is not COMPLETED, throw the {@link WorkflowUncompletedException}.
     *
     * @param valueClass    required, the type class of the output
     * @param workflowId    required, the workflowId
     * @param <T>           type of the output
     * @return the output result
     * @throws WorkflowUncompletedException if the workflow is not COMPLETED
     * @throws WorkflowNotExistsException if the workflow is not existing
     * @throws LongPollTimeoutException if the long poll timeout
     */
    public <T> T waitForWorkflowCompletion(
            final Class<T> valueClass,
            final String workflowId) {
        return this.getSimpleWorkflowResultWithWait(valueClass, workflowId);
    }

    /**
     * Use {@link #waitForWorkflowCompletion(Class, String)} instead
     * It's just a renaming.
     * @param <T> type of the output
     * @param valueClass   required, the type class of the output
     * @param workflowId   required, the workflowId
     * @param workflowRunId optional, can be empty
     * @return the output result
     * @throws WorkflowUncompletedException if the workflow is not COMPLETED
     * @throws WorkflowNotExistsException if the workflow is not existing
     * @throws LongPollTimeoutException if the long poll timeout
     */
    @Deprecated
    public <T> T getSimpleWorkflowResultWithWait(
            final Class<T> valueClass,
            final String workflowId,
            final String workflowRunId) {
        return unregisteredClient.getSimpleWorkflowResultWithWait(valueClass, workflowId, workflowRunId);
    }

    /**
     * Use {@link #waitForWorkflowCompletion(Class, String)} instead
     * It's just a renaming.
     * @param <T> type of the output
     * @param valueClass   required, the type class of the output
     * @param workflowId   required, the workflowId
     * @return the output result
     * @throws WorkflowUncompletedException if the workflow is not COMPLETED
     * @throws WorkflowNotExistsException if the workflow is not existing
     * @throws LongPollTimeoutException if the long poll timeout
     */
    @Deprecated
    public <T> T getSimpleWorkflowResultWithWait(
            final Class<T> valueClass,
            final String workflowId) {
        return getSimpleWorkflowResultWithWait(valueClass, workflowId, "");
    }

    /**
     * For most cases, a workflow only has one result(one completion state).
     * Use this API to retrieve the output of the state without waiting for the workflow to complete.
     *
     * @param valueClass    required, the type class of the output
     * @param workflowId    required, the workflowId
     * @param workflowRunId optional, can be empty
     * @param <T>           type of the output
     * @return the output result
     * @throws WorkflowUncompletedException if the workflow is not COMPLETED
     * @throws WorkflowNotExistsException if the workflow is not existing
     */
    public <T> T tryGettingSimpleWorkflowResult(
            final Class<T> valueClass,
            final String workflowId,
            final String workflowRunId) {
        return unregisteredClient.tryGettingSimpleWorkflowResult(valueClass, workflowId, workflowRunId);
    }

    /**
     * For most cases, a workflow only has one result(one completion state).
     * Use this API to retrieve the output of the state without waiting for the workflow to complete.
     *
     * @param valueClass    required, the type class of the output
     * @param workflowId    required, the workflowId
     * @param <T>           type of the output
     * @return the output result
     * @throws WorkflowUncompletedException if the workflow is not COMPLETED
     * @throws WorkflowNotExistsException if the workflow is not existing
     */
    public <T> T tryGettingSimpleWorkflowResult(
            final Class<T> valueClass,
            final String workflowId) {
        return tryGettingSimpleWorkflowResult(valueClass, workflowId, "");
    }

    /**
     * In some cases, a workflow may have more than one completion states.
     * Use this API to retrieve the output of the states with waiting for the workflow to complete.
     *
     * @param workflowId    required, the workflowId
     * @param workflowRunId optional, can be empty
     * @return a list of the state output for completion states. User code will figure how to use ObjectEncoder to decode the output
     * @throws WorkflowUncompletedException if the workflow is not COMPLETED
     * @throws WorkflowNotExistsException if the workflow is not existing
     * @throws LongPollTimeoutException if the long poll timeout
     */
    public List<StateCompletionOutput> getComplexWorkflowResultWithWait(
            final String workflowId, final String workflowRunId) {
        return unregisteredClient.getComplexWorkflowResultWithWait(workflowId, workflowRunId);
    }

    /**
     * In some cases, a workflow may have more than one completion states.
     * Use this API to retrieve the output of the states with waiting for the workflow to complete.
     *
     * @param workflowId    required, the workflowId
     * @return a list of the state output for completion states. User code will figure how to use ObjectEncoder to decode the output
     * @throws WorkflowUncompletedException if the workflow is not COMPLETED
     * @throws WorkflowNotExistsException if the workflow is not existing
     * @throws LongPollTimeoutException if the long poll timeout
     */
    public List<StateCompletionOutput> getComplexWorkflowResultWithWait(final String workflowId) {
        return getComplexWorkflowResultWithWait(workflowId, "");
    }

    /**
     * In some cases, a workflow may have more than one completion states.
     * Use this API to retrieve the output of the states without waiting for the workflow to complete.
     *
     * @param workflowId    required, the workflowId
     * @param workflowRunId optional, can be empty
     * @return a list of the state output for completion states. User code will figure how to use ObjectEncoder to decode the output
     * @throws WorkflowUncompletedException if the workflow is not COMPLETED
     * @throws WorkflowNotExistsException if the workflow is not existing
     */
    public List<StateCompletionOutput> tryGettingComplexWorkflowResult(
            final String workflowId, final String workflowRunId) {
        return unregisteredClient.tryGettingComplexWorkflowResult(workflowId, workflowRunId);
    }

    /**
     * In some cases, a workflow may have more than one completion states.
     * Use this API to retrieve the output of the states without waiting for the workflow to complete.
     *
     * @param workflowId    required, the workflowId
     * @return a list of the state output for completion states. User code will figure how to use ObjectEncoder to decode the output
     * @throws WorkflowUncompletedException if the workflow is not COMPLETED
     * @throws WorkflowNotExistsException if the workflow is not existing
     */
    public List<StateCompletionOutput> tryGettingComplexWorkflowResult(final String workflowId) {
        return tryGettingComplexWorkflowResult(workflowId, "");
    }

    /**
     * Emit a signal message for the workflow object to receive from external sources
     *
     * @param workflowClass     required
     * @param workflowId        required
     * @param workflowRunId     optional, can be empty
     * @param signalChannelName required
     * @param signalValue       optional, can be null
     * @throws NoRunningWorkflowException  if the workflow is not existing or not running
     */
    public void signalWorkflow(
            final Class<? extends ObjectWorkflow> workflowClass,
            final String workflowId,
            final String workflowRunId,
            final String signalChannelName,
            final Object signalValue) {
        final String wfType = workflowClass.getSimpleName();

        checkWorkflowTypeExists(wfType);

        final Class<?> signalType = registry.getSignalChannelTypeStore(wfType).getType(signalChannelName);

        if (signalValue != null && !signalType.isInstance(signalValue)) {
            throw new IllegalArgumentException(String.format("Signal value is not of type %s", signalType.getName()));
        }

        unregisteredClient.signalWorkflow(workflowId, workflowRunId, signalChannelName, signalValue);
    }

    /**
     * Emit a signal message for the workflow object to receive from external sources
     *
     * @param workflowClass     required
     * @param workflowId        required
     * @param signalChannelName required
     * @param signalValue       optional, can be null
     * @throws NoRunningWorkflowException  if the workflow is not existing or not running
     */
    public void signalWorkflow(
            final Class<? extends ObjectWorkflow> workflowClass,
            final String workflowId,
            final String signalChannelName,
            final Object signalValue) {
        signalWorkflow(workflowClass, workflowId, "", signalChannelName, signalValue);
    }

    /**
     * Send a single empty message to internalChannel
     *
     * @param workflowClass     required
     * @param workflowId        required
     * @param internalChannelName required
     * @throws NoRunningWorkflowException  if the workflow is not existing or not running
     */
    public void publishToInternalChannel(
            final Class<? extends ObjectWorkflow> workflowClass,
            final String workflowId,
            final String internalChannelName) {
        publishToInternalChannel(workflowClass, workflowId, "", internalChannelName, null);
    }

    /**
     * Send a single message to internalChannel
     *
     * @param workflowClass     required
     * @param workflowId        required
     * @param internalChannelName required
     * @param channelMessage       optional, can be null.
     * @throws NoRunningWorkflowException  if the workflow is not existing or not running
     */
    public void publishToInternalChannel(
            final Class<? extends ObjectWorkflow> workflowClass,
            final String workflowId,
            final String internalChannelName,
            final Object channelMessage) {
        publishToInternalChannel(workflowClass, workflowId, "", internalChannelName, channelMessage);
    }

    /**
     * Send a single message to internalChannel
     *
     * @param workflowClass     required
     * @param workflowId        required
     * @param workflowRunId     optional, can be empty
     * @param internalChannelName required
     * @param channelMessage       optional, can be null.
     * @throws NoRunningWorkflowException  if the workflow is not existing or not running
     */
    public void publishToInternalChannel(
            final Class<? extends ObjectWorkflow> workflowClass,
            final String workflowId,
            final String workflowRunId,
            final String internalChannelName,
            final Object channelMessage) {
        publishToInternalChannelBatch(workflowClass, workflowId, workflowRunId, internalChannelName, Arrays.asList(channelMessage));
    }

    /**
     * Send a batch of messages to internalChannel
     *
     * @param workflowClass     required
     * @param workflowId        required
     * @param workflowRunId     optional, can be empty
     * @param internalChannelName required
     * @param channelMessages       messages in batch
     * @throws NoRunningWorkflowException  if the workflow is not existing or not running
     */
    public void publishToInternalChannelBatch(
            final Class<? extends ObjectWorkflow> workflowClass,
            final String workflowId,
            final String workflowRunId,
            final String internalChannelName,
            final List<Object> channelMessages) {
        final String wfType = workflowClass.getSimpleName();

        checkWorkflowTypeExists(wfType);

        final Class<?> channelValueType = registry.getInternalChannelTypeStore(wfType).getType(internalChannelName);

        List<InterStateChannelPublishing> rawMessages = new ArrayList<>(channelMessages.size());
        for (Object channelValue : channelMessages) {
            if (channelValue != null && !channelValueType.isInstance(channelValue)) {
                throw new IllegalArgumentException(String.format("message value is not of channel type %s", channelValueType.getName()));
            }
            rawMessages.add(
                    new InterStateChannelPublishing()
                            .channelName(internalChannelName)
                            .value(clientOptions.getObjectEncoder().encode(channelValue))
            );
        }

        unregisteredClient.publishToInternalChannel(workflowId, workflowRunId, rawMessages);
    }

    /**
     * @param workflowId                    required
     * @param workflowRunId                 optional, can be empty
     * @param resetWorkflowTypeAndOptions   required, the combination parameter for reset
     * @return the new runId after reset
     * @throws WorkflowNotExistsException  if the workflow is not existing
     */
    public String resetWorkflow(
            final String workflowId,
            final String workflowRunId,
            final ResetWorkflowTypeAndOptions resetWorkflowTypeAndOptions
    ) {
        return unregisteredClient.resetWorkflow(workflowId, workflowRunId, resetWorkflowTypeAndOptions);
    }

    /**
     * @param workflowId                    required
     * @param resetWorkflowTypeAndOptions   required, the combination parameter for reset
     * @return the new runId after reset
     * @throws WorkflowNotExistsException  if the workflow is not existing
     */
    public String resetWorkflow(
            final String workflowId,
            final ResetWorkflowTypeAndOptions resetWorkflowTypeAndOptions
    ) {
        return resetWorkflow(workflowId, "", resetWorkflowTypeAndOptions);
    }

    /**
     * Stop a workflow with options
     *
     * @param workflowId    required
     * @param workflowRunId optional, can be empty
     * @param options       optional, can be null. If not set, the workflow status will be CANCELED
     * @throws NoRunningWorkflowException  if the workflow is not existing or not running
     */
    public void stopWorkflow(
            final String workflowId,
            final String workflowRunId,
            final StopWorkflowOptions options) {
        unregisteredClient.stopWorkflow(workflowId, workflowRunId, options);
    }

    /**
     * Stop a workflow with options
     *
     * @param workflowId    required
     * @param options       optional, can be null. If not set, the workflow status will be CANCELED
     * @throws NoRunningWorkflowException  if the workflow is not existing or not running
     */
    public void stopWorkflow(
            final String workflowId,
            final StopWorkflowOptions options) {
        stopWorkflow(workflowId, "", options);
    }

    /**
     * Stop a workflow, this is essentially terminate the workflow gracefully
     * The workflow status will be CANCELED
     *
     * @param workflowId    required
     * @throws NoRunningWorkflowException  if the workflow is not existing or not running
     */
    public void stopWorkflow(final String workflowId) {
        stopWorkflow(workflowId, "", null);
    }

    /**
     * Get specified data attributes (by keys) of a workflow
     *
     * @param workflowClass     required
     * @param workflowId        required
     * @param workflowRunId     optional, can be empty
     * @param keys              required, cannot be empty or null
     * @return the data attributes
     * @throws WorkflowNotExistsException  if the workflow is not existing
     */
    public Map<String, Object> getWorkflowDataAttributes(
            final Class<? extends ObjectWorkflow> workflowClass,
            final String workflowId,
            final String workflowRunId,
            final List<String> keys) {

        if (keys == null || keys.isEmpty()) {
            throw new IllegalArgumentException("keys must contain at least one entry, or use getAllDataAttributes API to get all");
        }
        return doGetWorkflowDataAttributes(workflowClass, workflowId, workflowRunId, keys);
    }

    /**
     * Get specified data attributes (by keys) of a workflow
     *
     * @param workflowClass     required
     * @param workflowId        required
     * @param keys              required, cannot be empty or null
     * @return the data attributes
     * @throws WorkflowNotExistsException  if the workflow is not existing
     */
    public Map<String, Object> getWorkflowDataAttributes(
            final Class<? extends ObjectWorkflow> workflowClass,
            final String workflowId,
            final List<String> keys) {
        return getWorkflowDataAttributes(workflowClass, workflowId, "", keys);
    }

    /**
     * Get all the data attributes of a workflow
     *
     * @param workflowClass     required
     * @param workflowId        required
     * @param workflowRunId     optional, can be empty
     * @return the data attributes
     * @throws WorkflowNotExistsException  if the workflow is not existing
     */
    public Map<String, Object> getAllDataAttributes(
            final Class<? extends ObjectWorkflow> workflowClass,
            final String workflowId,
            final String workflowRunId) {
        return doGetWorkflowDataAttributes(workflowClass, workflowId, workflowRunId, null);
    }

    /**
     * Get all the data attributes of a workflow
     *
     * @param workflowClass     required
     * @param workflowId        required
     * @return the data attributes
     * @throws WorkflowNotExistsException  if the workflow is not existing
     */
    public Map<String, Object> getAllDataAttributes(
            final Class<? extends ObjectWorkflow> workflowClass,
            final String workflowId) {
        return getAllDataAttributes(workflowClass, workflowId, "");
    }

    private Map<String, Object> doGetWorkflowDataAttributes(
            final Class<? extends ObjectWorkflow> workflowClass,
            final String workflowId,
            final String workflowRunId,
            final List<String> keys) {
        final String wfType = workflowClass.getSimpleName();
        checkWorkflowTypeExists(wfType);

        final TypeStore dataAttributeTypeStore = registry.getDataAttributeTypeStore(wfType);

        // if attribute keys is null or empty, dex server will return all data attributes
        if (keys != null && !keys.isEmpty()) {
            final Optional<String> first = keys.stream()
                    .filter(key -> !dataAttributeTypeStore.isValidNameOrPrefix(key))
                    .findFirst();
            if (first.isPresent()) {
                throw new IllegalArgumentException(
                        String.format(
                                "data attribute not registered: %s",
                                first.get()
                        )
                );
            }
        }

        final PersistenceOptions schemaOptions = registry.getPersistenceOptions(wfType);

        final WorkflowGetDataObjectsResponse response = unregisteredClient.getAnyWorkflowDataObjects(workflowId, workflowRunId, keys, schemaOptions.getEnableCaching());

        if (response.getObjects() == null) {
            throw new IllegalStateException("data attributes not returned");
        }

        final Map<String, Object> result = new HashMap<>();
        for (final KeyValue keyValue : response.getObjects()) {
            if (keyValue.getValue() != null) {
                result.put(
                        keyValue.getKey(),
                        clientOptions.getObjectEncoder().decode(
                                keyValue.getValue(),
                                dataAttributeTypeStore.getType(keyValue.getKey())
                        )
                );
            }
        }
        return result;
    }

    /**
     * Set the value for data attributes aka objects for a workflow
     *
     * @param workflowClass  required
     * @param workflowId     required
     * @param workflowRunId  optional, can be empty
     * @param dataAttributes required
     * @throws NoRunningWorkflowException  if the workflow is not existing or not running
     * */
    public void setWorkflowDataAttributes(
            final Class<? extends ObjectWorkflow> workflowClass,
            final String workflowId,
            final String workflowRunId,
            final Map<String, Object> dataAttributes) {
        doSetWorkflowDataAttributes(workflowClass, workflowId, workflowRunId, dataAttributes);
    }

    /**
     * Set the value for data attributes aka objects for a workflow
     *
     * @param workflowClass  required
     * @param workflowId     required
     * @param dataAttributes required
     * @throws NoRunningWorkflowException  if the workflow is not existing or not running
     * */
    public void setWorkflowDataAttributes(
            final Class<? extends ObjectWorkflow> workflowClass,
            final String workflowId,
            final Map<String, Object> dataAttributes) {
        doSetWorkflowDataAttributes(workflowClass, workflowId, "", dataAttributes);
    }

    private void doSetWorkflowDataAttributes(
            final Class<? extends ObjectWorkflow> workflowClass,
            final String workflowId,
            final String workflowRunId,
            final Map<String, Object> dataAttributes
    ) {
        //check if workflow type exists
        final String wfType = workflowClass.getSimpleName();
        checkWorkflowTypeExists(wfType);

        for (final Map.Entry<String, Object> entry : dataAttributes.entrySet()) {
            final String key = entry.getKey();
            //check that key exists in the store
            if (!registry.getDataAttributeTypeStore(wfType).isValidNameOrPrefix(key)) {
                throw new IllegalArgumentException(String.format("data attribute %s is not registered", key));
            }

            final Class<?> registeredType = registry.getDataAttributeTypeStore(wfType).getType(key);
            final Class<?> requestedType = entry.getValue().getClass();
            //check that type is registered in schema
            if (!requestedType.isAssignableFrom(registeredType)) {
                throw new IllegalArgumentException(
                        String.format(
                                "registered type %s is not assignable from %s",
                                registeredType.getName(),
                                requestedType.getName()));
            }
        }

        unregisteredClient.setAnyWorkflowDataObjects(workflowId, workflowRunId, dataAttributes);
    }

    /**
     * This is a simplified API to search without pagination, use the other searchWorkflow API for pagination feature
     *
     * @param query    the query of the search, see Cadence/Temporal search attributes doc
     * @param pageSize the page size
     * @return the results of the search, this will only return one page of the results
     */
    public WorkflowSearchResponse searchWorkflow(final String query, final int pageSize) {
        return unregisteredClient.searchWorkflow(query, pageSize);
    }

    /**
     * This search API support pagination
     *
     * @param request the search request
     * @return the results of the search
     */
    public WorkflowSearchResponse searchWorkflow(final WorkflowSearchRequest request) {
        return unregisteredClient.searchWorkflow(request);
    }

    /**
     * create a new stub for invoking RPC
     *
     * @param workflowClassForRpc the class of defining the RPCs to invoke
     * @param workflowId          required
     * @param workflowRunId       optional, can be empty
     * @param <T>                 the class of defining the RPCs to invoke
     * @return the result of the RPC
     */
    public <T> T newRpcStub(Class<T> workflowClassForRpc, String workflowId, String workflowRunId) {

        final String wfType = workflowClassForRpc.getSimpleName();
        final PersistenceOptions schemaOptions = registry.getPersistenceOptions(wfType);
        final Map<String, SearchAttributeValueType> searchAttributeKeyToTypeMap = registry.getSearchAttributeKeyToTypeMap(wfType);
        List<SearchAttributeKeyAndType> keyAndTypes = new ArrayList<>();

        searchAttributeKeyToTypeMap.forEach((key, type) -> {
            final SearchAttributeKeyAndType keyAndType = new SearchAttributeKeyAndType()
                    .key(key)
                    .valueType(type);
            keyAndTypes.add(keyAndType);
        });

        Class<?> dynamicType = new ByteBuddy()
                .subclass(workflowClassForRpc)
                .method(ElementMatchers.any())
                .intercept(MethodDelegation.to(new RpcInvocationHandler(this.unregisteredClient, workflowId, workflowRunId, schemaOptions, keyAndTypes)))
                .make()
                .load(getClass().getClassLoader())
                .getLoaded();

        Objenesis objenesis = new ObjenesisStd();
        return (T) objenesis.newInstance(dynamicType);
    }

    /**
     * create a new stub for invoking RPC
     *
     * @param workflowClassForRpc the class of defining the RPCs to invoke
     * @param workflowId          required
     * @param <T>                 the class of defining the RPCs to invoke
     * @return the result of the RPC
     */
    public <T> T newRpcStub(Class<T> workflowClassForRpc, String workflowId) {
        return newRpcStub(workflowClassForRpc, workflowId, "");
    }

    /**
     * invoking the RPC through RPC stub
     *
     * @param rpcStubMethod the RPC method from stub created by {@link #newRpcStub(Class, String, String)}
     * @param input         the input of the RPC method
     * @param <I>           the input type
     * @param <O>           the output type
     * @return output
     * @throws WorkflowNotExistsException  if the workflow is not existing, or not running to accept a write operation in RPC
     */
    public <I, O> O invokeRPC(RpcDefinitions.RpcFunc1<I, O> rpcStubMethod, I input) {
        return rpcStubMethod.execute(null, input, null, null);
    }

    /**
     * invoking the RPC through RPC stub
     *
     * @param rpcStubMethod the RPC method from stub created by {@link #newRpcStub(Class, String, String)}
     * @param input         the input of the RPC method
     * @param <I>           the input type
     * @param <O>           the output type
     * @return              output
     * @throws WorkflowNotExistsException  if the workflow is not existing, or not running to accept a write operation in RPC
     */
    public <I, O> O invokeRPC(RpcDefinitions.RpcFunc1NoPersistence<I, O> rpcStubMethod, I input) {
        return rpcStubMethod.execute(null, input, null);
    }

    /**
     * invoking the RPC through RPC stub
     *
     * @param rpcStubMethod the RPC method from stub created by {@link #newRpcStub(Class, String, String)}
     * @param <O>           the output type
     * @return output
     * @throws WorkflowNotExistsException  if the workflow is not existing, or not running to accept a write operation in RPC
     */
    public <O> O invokeRPC(RpcDefinitions.RpcFunc0<O> rpcStubMethod) {
        return rpcStubMethod.execute(null, null, null);
    }

    /**
     * invoking the RPC through RPC stub
     *
     * @param rpcStubMethod the RPC method from stub created by {@link #newRpcStub(Class, String, String)}
     * @param <O>           the output type
     * @return output
     * @throws WorkflowNotExistsException  if the workflow is not existing, or not running to accept a write operation in RPC
     */
    public <O> O invokeRPC(RpcDefinitions.RpcFunc0NoPersistence<O> rpcStubMethod) {
        return rpcStubMethod.execute(null, null);
    }

    /**
     * invoking the RPC through RPC stub
     *
     * @param rpcStubMethod the RPC method from stub created by {@link #newRpcStub(Class, String, String)}
     * @param input         the input of the RPC method
     * @param <I>           the input type
     * @throws WorkflowNotExistsException  if the workflow is not existing, or not running to accept a write operation in RPC
     */
    public <I> void invokeRPC(RpcDefinitions.RpcProc1<I> rpcStubMethod, I input) {
        rpcStubMethod.execute(null, input, null, null);
    }

    /**
     * invoking the RPC through RPC stub
     *
     * @param rpcStubMethod the RPC method from stub created by {@link #newRpcStub(Class, String, String)}
     * @param input         the input of the RPC method
     * @param <I>           the input type
     * @throws WorkflowNotExistsException  if the workflow is not existing, or not running to accept a write operation in RPC
     */
    public <I> void invokeRPC(RpcDefinitions.RpcProc1NoPersistence<I> rpcStubMethod, I input) {
        rpcStubMethod.execute(null, input, null);
    }

    /**
     * invoking the RPC through RPC stub
     *
     * @param rpcStubMethod the RPC method from stub created by {@link #newRpcStub(Class, String, String)}
     * @throws WorkflowNotExistsException  if the workflow is not existing, or not running to accept a write operation in RPC
     */
    public void invokeRPC(RpcDefinitions.RpcProc0 rpcStubMethod) {
        rpcStubMethod.execute(null, null, null);
    }

    /**
     * invoking the RPC through RPC stub
     *
     * @param rpcStubMethod the RPC method from stub created by {@link #newRpcStub(Class, String, String)}
     * @throws WorkflowNotExistsException  if the workflow is not existing, or not running to accept a write operation in RPC
     */
    public void invokeRPC(RpcDefinitions.RpcProc0NoPersistence rpcStubMethod) {
        rpcStubMethod.execute(null, null);
    }

    /**
     * Get specified search attributes (by attributeKeys) of a workflow
     *
     * @param workflowClass     required
     * @param workflowId        required
     * @param workflowRunId     optional, can be empty
     * @param attributeKeys     required, cannot be empty or null
     * @return the search attributes
     * @throws WorkflowNotExistsException  if the workflow is not existing
     */
    public Map<String, Object> getWorkflowSearchAttributes(
            final Class<? extends ObjectWorkflow> workflowClass,
            final String workflowId,
            final String workflowRunId,
            List<String> attributeKeys) {
        if (attributeKeys == null || attributeKeys.isEmpty()) {
            throw new IllegalArgumentException("attributeKeys must contain at least one entry, or use GetAllSearchAttributes API to get all");
        }
        return doGetWorkflowSearchAttributes(workflowClass, workflowId, workflowRunId, attributeKeys);
    }

    /**
     * Get specified search attributes (by attributeKeys) of a workflow
     *
     * @param workflowClass     required
     * @param workflowId        required
     * @param attributeKeys     required, cannot be empty or null
     * @return the search attributes
     * @throws WorkflowNotExistsException  if the workflow is not existing
     */
    public Map<String, Object> getWorkflowSearchAttributes(
            final Class<? extends ObjectWorkflow> workflowClass,
            final String workflowId,
            List<String> attributeKeys) {
        return getWorkflowSearchAttributes(workflowClass, workflowId, "", attributeKeys);
    }

    /**
     * Get all the search attributes of a workflow
     *
     * @param workflowClass     required
     * @param workflowId        required
     * @param workflowRunId     optional, can be empty
     * @return the search attributes
     * @throws WorkflowNotExistsException  if the workflow is not existing
     */
    public Map<String, Object> getAllSearchAttributes(
            final Class<? extends ObjectWorkflow> workflowClass,
            final String workflowId,
            final String workflowRunId) {
        return doGetWorkflowSearchAttributes(workflowClass, workflowId, workflowRunId, null);
    }

    /**
     * Set the value of search attributes for a workflow
     *
     * @param workflowClass    required
     * @param workflowId       required
     * @param searchAttributes required
     * @throws NoRunningWorkflowException if the workflow is not existing or not running
     * */
    public void setWorkflowSearchAttributes(
            final Class<? extends ObjectWorkflow> workflowClass,
            final String workflowId,
            final List<SearchAttribute> searchAttributes) {
        doSetWorkflowSearchAttributes(workflowClass, workflowId, "", searchAttributes);
    }

    /**
     * Set the value of search attributes for a workflow
     *
     * @param workflowClass    required
     * @param workflowId       required
     * @param workflowRunId    optional, can be empty
     * @param searchAttributes required
     * @throws NoRunningWorkflowException if the workflow is not existing or not running
     * */
    public void setWorkflowSearchAttributes(
            final Class<? extends ObjectWorkflow> workflowClass,
            final String workflowId,
            final String workflowRunId,
            final List<SearchAttribute> searchAttributes) {
        doSetWorkflowSearchAttributes(workflowClass, workflowId, workflowRunId, searchAttributes);
    }

    /**
     * Get all the search attributes of a workflow
     *
     * @param workflowClass     required
     * @param workflowId        required
     * @return the search attributes
     * @throws WorkflowNotExistsException  if the workflow is not existing
     */
    public Map<String, Object> getAllSearchAttributes(
            final Class<? extends ObjectWorkflow> workflowClass,
            final String workflowId) {
        return getAllSearchAttributes(workflowClass, workflowId, "");
    }

    /**
     * Describe a workflow to get its info.
     * If the workflow does not exist, throw the WORKFLOW_NOT_EXISTS_SUB_STATUS exception.
     *
     * @param workflowId    required
     * @param workflowRunId optional, can be empty
     * @return the workflow's info
     * @throws WorkflowNotExistsException  if the workflow is not existing
     */
    public WorkflowInfo describeWorkflow(
            final String workflowId,
            final String workflowRunId) {
        final WorkflowGetResponse response = unregisteredClient.getWorkflow(workflowId, workflowRunId, false);
        return WorkflowInfo.builder()
                .workflowStatus(response.getWorkflowStatus())
                .build();
    }

    /**
     * Describe a workflow to get its info.
     * If the workflow does not exist, throw the WORKFLOW_NOT_EXISTS_SUB_STATUS exception.
     *
     * @param workflowId    required
     * @return the workflow's info
     * @throws WorkflowNotExistsException  if the workflow is not existing
     */
    public WorkflowInfo describeWorkflow(
            final String workflowId) {
        return describeWorkflow(workflowId, "");
    }

    private Map<String, Object> doGetWorkflowSearchAttributes(
            final Class<? extends ObjectWorkflow> workflowClass,
            final String workflowId,
            final String workflowRunId,
            final List<String> attributeKeys) {
        final String wfType = workflowClass.getSimpleName();
        checkWorkflowTypeExists(wfType);

        final Map<String, SearchAttributeValueType> searchAttributeKeyToTypeMap = registry.getSearchAttributeKeyToTypeMap(wfType);

        // if attribute keys is null or empty, dex server will return all search attributes
        if (attributeKeys != null && !attributeKeys.isEmpty()) {
            List<String> nonExistingSearchAttributeList = attributeKeys.stream()
                    .filter(s -> !searchAttributeKeyToTypeMap.containsKey(s))
                    .collect(Collectors.toList());

            if (!nonExistingSearchAttributeList.isEmpty()) {
                throw new IllegalArgumentException(
                        String.format(
                                "Search attributes not registered: %s",
                                String.join(", ", nonExistingSearchAttributeList)
                        )
                );
            }
        }

        List<SearchAttributeKeyAndType> keyAndTypes = new ArrayList<>();
        if (attributeKeys == null) {
            searchAttributeKeyToTypeMap.forEach((key, type) -> {
                final SearchAttributeKeyAndType keyAndType = new SearchAttributeKeyAndType()
                        .key(key)
                        .valueType(type);
                keyAndTypes.add(keyAndType);
            });
        } else {
            attributeKeys.forEach((key) -> {
                final SearchAttributeValueType saType = searchAttributeKeyToTypeMap.get(key);
                final SearchAttributeKeyAndType keyAndType = new SearchAttributeKeyAndType()
                        .key(key)
                        .valueType(saType);
                keyAndTypes.add(keyAndType);
            });
        }

        WorkflowGetSearchAttributesResponse response = unregisteredClient.getAnyWorkflowSearchAttributes(workflowId, workflowRunId, keyAndTypes);

        if (response.getSearchAttributes() == null) {
            throw new IllegalStateException("search attributes not returned");
        }
        Map<String, Object> result = new HashMap<>();
        for (SearchAttribute searchAttribute : response.getSearchAttributes()) {
            final SearchAttributeValueType saType = searchAttributeKeyToTypeMap.get(searchAttribute.getKey());
            Object value = getSearchAttributeValue(saType, searchAttribute);
            result.put(searchAttribute.getKey(), value);
        }
        return result;
    }

    private void doSetWorkflowSearchAttributes(
            final Class<? extends ObjectWorkflow> workflowClass,
            final String workflowId,
            final String workflowRunId,
            final List<SearchAttribute> searchAttributes
    ) {
        final String wfType = workflowClass.getSimpleName();
        checkWorkflowTypeExists(wfType);

        final Map<String, SearchAttributeValueType> searchAttributeKeyToTypeMap = registry.getSearchAttributeKeyToTypeMap(wfType);
        //Check that the requested sa type is registered to the key
        searchAttributes.forEach(sa -> {
            final SearchAttributeValueType registeredValueType = searchAttributeKeyToTypeMap.get(sa.getKey());
            if (sa.getValueType() != null && registeredValueType != null && !registeredValueType.equals(sa.getValueType())) {
                throw new IllegalArgumentException(String.format("Search attribute key, %s is registered to type %s, but tried to add search attribute type %s", sa.getKey(), registeredValueType.getValue(), sa.getValueType().getValue()));
            }
        });
        unregisteredClient.setAnyWorkflowSearchAttributes(workflowId, workflowRunId, searchAttributes);
    }

    static Object getSearchAttributeValue(final SearchAttributeValueType saType, final SearchAttribute searchAttribute) {
        switch (saType) {
            case INT:
                return searchAttribute.getIntegerValue();
            case DOUBLE:
                return searchAttribute.getDoubleValue();
            case BOOL:
                return searchAttribute.getBoolValue();
            case KEYWORD:
            case TEXT:
            case DATETIME:
                return searchAttribute.getStringValue();
            case KEYWORD_ARRAY:
                return searchAttribute.getStringArrayValue();
            default:
                throw new IllegalStateException("unsupported type");
        }
    }

    public void skipTimer(
            final String workflowId,
            final String workflowRunId,
            final Class<? extends WorkflowState> stateClass,
            final int stateExecutionNumber,
            final String timerCommandId) {
        skipTimer(workflowId, workflowRunId, stateClass.getSimpleName(), stateExecutionNumber, timerCommandId);
    }

    public void skipTimer(
            final String workflowId,
            final String workflowRunId,
            final String workflowStateId,
            final int stateExecutionNumber,
            final String timerCommandId) {
        unregisteredClient.skipTimer(workflowId, workflowRunId, workflowStateId, stateExecutionNumber, timerCommandId);
    }

    public void skipTimer(
            final String workflowId,
            final String workflowRunId,
            final Class<? extends WorkflowState> stateClass,
            final int stateExecutionNumber,
            final int timerCommandIndex) {
        skipTimer(workflowId, workflowRunId, stateClass.getSimpleName(), stateExecutionNumber, timerCommandIndex);
    }

    public void skipTimer(
            final String workflowId,
            final String workflowRunId,
            final String workflowStateId,
            final int stateExecutionNumber,
            final int timerCommandIndex) {
        unregisteredClient.skipTimer(workflowId, workflowRunId, workflowStateId, stateExecutionNumber, timerCommandIndex);
    }

    /**
     * A long poll API to wait for the completion of the state. This only waits for the first completion.
     * Note 1 The stateCompletion to wait for is needed to registered on starting workflow due to limitation in https://github.com/superdurable/dex/issues/349
     * Note 2 The max polling time is configured as clientOptions as the Feign client timeout(default to 10s)
     * @param workflowId the workflowId
     * @param stateClass the state class.
     * @throws LongPollTimeoutException if the long poll timeout
     * @throws WorkflowNotExistsException if the workflow is not existing
     */
    public void waitForStateExecutionCompletion(
            final String workflowId,
            final Class<? extends WorkflowState> stateClass) {
        this.waitForStateExecutionCompletion(workflowId, stateClass, 1);
    }

    /**
     * A long poll API to wait for the completion of the state. This only waits for the first completion.
     * Note 1 The stateCompletion and stateExecutionNumber to wait for must be registered on starting workflow due to limitation in https://github.com/superdurable/dex/issues/349
     * Note 2 The max polling time is configured as clientOptions as the Feign client timeout(default to 10s)
     * If the state is not COMPLETED, throw the {@link ClientSideException} with the sub status of {@link ErrorSubStatus#LONG_POLL_TIME_OUT_SUB_STATUS}
     * @param workflowId the workflowId
     * @param stateClass the state class
     * @param waitForKey key provided by the client and to identity workflow
     * @throws LongPollTimeoutException if the long poll timeout
     * @throws WorkflowNotExistsException if the workflow is not existing
     */
    public void waitForStateExecutionCompletion(
            final String workflowId,
            final Class<? extends WorkflowState> stateClass,
            final String waitForKey) {
        final String stateId = WorkflowState.getDefaultStateId(stateClass);
        unregisteredClient.waitForStateExecutionCompletion(workflowId, stateId, waitForKey);
    }

    /**
     * A long poll API to wait for the completion of the state. This only waits for the first completion.
     * Note 1 The stateCompletion and stateExecutionNumber to wait for must be registered on starting workflow due to limitation in https://github.com/superdurable/dex/issues/349
     * Note 2 The max polling time is configured as clientOptions as the Feign client timeout(default to 10s)
     * If the state is not COMPLETED, throw the {@link ClientSideException} with the sub status of {@link ErrorSubStatus#LONG_POLL_TIME_OUT_SUB_STATUS}
     * @param workflowId the workflowId
     * @param stateClass the state class
     * @param stateExecutionNumber the state execution number. E.g. if it's 2, it means the 2nd execution of the state
     * @throws LongPollTimeoutException if the long poll timeout
     * @throws WorkflowNotExistsException if the workflow is not existing
     */
    public void waitForStateExecutionCompletion(
            final String workflowId,
            final Class<? extends WorkflowState> stateClass,
            final int stateExecutionNumber) {
        final String stateExecutionId = WorkflowState.getStateExecutionId(stateClass, stateExecutionNumber);
        unregisteredClient.waitForStateExecutionCompletion(workflowId, stateExecutionId);
    }
}
