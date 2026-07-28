# Monitors
## State API availability 
* Meaning: The State API is failing. The API is implemented by the iWF application. 
* Investigation:
  * Look at logs to see why the State API is failing.
  * If there are no error logs, then check if there is some devops/network issue between iWF server and application 
  * If you can find the workflow in Temporal/Cadence WebUI, look at the pending activity tab to see the error details
  * The failure eventually will be recroded in AcitivytTaskFailure event after max out backoff retry
  * Also use history events to trouble shoot why a workflow has reach to a certain state

## Workflow failure 
* Meaning: There are two cases that workflow could fail:
* Investigation: look at the failed workflows from Temporal UI ( search for ExecutionStatus="Failed" in the UI ):
  * User WorkflowState decides to fail in their code. It’s user behavior. User workflow max out backoff retry in State APIs
  * See the above State API availability alert runbook
  * Mitigation: reset the failed workflows if the bug got fixed and you want to mitigate. See more in HowTo section.

## Workflow timeout
* Meaning: workflow exceed the expected timeout period. The timeout is set when starting the workflow. (Note that users can set 0 for infinite timeout)
* Investigation: look at the failed workflows from Temporal Web UI ( search for ExecutionStatus="TimedOut" in the UI
  * To see the timeout value when starting the workflow, you have to look at the raw JSON history, in workflowExecutionTimeout field of the first event(WorkflowExecutionStarted) 
  * [This can be improved by this feature](https://github.com/temporalio/ui/issues/1429)
* Mitigation: reset the failed workflows if the bug got fixed and you want to mitigate. See more in HowTo section.

# How To

## Scale up horizontally 

iWF Application is REST based micro-service. To scale it up, you simply add instances. 

## Make a better view of workflow in Temporal UI
To make it easier with iWF, customize the columns by clicking the button from sidebar, to replace WorkflowType with IwfWorkflowType

![Screenshot 2023-05-31 at 3 43 20 PM](https://github.com/indeedeng/iwf/assets/4523955/541a6b93-626f-4bfa-9e0c-924cf8cc7ec9)

## Troubleshoot & Debugging

### !!!Important Tips!!!
* Let your worker service return error stacktrace as the response body to iWF server. E.g.
  like [this example of Spring Boot using ExceptionHandler](../../examples/java/src/main/java/io/iworkflow/controller/IwfWorkerApiController.java#L51)
* If you return the full stacktrace in response body, the pending activity view will show it to you! Then use
  Cadence/Temporal WebUI to debug your application.

<img width="428" height="499" alt="Screenshot 2026-01-29 at 7 25 41 PM" src="https://github.com/user-attachments/assets/76289828-ccee-4398-92f6-87e6411b3257" />


### Use QueryHandlers in Cadence/Temporal WebUI


* GetDataObjects return all the current data attributes of a workflow.  (TODO, rename GetDataObjects to GetDataAttributes )

* GetSearchAttributes return all the current search attributes.

* GetCurrentTimerInfos returns all the current timers.

* PrepareRPCQueryType return all the workflow states' status -- what commands are they waiting for, and what have been completed

There are two fields that are most useful for debugging purpose:

  * PendingStateExecutionsRequestCommands shows what are the commands a state execution is requesting

![image](https://github.com/indeedeng/iwf/assets/4523955/9d8ab362-f008-4cab-9f84-2f484960556b)




### Read Workflow History in WebUI
Workflow can be used to understand how does a workflow reach to the current status.

**These are ONLY events that you need to understand. You can ignore the others as implementation details of iWF service**

* WorkflowExecutionStarted

It contains the input of the workflow

* ActivityTaskScheduled

It contains the input of State API (waitUntil or execute, depends on the activity type)

* ActivityTaskCompleted

It contains the output of State API (waitUntil or execute, depends on the activity type)

* WorkflowSignaled

If the signal name starts with __IwfSystem_ .. then it's a system signal
They are for internal implementation of iWF service. e.g. SkipTimerChannel is for skipping a timer.
Others are user signals Sent by cient.signalWorkflow API, and received by signalChannelCommand)

* WorkflowExecutionCompleted/Failed

It contains the output of the workflow

## Search for Workflows 
To search for workflows in Temporal UI, check the [documentation](https://docs.temporal.io/visibility#search-attribute) for the search syntax. 

Example search patterns:

* `ExecutionStatus!='Running'` to see closed workflow
* `ExecutionStatus='Failed'` to see failed workflows
* `ExecutionStatus="Failed" AND CloseTime > '2023-03-01T00:00:00.236-08:00' AND CloseTime < '2023-03-02T00:00:00.236-08:00'`  to see workflows failed between 03/01 and 03/02 of 2023
* `HistoryLength>30` to find workflows that have more than 30 events

On top of the Temporal system search attributes, iWF also provide three system search attributes:

* `IwfWorkflowType`: Keyword:  
* `IwfExecutingStateIds`: Keyword array
* `IwfGlobalWorkflowVersion`: Int

For example, you can search for workflow type that is executing a state: `IwfWorkflowType='AbcWorkflow' AND IwfExecutingStateIds='StateA'`  

Search for executing states will be useful if you want to remove a state from the code base. Use the search query to check there is no workflow running at the state, otherwise the State API will run into error because the state is removed. 

You can also use your [custom search attributes in iWF](Persistence.md):

## Register Search Attribute

### In Temporal Cloud 
In Temporal Cloud WebUI, register your new search attribute. E.g. you can add a new "RsvpEventId" as Keyword.

### Not in Temporal Cloud
For self-hosted Temporal or Cadence, use command line to register search attributes. 
See [examples](../../server/CONTRIBUTING.md)

### Then ...
Then in your workflow code, use persistence API to set the search attribute value. E.g. 
```java
persistence.setKeyword("RsvpEventId", "eventId-123");
```
In your application code, use search API to search for workflows. 


## Reset Workflows
You can reset the workflows to the previous states. 

For Temporal, you can use [tctl](https://docs.temporal.io/tctl-v1) / [temporal](https://github.com/temporalio/cli) to perform the reset, or [cadence command](https://github.com/uber/cadence#use-cadence-cli) for Cadence, but there are limited reset types with tctl/temporal/cadence.

With iWF it's recommended to reset through the iWF service API.

You can run the [HTTP script](../../server/script/http/local/home.http#L20) locally to invoke the reset API, or use a CURL command. 

Supported resetType:

* BEGINNING: reset to the beginning or the workflow history.
* HISTORY_EVENT_ID: reset to a particular history event Id, must be used with historyEventId
* HISTORY_EVENT_TIME: reset to a time (exclusive), must be used with historyEventTime
* STATE_ID: reset: reset to the first time that the first time that the state is executed (note, by default the class short name is the stateId of the state). E.g. WaitAndPrepareState uses WaitAndPrepareState as the stateId. must be used with stateId
* STATE_EXECUTION_ID: reset the the particular execution of the state. iWF use increasing number from 1,2,3... to order the state execution for the same StateId. E.g. WaitAndPrepareState-2 means the Id of the second time that state is executed. must be used with stateExecutionId 
WorkflowRunId is optional, by default it will reset the latest/current workflow execution.

skipSignalReapply is optional, When it's true, it will not reapply the signals to the new run. Recommended to true if you don't really understand what it means (big grin) 

For example, to use a CURL command to reset a workflow to 

`curl -X POST https://iwf-service.com/api/v1/workflow/reset -d '{ "workflowId": "<workflowId>", "resetType": "STATE_ID", "stateId": "WaitAndPrepareState", "skipSignalReapply": true }'`


See iWF service [`iwf.proto`](../../protos/iwf.proto) for more details.

## How To Skip Timer for Workflows
Any the timer commands can be skipped by an API call. The API is provided by iWF server for free. This can be used for operation or testing purpose. 

To locate a timer to skip, either timerCommandIndex, or timerCommandId is needed, with stateExecutionId + workflowId(and optional workflowRunId). 

You can also use QueryHandler GetCurrentTimerInfos in Cadence/Temporal WebUI to check what are the timers can be skipped.

For example, to use a CURL command to skip a timer of a workflow 

`curl -X POST https://iwf-service.com/api/v1/workflow/timer/skip -d '{ "workflowId": "<workflowId>", "workflowStateExecutionId": "WaitAndPrepareState-1", "timerCommandIndex": 0 }'`

Or run t[his HTTP script](../../server/script/http/local/home.http#L33) in IntelliJ IDE 

See iWF service [`iwf.proto`](../../protos/iwf.proto) for more details.

## How To Send a Signal Manually
**iWF requires a format for the signalValue. Make sure you test it in QA first!**

You can send a signal via Temporal WebUI
![image](https://github.com/indeedeng/iwf/assets/4523955/40a32801-94d2-4ac5-ad52-d85e21e44591)

Alternatively, use this CURL command to call iWF service:

`curl -X POST https://iwf-service.com/api/v1/workflow/signal -d '{ "workflowId": "<workflowId>", "signalChannelName": "MySignalName", "signalValue": { "encoding": "jsonType", "data": "\"a string value\"" } }'`

Or run t[his HTTP script](../../server/script/http/local/home.http#L43) in IntelliJ IDE 


## How To Invoke RPC write manually 
RPC write operation is implemented as a system signal. So you can send a RPC write by sending a signal with the right format.

**iWF service is sensitive to the format. Make sure you test it in QA first!**



```json
[
  {
    "RpcInput": {
      "data": "...",
      "encoding": "springJackson"
    },
    "RpcOutput": null,
    "UpsertDataObjects": [
      {
        "key": "...",
        "value": {
          "data": "...",
          "encoding": "springJackson"
        }
      }
    ],
    "UpsertSearchAttributes": null,
    "StateDecision": null,
    "RecordEvents": null,
    "InterStateChannelPublishing": [
      {
        "channelName": "..."
      }
    ]
  }
]
```


See iWF service [`iwf.proto`](../../protos/iwf.proto) for more details.

## More Operations
All other operation that you can do is defined in the [`iwf.proto`](../../protos/iwf.proto) of iWF service. All the operations supported in SDKs can be done using CURL command:

* Stop a workflow ( you can also just click the button in Temporal UI to do so, but you may want to use this REST API for batch operation using a script)
* Start a workflow
* etc
