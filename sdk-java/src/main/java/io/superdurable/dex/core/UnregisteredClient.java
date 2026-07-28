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

package io.superdurable.dex.core;

import com.google.common.base.Preconditions;
import com.google.common.collect.ImmutableList;
import feign.Feign;
import feign.FeignException;
import feign.Retryer;
import io.superdurable.dex.core.validator.CronScheduleValidator;
import io.superdurable.dex.gen.api.ApiClient;
import io.superdurable.dex.gen.api.DefaultApi;
import io.superdurable.dex.gen.models.*;

import java.util.List;
import java.util.Map;
import java.util.stream.Collectors;

/**
 * UntypedClient will let you invoke the APIs to Dex server without much type validation checks(workflow type, signalChannelName, etc).
 * It's useful for calling Client APIs without workflow registry(which may require to have all the workflow dependencies)
 */
public class UnregisteredClient {
    private final DefaultApi defaultApi;

    private final ClientOptions clientOptions;

    private WorkflowRpcRequest outgoingWorkflowRpcRequest;

    // for testing purpose
    public WorkflowRpcRequest getLastOutgoingWorkflowRpcRequest() {
        return this.outgoingWorkflowRpcRequest;
    }

    public UnregisteredClient(final ClientOptions clientOptions) {
        this.clientOptions = clientOptions;

        final ApiClient apiClient = new ApiClient()
                .setBasePath(clientOptions.getServerUrl());
        apiClient.setObjectMapper(clientOptions.getObjectEncoder().getObjectMapper());

        final ServiceApiRetryConfig apiRetryConfig = clientOptions.getServiceApiRetryConfig();
        final Feign.Builder feignBuilder = apiClient.getFeignBuilder();
        feignBuilder.retryer(
                new Retryer.Default(apiRetryConfig.getInitialIntervalMills(), apiRetryConfig.getMaximumIntervalMills(), apiRetryConfig.getMaximumAttempts())
        );
        feignBuilder.errorDecoder(new ServerErrorRetryDecoder());
        apiClient.setFeignBuilder(feignBuilder);
        feignBuilder.requestInterceptor(
                (requestTemplate) -> clientOptions.getRequestHeaders().forEach(requestTemplate::header)
        );

        this.defaultApi = apiClient.buildClient(DefaultApi.class);
    }
    
    public UnregisteredClient(final ClientOptions clientOptions, final DefaultApi openApiClient) {
        this.clientOptions = clientOptions;
        this.defaultApi = openApiClient;
    }

    /**
     * @param workflowType           required
     * @param startStateId           required
     * @param workflowId             required
     * @param workflowTimeoutSeconds required
     * @return runId
     */
    public String startWorkflow(
            final String workflowType,
            final String startStateId,
            final String workflowId,
            final int workflowTimeoutSeconds) {
        return this.startWorkflow(workflowType, startStateId, workflowId, workflowTimeoutSeconds, null, null);
    }

    /**
     * @param workflowType           required
     * @param startStateId           required
     * @param workflowId             required
     * @param workflowTimeoutSeconds required
     * @param input                  optional, can be null
     * @return runId
     */
    public String startWorkflow(
            final String workflowType,
            final String startStateId,
            final String workflowId,
            final int workflowTimeoutSeconds,
            final Object input) {
        return this.startWorkflow(workflowType, startStateId, workflowId, workflowTimeoutSeconds, input, null);
    }

    /**
     * @param workflowType           required
     * @param startStateId           required
     * @param workflowId             required
     * @param workflowTimeoutSeconds required
     * @param input                  optional, can be null
     * @param options                optional, can be null
     * @return runId
     */
    public String startWorkflow(
            final String workflowType,
            final String startStateId,
            final String workflowId,
            final int workflowTimeoutSeconds,
            final Object input,
            final UnregisteredWorkflowOptions options) {

        final WorkflowStartRequest request = new WorkflowStartRequest()
                .workflowId(workflowId)
                .dexWorkerUrl(clientOptions.getWorkerUrl())
                .dexWorkflowType(workflowType)
                .workflowTimeoutSeconds(workflowTimeoutSeconds)
                .stateInput(clientOptions.getObjectEncoder().encode(input))
                .startStateId(startStateId);

        if (options != null) {
            final WorkflowStartOptions startOptions = new WorkflowStartOptions();
            if (options.getCronSchedule().isPresent()) {
                startOptions.cronSchedule(CronScheduleValidator.validate(options.getCronSchedule()));
            }
            if (options.getWorkflowStartDelaySeconds().isPresent()) {
                startOptions.workflowStartDelaySeconds(options.getWorkflowStartDelaySeconds().get());
            }
            if (options.getWorkflowIdReusePolicy().isPresent()) {
                startOptions.idReusePolicy(options.getWorkflowIdReusePolicy().get());
            }
            if (options.getWorkflowRetryPolicy().isPresent()) {
                startOptions.retryPolicy(options.getWorkflowRetryPolicy().get());
            }
            if (options.getWorkflowConfigOverride().isPresent()) {
                startOptions.workflowConfigOverride(options.getWorkflowConfigOverride().get());
            }
            if (options.getInitialSearchAttribute().size() > 0) {
                options.getInitialSearchAttribute().forEach(sa -> {
                    assert sa.getValueType() != null;
                    final Object val = Client.getSearchAttributeValue(sa.getValueType(), sa);
                    if (val == null) {
                        throw new IllegalArgumentException(String.format("search attribute value is not set correctly for key %s with value type %s", sa.getKey(), sa.getValueType()));
                    }
                });
                startOptions.searchAttributes(options.getInitialSearchAttribute());
            }
            if(options.getInitialDataAttribute().size()>0){
                List<KeyValue> dataAttributes = options.getInitialDataAttribute().entrySet().stream()
                        .map(entry -> new KeyValue()
                                .key(entry.getKey())
                                .value(clientOptions.getObjectEncoder().encode(entry.getValue())))
                        .collect(Collectors.toList());
                startOptions.dataAttributes(dataAttributes);
            }

            if (options.getStartStateOptions().isPresent()) {
                request.stateOptions(options.getStartStateOptions().get());
            }
            if (options.getUsingMemoForDataAttributes().isPresent()) {
                startOptions.useMemoForDataAttributes(options.getUsingMemoForDataAttributes().get());
            }

            if (options.getWorkflowAlreadyStartedOptions().isPresent()) {
                startOptions.workflowAlreadyStartedOptions(options.getWorkflowAlreadyStartedOptions().get());
            }

            request.workflowStartOptions(startOptions);

            request.waitForCompletionStateExecutionIds(options.getWaitForCompletionStateExecutionIds());
            request.waitForCompletionStateIds(options.getWaitForCompletionStateIds());
        }

        try {
            WorkflowStartResponse workflowStartResponse = defaultApi.apiV1WorkflowStartPost(request);
            return workflowStartResponse.getWorkflowRunId();
        } catch (FeignException.FeignClientException exp) {
            throw DexHttpException.fromFeignException(clientOptions.getObjectEncoder(), exp);
        }
    }

    /**
     * For most cases, a workflow only has one result(one completion state).
     * Use this API to retrieve the output of the state with waiting for the workflow to complete.
     * If the workflow is not COMPLETED, throw the {@link feign.FeignException.FeignClientException}.
     *
     * @param valueClass    required, the type class of the output
     * @param workflowId    required, the workflowId
     * @param workflowRunId optional, can be empty
     * @param <T>           type of the output
     * @return the output result
     */
    public <T> T getSimpleWorkflowResultWithWait(
            final Class<T> valueClass,
            final String workflowId,
            final String workflowRunId) {
        return getWorkflowResultsAndDecode(valueClass, workflowId, workflowRunId, true);
    }

    /**
     * For most cases, a workflow only has one result(one completion state).
     * Use this API to retrieve the output of the state with waiting for the workflow to complete.
     * If the workflow is not COMPLETED, throw the {@link feign.FeignException.FeignClientException}.
     *
     * @param valueClass    required, the type class of the output
     * @param workflowId    required, the workflowId
     * @param <T>           type of the output
     * @return the output result
     */
    public <T> T getSimpleWorkflowResultWithWait(
            final Class<T> valueClass,
            final String workflowId) {
        return getSimpleWorkflowResultWithWait(valueClass, workflowId, "");
    }

    /**
     * For most cases, a workflow only has one result(one completion state).
     * Use this API to retrieve the output of the state without waiting for the workflow to complete.
     * If the workflow is not COMPLETED, throw the {@link WorkflowUncompletedException}.
     * Else, return the same result as {@link #getSimpleWorkflowResultWithWait(Class, String, String)}.
     *
     * @param valueClass    required, the type class of the output
     * @param workflowId    required, the workflowId
     * @param workflowRunId optional, can be empty
     * @param <T>           type of the output
     * @return the output result
     */
    public <T> T tryGettingSimpleWorkflowResult(
            final Class<T> valueClass,
            final String workflowId,
            final String workflowRunId) {
        return getWorkflowResultsAndDecode(valueClass, workflowId, workflowRunId, false);
    }

    /**
     * For most cases, a workflow only has one result(one completion state).
     * Use this API to retrieve the output of the state without waiting for the workflow to complete.
     * If the workflow is not COMPLETED, throw the {@link WorkflowUncompletedException}.
     * Else, return the same result as {@link #getSimpleWorkflowResultWithWait(Class, String)}.
     *
     * @param valueClass    required, the type class of the output
     * @param workflowId    required, the workflowId
     * @param <T>           type of the output
     * @return the output result
     */
    public <T> T tryGettingSimpleWorkflowResult(
            final Class<T> valueClass,
            final String workflowId) {
        return tryGettingSimpleWorkflowResult(valueClass, workflowId, "");
    }

    /**
     * In some cases, a workflow may have more than one completion states.
     * Use this API to retrieve the output of the states with waiting for the workflow to complete.
     * If the workflow is not COMPLETED, throw the {@link feign.FeignException.FeignClientException}.
     *
     * @param workflowId    required, the workflowId
     * @param workflowRunId optional, can be empty
     * @return a list of the state output for completion states. User code will figure how to use ObjectEncoder to decode the output
     */
    public List<StateCompletionOutput> getComplexWorkflowResultWithWait(
            final String workflowId, final String workflowRunId) {
        return getWorkflowResults(workflowId, workflowRunId, true);
    }

    /**
     * In some cases, a workflow may have more than one completion states.
     * Use this API to retrieve the output of the states with waiting for the workflow to complete.
     * If the workflow is not COMPLETED, throw the {@link feign.FeignException.FeignClientException}.
     *
     * @param workflowId    required, the workflowId
     * @return a list of the state output for completion states. User code will figure how to use ObjectEncoder to decode the output
     */
    public List<StateCompletionOutput> getComplexWorkflowResultWithWait(final String workflowId) {
        return getComplexWorkflowResultWithWait(workflowId, "");
    }

    /**
     * In some cases, a workflow may have more than one completion states.
     * Use this API to retrieve the output of the states without waiting for the workflow to complete.
     * If the workflow is not COMPLETED, throw the {@link WorkflowUncompletedException}.
     * Else, return the same result as {@link #getComplexWorkflowResultWithWait(String, String)}.
     *
     * @param workflowId    required, the workflowId
     * @param workflowRunId optional, can be empty
     * @return a list of the state output for completion states. User code will figure how to use ObjectEncoder to decode the output
     */
    public List<StateCompletionOutput> tryGettingComplexWorkflowResult(
            final String workflowId, final String workflowRunId) {
        return getWorkflowResults(workflowId, workflowRunId, false);
    }

    /**
     * In some cases, a workflow may have more than one completion states.
     * Use this API to retrieve the output of the states without waiting for the workflow to complete.
     * If the workflow is not COMPLETED, throw the {@link WorkflowUncompletedException}.
     * Else, return the same result as {@link #getComplexWorkflowResultWithWait(String)}.
     *
     * @param workflowId    required, the workflowId
     * @return a list of the state output for completion states. User code will figure how to use ObjectEncoder to decode the output
     */
    public List<StateCompletionOutput> tryGettingComplexWorkflowResult(final String workflowId) {
        return tryGettingComplexWorkflowResult(workflowId, "");
    }

    private <T> T getWorkflowResultsAndDecode(
            final Class<T> valueClass,
            final String workflowId,
            final String workflowRunId,
            final boolean withWait) {
        final List<StateCompletionOutput> workflowResults = getWorkflowResults(workflowId, workflowRunId, withWait);

        if (workflowResults.size() == 0) {
            return null;
        }

        final String checkErrorMessage = "this workflow should have one or zero state output for using this API";
        final List<StateCompletionOutput> filteredResults = workflowResults.stream().filter((res) -> res.getCompletedStateOutput() != null).collect(Collectors.toList());
        Preconditions.checkArgument(workflowResults.size() == 1 || filteredResults.size() == 1, checkErrorMessage + ", found " + workflowResults.size() + ", after filtered NULL: " + filteredResults.size());

        final StateCompletionOutput output;
        if (filteredResults.size() == 1) {
            output = filteredResults.get(0);
        } else {
            output = workflowResults.get(0);
        }
        return clientOptions.getObjectEncoder().decode(output.getCompletedStateOutput(), valueClass);
    }

    private List<StateCompletionOutput> getWorkflowResults(
            final String workflowId,
            final String workflowRunId,
            final boolean withWait) {
        final WorkflowGetRequest request = new WorkflowGetRequest()
                .needsResults(true)
                .workflowId(workflowId)
                .workflowRunId(workflowRunId);

        if(withWait && clientOptions.getLongPollApiMaxWaitTimeSeconds().isPresent()) {
            request.waitTimeSeconds(clientOptions.getLongPollApiMaxWaitTimeSeconds().get());
        }

        final WorkflowGetResponse workflowGetResponse;
        try {
            if (withWait) {
                workflowGetResponse = defaultApi.apiV1WorkflowGetWithWaitPost(request);
            } else {
                workflowGetResponse = defaultApi.apiV1WorkflowGetPost(request);
            }
        } catch (final FeignException.FeignClientException exp) {
            throw DexHttpException.fromFeignException(clientOptions.getObjectEncoder(), exp);
        }

        if (workflowGetResponse.getWorkflowStatus() != WorkflowStatus.COMPLETED) {
            throwUncompletedException(workflowGetResponse);
        }

        final List<StateCompletionOutput> results = workflowGetResponse.getResults();
        if (results == null) {
            return ImmutableList.of();
        }
        return results;
    }

    public void waitForStateExecutionCompletion(
            final String workflowId,
            final String stateExecutionId) {
        final WorkflowWaitForStateCompletionRequest request = new WorkflowWaitForStateCompletionRequest()
                .stateExecutionId(stateExecutionId)
                .workflowId(workflowId);

        if(clientOptions.getLongPollApiMaxWaitTimeSeconds().isPresent()) {
            request.waitTimeSeconds(clientOptions.getLongPollApiMaxWaitTimeSeconds().get());
        }

        try {
            defaultApi.apiV1WorkflowWaitForStateCompletionPost(request);
        } catch (final FeignException.FeignClientException exp) {
            throw DexHttpException.fromFeignException(clientOptions.getObjectEncoder(), exp);
        }
    }

    public void waitForStateExecutionCompletion(
            final String workflowId,
            final String stateId,
            final String waitForKey) {
        final WorkflowWaitForStateCompletionRequest request = new WorkflowWaitForStateCompletionRequest()
                .stateId(stateId)
                .waitForKey(waitForKey)
                .workflowId(workflowId);

        if(clientOptions.getLongPollApiMaxWaitTimeSeconds().isPresent()) {
            request.waitTimeSeconds(clientOptions.getLongPollApiMaxWaitTimeSeconds().get());
        }

        try {
            defaultApi.apiV1WorkflowWaitForStateCompletionPost(request);
        } catch (final FeignException.FeignClientException exp) {
            throw DexHttpException.fromFeignException(clientOptions.getObjectEncoder(), exp);
        }
    }

    private void throwUncompletedException(final WorkflowGetResponse workflowGetResponse) {
        throw new WorkflowUncompletedException(
                workflowGetResponse.getWorkflowRunId(),
                workflowGetResponse.getWorkflowStatus(),
                workflowGetResponse.getErrorType(),
                workflowGetResponse.getErrorMessage(),
                workflowGetResponse.getResults(),
                this.clientOptions.getObjectEncoder());
    }

    public void signalWorkflow(
            final String workflowId,
            final String workflowRunId,
            final String signalChannelName,
            final Object signalValue) {

        try {
            defaultApi.apiV1WorkflowSignalPost(new WorkflowSignalRequest()
                    .workflowId(workflowId)
                    .workflowRunId(workflowRunId)
                    .signalChannelName(signalChannelName)
                    .signalValue(clientOptions.getObjectEncoder().encode(signalValue)));
        } catch (FeignException.FeignClientException exp) {
            throw DexHttpException.fromFeignException(clientOptions.getObjectEncoder(), exp);
        }
    }

    public void publishToInternalChannel(
            final String workflowId,
            final String workflowRunId,
            final List<InterStateChannelPublishing> messages){

        try {
            defaultApi.apiV1WorkflowPublishToInternalChannelPost(
                    new PublishToInternalChannelRequest()
                            .messages(messages)
                            .workflowId(workflowId)
                            .workflowRunId(workflowRunId)
            );
        } catch (FeignException.FeignClientException exp) {
            throw DexHttpException.fromFeignException(clientOptions.getObjectEncoder(), exp);
        }
    }

    /**
     * @param workflowId                  workflowId
     * @param workflowRunId               workflowRunId
     * @param resetWorkflowTypeAndOptions the combination parameter for reset
     * @return the new runId after reset
     */
    public String resetWorkflow(
            final String workflowId,
            final String workflowRunId,
            final ResetWorkflowTypeAndOptions resetWorkflowTypeAndOptions
    ) {

        final WorkflowResetRequest request = new WorkflowResetRequest()
                .workflowId(workflowId)
                .workflowRunId(workflowRunId)
                .resetType(resetWorkflowTypeAndOptions.getResetType())
                .reason(resetWorkflowTypeAndOptions.getReason());
        if (resetWorkflowTypeAndOptions.getHistoryEventId().isPresent()) {
            request.historyEventId(resetWorkflowTypeAndOptions.getHistoryEventId().get());
        }
        if (resetWorkflowTypeAndOptions.getHistoryEventTime().isPresent()) {
            request.historyEventTime(resetWorkflowTypeAndOptions.getHistoryEventTime().get());
        }
        if (resetWorkflowTypeAndOptions.getSkipSignalReapply().isPresent()) {
            request.skipSignalReapply(resetWorkflowTypeAndOptions.getSkipSignalReapply().get());
        }
        if (resetWorkflowTypeAndOptions.getSkipUpdateReapply().isPresent()) {
            request.skipUpdateReapply(resetWorkflowTypeAndOptions.getSkipUpdateReapply().get());
        }
        if (resetWorkflowTypeAndOptions.getStateId().isPresent()) {
            request.stateId(resetWorkflowTypeAndOptions.getStateId().get());
        }
        if (resetWorkflowTypeAndOptions.getStateExecutionId().isPresent()) {
            request.stateExecutionId(resetWorkflowTypeAndOptions.getStateExecutionId().get());
        }

        try {
            final WorkflowResetResponse resp = defaultApi.apiV1WorkflowResetPost(request);
            return resp.getWorkflowRunId();
        } catch (FeignException.FeignClientException exp) {
            throw DexHttpException.fromFeignException(clientOptions.getObjectEncoder(), exp);
        }
    }

    public void skipTimer(
            final String workflowId,
            final String workflowRunId,
            final String workflowStateId,
            final int stateExecutionNumber,
            final String timerCommandId) {
        final String stateExecutionId = String.format("%s-%s", workflowStateId, stateExecutionNumber);

        try {
            defaultApi.apiV1WorkflowTimerSkipPost(new WorkflowSkipTimerRequest()
                    .workflowId(workflowId)
                    .workflowRunId(workflowRunId)
                    .workflowStateExecutionId(stateExecutionId)
                    .timerCommandId(timerCommandId));
        } catch (FeignException.FeignClientException exp) {
            throw DexHttpException.fromFeignException(clientOptions.getObjectEncoder(), exp);
        }
    }

    public void skipTimer(
            final String workflowId,
            final String workflowRunId,
            final String workflowStateId,
            final int stateExecutionNumber,
            final int timerCommandIndex) {
        final String stateExecutionId = String.format("%s-%s", workflowStateId, stateExecutionNumber);

        try {
            defaultApi.apiV1WorkflowTimerSkipPost(new WorkflowSkipTimerRequest()
                    .workflowId(workflowId)
                    .workflowRunId(workflowRunId)
                    .workflowStateExecutionId(stateExecutionId)
                    .timerCommandIndex(timerCommandIndex));
        } catch (FeignException.FeignClientException exp) {
            throw DexHttpException.fromFeignException(clientOptions.getObjectEncoder(), exp);
        }
    }

    /**
     * Stop a workflow, this is essentially cancel the workflow gracefully
     *
     * @param workflowId    required
     * @param workflowRunId optional
     */
    public void stopWorkflow(
            final String workflowId,
            final String workflowRunId) {
        stopWorkflow(workflowId, workflowRunId, null);
    }

    /**
     * Stop a workflow with options
     *
     * @param workflowId    required
     * @param workflowRunId optional
     * @param options       optional
     */
    public void stopWorkflow(
            final String workflowId,
            final String workflowRunId,
            final StopWorkflowOptions options) {
        try {
            final WorkflowStopRequest request = new WorkflowStopRequest()
                    .workflowId(workflowId)
                    .workflowRunId(workflowRunId);
            if (options != null) {
                if (options.getWorkflowStopType().isPresent()) {
                    request.stopType(options.getWorkflowStopType().get());
                }
                if (options.getReason().isPresent()) {
                    request.reason(options.getReason().get());
                }
            }
            defaultApi.apiV1WorkflowStopPost(request);
        } catch (FeignException.FeignClientException exp) {
            throw DexHttpException.fromFeignException(clientOptions.getObjectEncoder(), exp);
        }
    }

    /**
     * @param workflowId workflowId
     * @param workflowRunId workflowRunId
     * @param attributeKeys, return all attributes if this is empty or null
     * @return the response
     */
    public WorkflowGetDataObjectsResponse getAnyWorkflowDataObjects(
            final String workflowId,
            final String workflowRunId,
            List<String> attributeKeys) {
        return getAnyWorkflowDataObjects(workflowId, workflowRunId, attributeKeys, false);
    }

    /**
     * Get a workflow's status and results(if completed &amp; requested).
     * If the workflow does not exist, throw the WORKFLOW_NOT_EXISTS_SUB_STATUS exception.
     *
     * @param workflowId    required
     * @param workflowRunId optional
     * @param needsResults  result will be returned if this is true and workflow is completed
     * @return the workflow's status and results
     */
    public WorkflowGetResponse getWorkflow(
            final String workflowId,
            final String workflowRunId,
            final Boolean needsResults) {
        try {
            return defaultApi.apiV1WorkflowGetPost(
                    new WorkflowGetRequest()
                            .workflowId(workflowId)
                            .workflowRunId(workflowRunId)
                            .needsResults(needsResults)
            );
        } catch (final FeignException.FeignClientException exp) {
            throw DexHttpException.fromFeignException(clientOptions.getObjectEncoder(), exp);
        }
    }

    public WorkflowGetDataObjectsResponse getAnyWorkflowDataObjects(
            final String workflowId,
            final String workflowRunId,
            List<String> attributeKeys,
            boolean usingMemoForDataAttributes) {
        try {
            return defaultApi.apiV1WorkflowDataobjectsGetPost(
                    new WorkflowGetDataObjectsRequest()
                            .workflowId(workflowId)
                            .workflowRunId(workflowRunId)
                            .keys(attributeKeys)
                            .useMemoForDataAttributes(usingMemoForDataAttributes)
            );
        } catch (FeignException.FeignClientException exp) {
            throw DexHttpException.fromFeignException(clientOptions.getObjectEncoder(), exp);
        }
    }

    public void setAnyWorkflowDataObjects(
            final String workflowId,
            final String workflowRunId,
            final Map<String, Object> dataObjects
    ) {
        final List<KeyValue> encodedObjects = dataObjects.entrySet().stream().map(entry -> new KeyValue()
                        .key(entry.getKey())
                        .value(clientOptions.getObjectEncoder().encode(entry.getValue())))
                .collect(Collectors.toList());

        try {
            defaultApi.apiV1WorkflowDataobjectsSetPost(
                    new WorkflowSetDataObjectsRequest()
                            .workflowId(workflowId)
                            .workflowRunId(workflowRunId)
                            .objects(encodedObjects)
            );
        } catch (final FeignException.FeignClientException exp) {
            throw DexHttpException.fromFeignException(clientOptions.getObjectEncoder(), exp);
        }
    }

    public WorkflowSearchResponse searchWorkflow(final String query, final int pageSize) {

        try {
            return defaultApi.apiV1WorkflowSearchPost(
                    new WorkflowSearchRequest()
                            .query(query)
                            .pageSize(pageSize)
            );
        } catch (FeignException.FeignClientException exp) {
            throw DexHttpException.fromFeignException(clientOptions.getObjectEncoder(), exp);
        }
    }

    public WorkflowSearchResponse searchWorkflow(final WorkflowSearchRequest request) {
        try {
            return defaultApi.apiV1WorkflowSearchPost(request);
        } catch (FeignException.FeignClientException exp) {
            throw DexHttpException.fromFeignException(clientOptions.getObjectEncoder(), exp);
        }
    }

    public WorkflowGetSearchAttributesResponse getAnyWorkflowSearchAttributes(
            final String workflowId,
            final String workflowRunId,
            List<SearchAttributeKeyAndType> attributeKeys) {
        try {
            return defaultApi.apiV1WorkflowSearchattributesGetPost(
                    new WorkflowGetSearchAttributesRequest()
                            .workflowId(workflowId)
                            .workflowRunId(workflowRunId)
                            .keys(attributeKeys)
            );
        } catch (FeignException.FeignClientException exp) {
            throw DexHttpException.fromFeignException(clientOptions.getObjectEncoder(), exp);
        }
    }

    public void setAnyWorkflowSearchAttributes(
            final String workflowId,
            final String workflowRunId,
            final List<SearchAttribute> searchAttributes
    ) {
        try {
            defaultApi.apiV1WorkflowSearchattributesSetPost(
                    new WorkflowSetSearchAttributesRequest()
                            .workflowId(workflowId)
                            .workflowRunId(workflowRunId)
                            .searchAttributes(searchAttributes)
            );
        } catch (final FeignException.FeignClientException exp) {
            throw DexHttpException.fromFeignException(clientOptions.getObjectEncoder(), exp);
        }
    }

    public <T> T invokeRpc(
            Class<T> valueClass,
            final Object input,
            final String workflowId,
            final String workflowRunId,
            final String rpcName,
            final int timeoutSeconds,
            final PersistenceLoadingPolicy dataAttributesLoadingPolicy,
            final PersistenceLoadingPolicy searchAttributesLoadingPolicy) {
        return invokeRpc(valueClass, input, workflowId, workflowRunId, rpcName, timeoutSeconds, dataAttributesLoadingPolicy, searchAttributesLoadingPolicy, false, null);
    }

    public <T> T invokeRpc(
            Class<T> valueClass,
            final Object input,
            final String workflowId,
            final String workflowRunId,
            final String rpcName,
            final int timeoutSeconds,
            final PersistenceLoadingPolicy dataAttributesLoadingPolicy,
            final PersistenceLoadingPolicy searchAttributesLoadingPolicy,
            final boolean usingMemoForDataAttributes,
            final List<SearchAttributeKeyAndType> allSearchAttributes) {
        try {
            final EncodedObject encodedInput = this.clientOptions.getObjectEncoder().encode(input);
            WorkflowRpcRequest request = new WorkflowRpcRequest()
                    .input(encodedInput)
                    .workflowId(workflowId)
                    .workflowRunId(workflowRunId)
                    .rpcName(rpcName)
                    .timeoutSeconds(timeoutSeconds)
                    .dataAttributesLoadingPolicy(dataAttributesLoadingPolicy)
                    .searchAttributesLoadingPolicy(searchAttributesLoadingPolicy)
                    .useMemoForDataAttributes(usingMemoForDataAttributes)
                    .searchAttributes(allSearchAttributes);
            this.outgoingWorkflowRpcRequest = request;
            final WorkflowRpcResponse response = defaultApi.apiV1WorkflowRpcPost(request);
            return this.clientOptions.getObjectEncoder().decode(response.getOutput(), valueClass);
        } catch (FeignException.FeignClientException exp) {
            throw DexHttpException.fromFeignException(clientOptions.getObjectEncoder(), exp);
        }
    }
}
