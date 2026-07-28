package io.superdurable.dex.gen.api;

import io.superdurable.dex.gen.api.ApiClient;
import io.superdurable.dex.gen.api.EncodingUtils;
import io.superdurable.dex.gen.models.ApiResponse;

import io.superdurable.dex.gen.models.EncodedObject;
import io.superdurable.dex.gen.models.ErrorResponse;
import io.superdurable.dex.gen.models.HealthInfo;
import io.superdurable.dex.gen.models.PublishToInternalChannelRequest;
import io.superdurable.dex.gen.models.TriggerContinueAsNewRequest;
import io.superdurable.dex.gen.models.WorkerErrorResponse;
import io.superdurable.dex.gen.models.WorkflowConfigUpdateRequest;
import io.superdurable.dex.gen.models.WorkflowDumpRequest;
import io.superdurable.dex.gen.models.WorkflowDumpResponse;
import io.superdurable.dex.gen.models.WorkflowGetDataObjectsRequest;
import io.superdurable.dex.gen.models.WorkflowGetDataObjectsResponse;
import io.superdurable.dex.gen.models.WorkflowGetRequest;
import io.superdurable.dex.gen.models.WorkflowGetResponse;
import io.superdurable.dex.gen.models.WorkflowGetSearchAttributesRequest;
import io.superdurable.dex.gen.models.WorkflowGetSearchAttributesResponse;
import io.superdurable.dex.gen.models.WorkflowResetRequest;
import io.superdurable.dex.gen.models.WorkflowResetResponse;
import io.superdurable.dex.gen.models.WorkflowRpcRequest;
import io.superdurable.dex.gen.models.WorkflowRpcResponse;
import io.superdurable.dex.gen.models.WorkflowSearchRequest;
import io.superdurable.dex.gen.models.WorkflowSearchResponse;
import io.superdurable.dex.gen.models.WorkflowSetDataObjectsRequest;
import io.superdurable.dex.gen.models.WorkflowSetSearchAttributesRequest;
import io.superdurable.dex.gen.models.WorkflowSignalRequest;
import io.superdurable.dex.gen.models.WorkflowSkipTimerRequest;
import io.superdurable.dex.gen.models.WorkflowStartRequest;
import io.superdurable.dex.gen.models.WorkflowStartResponse;
import io.superdurable.dex.gen.models.WorkflowStateExecuteRequest;
import io.superdurable.dex.gen.models.WorkflowStateExecuteResponse;
import io.superdurable.dex.gen.models.WorkflowStateWaitUntilRequest;
import io.superdurable.dex.gen.models.WorkflowStateWaitUntilResponse;
import io.superdurable.dex.gen.models.WorkflowStopRequest;
import io.superdurable.dex.gen.models.WorkflowWaitForStateCompletionRequest;
import io.superdurable.dex.gen.models.WorkflowWaitForStateCompletionResponse;
import io.superdurable.dex.gen.models.WorkflowWorkerRpcRequest;
import io.superdurable.dex.gen.models.WorkflowWorkerRpcResponse;

import java.util.ArrayList;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import feign.*;

@javax.annotation.Generated(value = "org.openapitools.codegen.languages.JavaClientCodegen", date = "2026-07-23T10:38:15.577580-07:00[America/Los_Angeles]")
public interface DefaultApi extends ApiClient.Api {


  /**
   * update the config of a workflow
   * 
   * @param workflowConfigUpdateRequest  (optional)
   */
  @RequestLine("POST /api/v1/workflow/config/update")
  @Headers({
    "Content-Type: application/json",
    "Accept: application/json",
  })
  void apiV1WorkflowConfigUpdatePost(WorkflowConfigUpdateRequest workflowConfigUpdateRequest);

  /**
   * update the config of a workflow
   * Similar to <code>apiV1WorkflowConfigUpdatePost</code> but it also returns the http response headers .
   * 
   * @param workflowConfigUpdateRequest  (optional)
   */
  @RequestLine("POST /api/v1/workflow/config/update")
  @Headers({
    "Content-Type: application/json",
    "Accept: application/json",
  })
  ApiResponse<Void> apiV1WorkflowConfigUpdatePostWithHttpInfo(WorkflowConfigUpdateRequest workflowConfigUpdateRequest);



  /**
   * get workflow data objects aka data attributes
   * 
   * @param workflowGetDataObjectsRequest  (optional)
   * @return WorkflowGetDataObjectsResponse
   */
  @RequestLine("POST /api/v1/workflow/dataobjects/get")
  @Headers({
    "Content-Type: application/json",
    "Accept: application/json",
  })
  WorkflowGetDataObjectsResponse apiV1WorkflowDataobjectsGetPost(WorkflowGetDataObjectsRequest workflowGetDataObjectsRequest);

  /**
   * get workflow data objects aka data attributes
   * Similar to <code>apiV1WorkflowDataobjectsGetPost</code> but it also returns the http response headers .
   * 
   * @param workflowGetDataObjectsRequest  (optional)
   * @return A ApiResponse that wraps the response boyd and the http headers.
   */
  @RequestLine("POST /api/v1/workflow/dataobjects/get")
  @Headers({
    "Content-Type: application/json",
    "Accept: application/json",
  })
  ApiResponse<WorkflowGetDataObjectsResponse> apiV1WorkflowDataobjectsGetPostWithHttpInfo(WorkflowGetDataObjectsRequest workflowGetDataObjectsRequest);



  /**
   * set workflow data objects aka data attributes
   * 
   * @param workflowSetDataObjectsRequest  (optional)
   */
  @RequestLine("POST /api/v1/workflow/dataobjects/set")
  @Headers({
    "Content-Type: application/json",
    "Accept: application/json",
  })
  void apiV1WorkflowDataobjectsSetPost(WorkflowSetDataObjectsRequest workflowSetDataObjectsRequest);

  /**
   * set workflow data objects aka data attributes
   * Similar to <code>apiV1WorkflowDataobjectsSetPost</code> but it also returns the http response headers .
   * 
   * @param workflowSetDataObjectsRequest  (optional)
   */
  @RequestLine("POST /api/v1/workflow/dataobjects/set")
  @Headers({
    "Content-Type: application/json",
    "Accept: application/json",
  })
  ApiResponse<Void> apiV1WorkflowDataobjectsSetPostWithHttpInfo(WorkflowSetDataObjectsRequest workflowSetDataObjectsRequest);



  /**
   * load encoded object from storage
   * 
   * @param encodedObject  (optional)
   * @return EncodedObject
   */
  @RequestLine("POST /api/v1/workflow/encodedobject/load")
  @Headers({
    "Content-Type: application/json",
    "Accept: application/json",
  })
  EncodedObject apiV1WorkflowEncodedobjectLoadPost(EncodedObject encodedObject);

  /**
   * load encoded object from storage
   * Similar to <code>apiV1WorkflowEncodedobjectLoadPost</code> but it also returns the http response headers .
   * 
   * @param encodedObject  (optional)
   * @return A ApiResponse that wraps the response boyd and the http headers.
   */
  @RequestLine("POST /api/v1/workflow/encodedobject/load")
  @Headers({
    "Content-Type: application/json",
    "Accept: application/json",
  })
  ApiResponse<EncodedObject> apiV1WorkflowEncodedobjectLoadPostWithHttpInfo(EncodedObject encodedObject);



  /**
   * get a workflow&#39;s status and results(if completed &amp; requested)
   * 
   * @param workflowGetRequest  (optional)
   * @return WorkflowGetResponse
   */
  @RequestLine("POST /api/v1/workflow/get")
  @Headers({
    "Content-Type: application/json",
    "Accept: application/json",
  })
  WorkflowGetResponse apiV1WorkflowGetPost(WorkflowGetRequest workflowGetRequest);

  /**
   * get a workflow&#39;s status and results(if completed &amp; requested)
   * Similar to <code>apiV1WorkflowGetPost</code> but it also returns the http response headers .
   * 
   * @param workflowGetRequest  (optional)
   * @return A ApiResponse that wraps the response boyd and the http headers.
   */
  @RequestLine("POST /api/v1/workflow/get")
  @Headers({
    "Content-Type: application/json",
    "Accept: application/json",
  })
  ApiResponse<WorkflowGetResponse> apiV1WorkflowGetPostWithHttpInfo(WorkflowGetRequest workflowGetRequest);



  /**
   * get a workflow&#39;s status and results(if completed &amp; requested), wait if the workflow is still running
   * 
   * @param workflowGetRequest  (optional)
   * @return WorkflowGetResponse
   */
  @RequestLine("POST /api/v1/workflow/getWithWait")
  @Headers({
    "Content-Type: application/json",
    "Accept: application/json",
  })
  WorkflowGetResponse apiV1WorkflowGetWithWaitPost(WorkflowGetRequest workflowGetRequest);

  /**
   * get a workflow&#39;s status and results(if completed &amp; requested), wait if the workflow is still running
   * Similar to <code>apiV1WorkflowGetWithWaitPost</code> but it also returns the http response headers .
   * 
   * @param workflowGetRequest  (optional)
   * @return A ApiResponse that wraps the response boyd and the http headers.
   */
  @RequestLine("POST /api/v1/workflow/getWithWait")
  @Headers({
    "Content-Type: application/json",
    "Accept: application/json",
  })
  ApiResponse<WorkflowGetResponse> apiV1WorkflowGetWithWaitPostWithHttpInfo(WorkflowGetRequest workflowGetRequest);



  /**
   * dump internal info of a workflow
   * 
   * @param workflowDumpRequest  (optional)
   * @return WorkflowDumpResponse
   */
  @RequestLine("POST /api/v1/workflow/internal/dump")
  @Headers({
    "Content-Type: application/json",
    "Accept: application/json",
  })
  WorkflowDumpResponse apiV1WorkflowInternalDumpPost(WorkflowDumpRequest workflowDumpRequest);

  /**
   * dump internal info of a workflow
   * Similar to <code>apiV1WorkflowInternalDumpPost</code> but it also returns the http response headers .
   * 
   * @param workflowDumpRequest  (optional)
   * @return A ApiResponse that wraps the response boyd and the http headers.
   */
  @RequestLine("POST /api/v1/workflow/internal/dump")
  @Headers({
    "Content-Type: application/json",
    "Accept: application/json",
  })
  ApiResponse<WorkflowDumpResponse> apiV1WorkflowInternalDumpPostWithHttpInfo(WorkflowDumpRequest workflowDumpRequest);



  /**
   * signal a workflow
   * 
   * @param publishToInternalChannelRequest  (optional)
   */
  @RequestLine("POST /api/v1/workflow/publishToInternalChannel")
  @Headers({
    "Content-Type: application/json",
    "Accept: application/json",
  })
  void apiV1WorkflowPublishToInternalChannelPost(PublishToInternalChannelRequest publishToInternalChannelRequest);

  /**
   * signal a workflow
   * Similar to <code>apiV1WorkflowPublishToInternalChannelPost</code> but it also returns the http response headers .
   * 
   * @param publishToInternalChannelRequest  (optional)
   */
  @RequestLine("POST /api/v1/workflow/publishToInternalChannel")
  @Headers({
    "Content-Type: application/json",
    "Accept: application/json",
  })
  ApiResponse<Void> apiV1WorkflowPublishToInternalChannelPostWithHttpInfo(PublishToInternalChannelRequest publishToInternalChannelRequest);



  /**
   * reset a workflow
   * 
   * @param workflowResetRequest  (optional)
   * @return WorkflowResetResponse
   */
  @RequestLine("POST /api/v1/workflow/reset")
  @Headers({
    "Content-Type: application/json",
    "Accept: application/json",
  })
  WorkflowResetResponse apiV1WorkflowResetPost(WorkflowResetRequest workflowResetRequest);

  /**
   * reset a workflow
   * Similar to <code>apiV1WorkflowResetPost</code> but it also returns the http response headers .
   * 
   * @param workflowResetRequest  (optional)
   * @return A ApiResponse that wraps the response boyd and the http headers.
   */
  @RequestLine("POST /api/v1/workflow/reset")
  @Headers({
    "Content-Type: application/json",
    "Accept: application/json",
  })
  ApiResponse<WorkflowResetResponse> apiV1WorkflowResetPostWithHttpInfo(WorkflowResetRequest workflowResetRequest);



  /**
   * execute an RPC of a workflow
   * 
   * @param workflowRpcRequest  (optional)
   * @return WorkflowRpcResponse
   */
  @RequestLine("POST /api/v1/workflow/rpc")
  @Headers({
    "Content-Type: application/json",
    "Accept: application/json",
  })
  WorkflowRpcResponse apiV1WorkflowRpcPost(WorkflowRpcRequest workflowRpcRequest);

  /**
   * execute an RPC of a workflow
   * Similar to <code>apiV1WorkflowRpcPost</code> but it also returns the http response headers .
   * 
   * @param workflowRpcRequest  (optional)
   * @return A ApiResponse that wraps the response boyd and the http headers.
   */
  @RequestLine("POST /api/v1/workflow/rpc")
  @Headers({
    "Content-Type: application/json",
    "Accept: application/json",
  })
  ApiResponse<WorkflowRpcResponse> apiV1WorkflowRpcPostWithHttpInfo(WorkflowRpcRequest workflowRpcRequest);



  /**
   * search for workflows by a search attribute query
   * 
   * @param workflowSearchRequest  (optional)
   * @return WorkflowSearchResponse
   */
  @RequestLine("POST /api/v1/workflow/search")
  @Headers({
    "Content-Type: application/json",
    "Accept: application/json",
  })
  WorkflowSearchResponse apiV1WorkflowSearchPost(WorkflowSearchRequest workflowSearchRequest);

  /**
   * search for workflows by a search attribute query
   * Similar to <code>apiV1WorkflowSearchPost</code> but it also returns the http response headers .
   * 
   * @param workflowSearchRequest  (optional)
   * @return A ApiResponse that wraps the response boyd and the http headers.
   */
  @RequestLine("POST /api/v1/workflow/search")
  @Headers({
    "Content-Type: application/json",
    "Accept: application/json",
  })
  ApiResponse<WorkflowSearchResponse> apiV1WorkflowSearchPostWithHttpInfo(WorkflowSearchRequest workflowSearchRequest);



  /**
   * get workflow search attributes
   * 
   * @param workflowGetSearchAttributesRequest  (optional)
   * @return WorkflowGetSearchAttributesResponse
   */
  @RequestLine("POST /api/v1/workflow/searchattributes/get")
  @Headers({
    "Content-Type: application/json",
    "Accept: application/json",
  })
  WorkflowGetSearchAttributesResponse apiV1WorkflowSearchattributesGetPost(WorkflowGetSearchAttributesRequest workflowGetSearchAttributesRequest);

  /**
   * get workflow search attributes
   * Similar to <code>apiV1WorkflowSearchattributesGetPost</code> but it also returns the http response headers .
   * 
   * @param workflowGetSearchAttributesRequest  (optional)
   * @return A ApiResponse that wraps the response boyd and the http headers.
   */
  @RequestLine("POST /api/v1/workflow/searchattributes/get")
  @Headers({
    "Content-Type: application/json",
    "Accept: application/json",
  })
  ApiResponse<WorkflowGetSearchAttributesResponse> apiV1WorkflowSearchattributesGetPostWithHttpInfo(WorkflowGetSearchAttributesRequest workflowGetSearchAttributesRequest);



  /**
   * set workflow search attributes
   * 
   * @param workflowSetSearchAttributesRequest  (optional)
   */
  @RequestLine("POST /api/v1/workflow/searchattributes/set")
  @Headers({
    "Content-Type: application/json",
    "Accept: application/json",
  })
  void apiV1WorkflowSearchattributesSetPost(WorkflowSetSearchAttributesRequest workflowSetSearchAttributesRequest);

  /**
   * set workflow search attributes
   * Similar to <code>apiV1WorkflowSearchattributesSetPost</code> but it also returns the http response headers .
   * 
   * @param workflowSetSearchAttributesRequest  (optional)
   */
  @RequestLine("POST /api/v1/workflow/searchattributes/set")
  @Headers({
    "Content-Type: application/json",
    "Accept: application/json",
  })
  ApiResponse<Void> apiV1WorkflowSearchattributesSetPostWithHttpInfo(WorkflowSetSearchAttributesRequest workflowSetSearchAttributesRequest);



  /**
   * signal a workflow
   * 
   * @param workflowSignalRequest  (optional)
   */
  @RequestLine("POST /api/v1/workflow/signal")
  @Headers({
    "Content-Type: application/json",
    "Accept: application/json",
  })
  void apiV1WorkflowSignalPost(WorkflowSignalRequest workflowSignalRequest);

  /**
   * signal a workflow
   * Similar to <code>apiV1WorkflowSignalPost</code> but it also returns the http response headers .
   * 
   * @param workflowSignalRequest  (optional)
   */
  @RequestLine("POST /api/v1/workflow/signal")
  @Headers({
    "Content-Type: application/json",
    "Accept: application/json",
  })
  ApiResponse<Void> apiV1WorkflowSignalPostWithHttpInfo(WorkflowSignalRequest workflowSignalRequest);



  /**
   * start a workflow
   * 
   * @param workflowStartRequest  (optional)
   * @return WorkflowStartResponse
   */
  @RequestLine("POST /api/v1/workflow/start")
  @Headers({
    "Content-Type: application/json",
    "Accept: application/json",
  })
  WorkflowStartResponse apiV1WorkflowStartPost(WorkflowStartRequest workflowStartRequest);

  /**
   * start a workflow
   * Similar to <code>apiV1WorkflowStartPost</code> but it also returns the http response headers .
   * 
   * @param workflowStartRequest  (optional)
   * @return A ApiResponse that wraps the response boyd and the http headers.
   */
  @RequestLine("POST /api/v1/workflow/start")
  @Headers({
    "Content-Type: application/json",
    "Accept: application/json",
  })
  ApiResponse<WorkflowStartResponse> apiV1WorkflowStartPostWithHttpInfo(WorkflowStartRequest workflowStartRequest);



  /**
   * for invoking WorkflowState.execute API
   * 
   * @param workflowStateExecuteRequest  (optional)
   * @return WorkflowStateExecuteResponse
   */
  @RequestLine("POST /api/v1/workflowState/decide")
  @Headers({
    "Content-Type: application/json",
    "Accept: application/json",
  })
  WorkflowStateExecuteResponse apiV1WorkflowStateDecidePost(WorkflowStateExecuteRequest workflowStateExecuteRequest);

  /**
   * for invoking WorkflowState.execute API
   * Similar to <code>apiV1WorkflowStateDecidePost</code> but it also returns the http response headers .
   * 
   * @param workflowStateExecuteRequest  (optional)
   * @return A ApiResponse that wraps the response boyd and the http headers.
   */
  @RequestLine("POST /api/v1/workflowState/decide")
  @Headers({
    "Content-Type: application/json",
    "Accept: application/json",
  })
  ApiResponse<WorkflowStateExecuteResponse> apiV1WorkflowStateDecidePostWithHttpInfo(WorkflowStateExecuteRequest workflowStateExecuteRequest);



  /**
   * for invoking WorkflowState.waitUntil API
   * 
   * @param workflowStateWaitUntilRequest  (optional)
   * @return WorkflowStateWaitUntilResponse
   */
  @RequestLine("POST /api/v1/workflowState/start")
  @Headers({
    "Content-Type: application/json",
    "Accept: application/json",
  })
  WorkflowStateWaitUntilResponse apiV1WorkflowStateStartPost(WorkflowStateWaitUntilRequest workflowStateWaitUntilRequest);

  /**
   * for invoking WorkflowState.waitUntil API
   * Similar to <code>apiV1WorkflowStateStartPost</code> but it also returns the http response headers .
   * 
   * @param workflowStateWaitUntilRequest  (optional)
   * @return A ApiResponse that wraps the response boyd and the http headers.
   */
  @RequestLine("POST /api/v1/workflowState/start")
  @Headers({
    "Content-Type: application/json",
    "Accept: application/json",
  })
  ApiResponse<WorkflowStateWaitUntilResponse> apiV1WorkflowStateStartPostWithHttpInfo(WorkflowStateWaitUntilRequest workflowStateWaitUntilRequest);



  /**
   * stop a workflow
   * 
   * @param workflowStopRequest  (optional)
   */
  @RequestLine("POST /api/v1/workflow/stop")
  @Headers({
    "Content-Type: application/json",
    "Accept: application/json",
  })
  void apiV1WorkflowStopPost(WorkflowStopRequest workflowStopRequest);

  /**
   * stop a workflow
   * Similar to <code>apiV1WorkflowStopPost</code> but it also returns the http response headers .
   * 
   * @param workflowStopRequest  (optional)
   */
  @RequestLine("POST /api/v1/workflow/stop")
  @Headers({
    "Content-Type: application/json",
    "Accept: application/json",
  })
  ApiResponse<Void> apiV1WorkflowStopPostWithHttpInfo(WorkflowStopRequest workflowStopRequest);



  /**
   * skip the timer of a workflow
   * 
   * @param workflowSkipTimerRequest  (optional)
   */
  @RequestLine("POST /api/v1/workflow/timer/skip")
  @Headers({
    "Content-Type: application/json",
    "Accept: application/json",
  })
  void apiV1WorkflowTimerSkipPost(WorkflowSkipTimerRequest workflowSkipTimerRequest);

  /**
   * skip the timer of a workflow
   * Similar to <code>apiV1WorkflowTimerSkipPost</code> but it also returns the http response headers .
   * 
   * @param workflowSkipTimerRequest  (optional)
   */
  @RequestLine("POST /api/v1/workflow/timer/skip")
  @Headers({
    "Content-Type: application/json",
    "Accept: application/json",
  })
  ApiResponse<Void> apiV1WorkflowTimerSkipPostWithHttpInfo(WorkflowSkipTimerRequest workflowSkipTimerRequest);



  /**
   * trigger ContinueAsNew for a workflow
   * 
   * @param triggerContinueAsNewRequest  (optional)
   */
  @RequestLine("POST /api/v1/workflow/triggerContinueAsNew")
  @Headers({
    "Content-Type: application/json",
    "Accept: application/json",
  })
  void apiV1WorkflowTriggerContinueAsNewPost(TriggerContinueAsNewRequest triggerContinueAsNewRequest);

  /**
   * trigger ContinueAsNew for a workflow
   * Similar to <code>apiV1WorkflowTriggerContinueAsNewPost</code> but it also returns the http response headers .
   * 
   * @param triggerContinueAsNewRequest  (optional)
   */
  @RequestLine("POST /api/v1/workflow/triggerContinueAsNew")
  @Headers({
    "Content-Type: application/json",
    "Accept: application/json",
  })
  ApiResponse<Void> apiV1WorkflowTriggerContinueAsNewPostWithHttpInfo(TriggerContinueAsNewRequest triggerContinueAsNewRequest);



  /**
   * 
   * 
   * @param workflowWaitForStateCompletionRequest  (optional)
   * @return WorkflowWaitForStateCompletionResponse
   */
  @RequestLine("POST /api/v1/workflow/waitForStateCompletion")
  @Headers({
    "Content-Type: application/json",
    "Accept: application/json",
  })
  WorkflowWaitForStateCompletionResponse apiV1WorkflowWaitForStateCompletionPost(WorkflowWaitForStateCompletionRequest workflowWaitForStateCompletionRequest);

  /**
   * 
   * Similar to <code>apiV1WorkflowWaitForStateCompletionPost</code> but it also returns the http response headers .
   * 
   * @param workflowWaitForStateCompletionRequest  (optional)
   * @return A ApiResponse that wraps the response boyd and the http headers.
   */
  @RequestLine("POST /api/v1/workflow/waitForStateCompletion")
  @Headers({
    "Content-Type: application/json",
    "Accept: application/json",
  })
  ApiResponse<WorkflowWaitForStateCompletionResponse> apiV1WorkflowWaitForStateCompletionPostWithHttpInfo(WorkflowWaitForStateCompletionRequest workflowWaitForStateCompletionRequest);



  /**
   * for invoking workflow RPC API in the worker
   * 
   * @param workflowWorkerRpcRequest  (optional)
   * @return WorkflowWorkerRpcResponse
   */
  @RequestLine("POST /api/v1/workflowWorker/rpc")
  @Headers({
    "Content-Type: application/json",
    "Accept: application/json",
  })
  WorkflowWorkerRpcResponse apiV1WorkflowWorkerRpcPost(WorkflowWorkerRpcRequest workflowWorkerRpcRequest);

  /**
   * for invoking workflow RPC API in the worker
   * Similar to <code>apiV1WorkflowWorkerRpcPost</code> but it also returns the http response headers .
   * 
   * @param workflowWorkerRpcRequest  (optional)
   * @return A ApiResponse that wraps the response boyd and the http headers.
   */
  @RequestLine("POST /api/v1/workflowWorker/rpc")
  @Headers({
    "Content-Type: application/json",
    "Accept: application/json",
  })
  ApiResponse<WorkflowWorkerRpcResponse> apiV1WorkflowWorkerRpcPostWithHttpInfo(WorkflowWorkerRpcRequest workflowWorkerRpcRequest);



  /**
   * return health info of the server
   * 
   * @return HealthInfo
   */
  @RequestLine("GET /info/healthcheck")
  @Headers({
    "Accept: application/json",
  })
  HealthInfo infoHealthcheckGet();

  /**
   * return health info of the server
   * Similar to <code>infoHealthcheckGet</code> but it also returns the http response headers .
   * 
   * @return A ApiResponse that wraps the response boyd and the http headers.
   */
  @RequestLine("GET /info/healthcheck")
  @Headers({
    "Accept: application/json",
  })
  ApiResponse<HealthInfo> infoHealthcheckGetWithHttpInfo();


}
