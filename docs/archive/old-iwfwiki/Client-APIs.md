## Client

### StartWorkflow 
Starts a workflow 
* Parameters 
  * workflowDefinition
  * workflowID
  * timeoutSeconds 
  * workflowInput
  * workflowOptions 
* Known error handling
  * WorkflowAlreadyStartedError -- when there is a workflowID conflict and cannot start based on "IdReusePolicy"


### WaitForWorkflowCompletion
Makes a long poll API to wait for workflow completion 
* Parameters
  * workflowID
  * valueType
* Known Error handling
  * UncompletedError
  * NotExistsError
LongPollTimeout
### SignalWorkflow
Sends a message to the signalChannel of a workflow 
* Parameters
  * workflowDefinition 
  * workflowID
  * channelName
  * value
* Known Error handling
  * No running workflow 

### ResetWorkflow
Resets a workflow execution to the previous point of the history 
* Parameters 
  * workflowID
reseTypeAndOptions
* Known Error handling
  * Not found

### StopWorkflow
Stops a workflow execution 
* Parameters 
  * workflowID
  * StopWorkflowOptions
* Known Error handling
  * No running workflow 

### GetDataAttributes or GetSearchAttributes
Reads the data or search attributes of a workflow execution
* Parameters
  * workflowDefinition 
  * workflowID
  * keys 
* Known Error handling
  * Not found

### SetDataAttributes or SetSearchAttributes
Write the data or search attributes of a workflow execution
* Parameters
  * workflowDefinition 
  * workflowID
  * key and value pairs of attributes
* Known Error handling
  * No running workflow 

### SearchFlows
Finds flow executions given a search attribute query.

* Parameters
  * query
  * page size
  * pagination token
* Returns
  * flow ID and run ID
  * all search attributes returned by the backend visibility API as key/value entries

Search attribute values use the Dex `Value` variants. Temporal preserves
numeric types from payload metadata. Cadence does not return index types from
its list API, so integral JSON numbers are returned as integers and all other
numbers as doubles.

### InvokeRPC
Invokes an RPC of a workflow execution 
* Parameters 
  * workflowID
  * rpc method (by name of stub)
* Known Error handling
  * No applicable workflow 

### DescribeWorkflow
Gets basic info of a workflow execution, including running status 
* Parameters 
  * workflowID
* Known Error handling
  * No applicable workflow 

### SkipTimer
Skips the timer, to let it fire immediately 
* Parameters 
  * workflowID
  * stateId
  * stateExecutionNumber : optional
  * timerCommandID: optional
* Known Error handling
  * No applicable workflow 

### WaitForStateExecutionCompletion
Makes long poll API call to wait for a state execution to complete
* Parameters 
  * workflowID
  * stateId
  * stateExecutionNumber : optional
  * waitForKey: optional
* Known Error handling:
  * LongPollTimeout
  * Not applicable workflow 

## UnregisteredClient 
UnregisteredClient is the raw client without workflow registry. 

It's useful for calling Client APIs in a different repo that doesn’t host the workflows (which may require more dependencies). Underlying, Client is built on top of UnregisteredClient. 

## More APIs from server

* Trigger continue as new 
Update wf config 

TODO: https://github.com/indeedeng/iwf-java-sdk/issues/285 add them to Client/UnregisteredClient

## ClientOptions 
ClientOptions allows customize a Client for communicating with server, including
* ServerURL
* WorkerURL
* ObjectEncoder
* LongPollTimeout
* RequestHeaders
* RetryConfig -- for local retry on calling server API

