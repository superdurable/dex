# Compare With Cadence/Temporal
Migrating from Cadence/Temporal is simple and easy. It's only possible to migrate new workflow executions. Let your applications to only start new workflows in Dex. For the existing running workflows in Cadence/Temporal, keep the Cadence/Temporal workers until they are finished.

## Determinism and versioning
There is no non-deterministic errors in Dex workflow code, and hence no versioning API at all in Dex! 

Because there is no replay at all for Dex workflow applications. All workflow state executions are stored in Cadence/Temporal activities of the interpreter workflow activities.

Workflow code change will always apply to any running existing and new workflow executions once deployed. This gives flexibility to maintain long-running business applications.

However, making workflow code change will still have backward-compatibility issue like all other microservice applications. 
Below are the standard ways to address the issues:

1) It's rare but if you don't want old workflows to execute the new code, use a flag in new executions to branch out. For example, if changing flow `StateA->StateB` to `StateA->StateC` only for new workflows, then set a new flag in the new workflow so that StateA can decide go to StateB or StateC. 
2) Removing state code could cause errors(state not found) if there is any state execution still running.  For example, after changed `StateA->StateB` to `StateA->StateC`, you may want to delete StateB. If a StateExecution stays at StateB(most commonly waiting for commands to complete), deleting StateB will cause a not found error when StateB is executed.
   1) The error will be gone if you add the StateB back. Because by default, all State APIs will be backoff retried forever.
   2) If you want to delete StateB as early as possible, use `DexWorkflowType` and `DexExecutingStateIds` search attributes to confirm if there is any workflows still running at the state. These are built-in search attributes from Dex server.  

See more in this [wiki](%5BVersioning%5DHow-to-modify-workflow-code-without-breaking-changes.md).

## ContinueAsNew

There is NO ContinueAsNew API exposed to user workflow!
ContinueAsNew of Cadence/Temporal is a purely leaked technical details. It's due to the replay model conflicting with the underlying storage limit/performance.
As Dex is built on Cadence/Temporal, it will be implemented in a way that is transparent to user workflows. 

Internally the interpreter workflow can continueAsNew without letting Dex user workflow to know. This is called "auto continueAsNew" --
 
After exceeding the action threshold, continueAsNew will happen automatically. 
AutoContinueAsNew will carry over the pending states, along with all the internal states like DataObjects, interStateChannels, searchAttributes.

## Activity
Wait, what? **There is no activity at all in Dex?**

Yes, Dex workflows are essentially a REST service and all the activity code in Cadence/Temporal can just move in Dex workflow code -- waitUntil or execute API of WorkflowState.

A main reason that many people use Cadence/Temporal activity is to take advantage of the history showing input/output in WebUI. This is handy for debugging/troubleshooting.
Dex provides a `RecordEvent` API to mimic. It can be called with any arbitrary data, and they will be recorded into history just for debugging/troubleshooting.  

## Signal
Depends on different SDKs of Cadence/Temporal, there are different APIs like SignalMethod/SignalChannel/SignalHandler etc.
In Dex, just use SignalCommand as equivalent. 

In some use cases, you may have multiple signals commands and use `AnyCommandCompleted` CommandWaitingType to wait for any command completed.

## Timer
There are different timer APIs in Cadence/Temporal depends on which SDK:
* workflow.Sleep(duration)
* workflow.Await(duration, condition)
* workflow.NewTimer(duration)
* ...

In Dex, just use TimerCommand as equivalent.

Again in some use cases, you may combine signal/timer commands and use `AnyCommandCompleted` commandWaitingType to wait for any command completed.

## Query
Depends on different SDKs of Cadence/Temporal, there are different APIs like QueryHandler/QueryMethod/etc. 

In Dex, use DataObjects as equivalent. Unlike Cadence/Temporal, DataObjects should be explicitly defined in WorkflowDefinition.

Note that by default all the DataObjects and SearchAttributes will be loaded into any WorkflowState as `LOAD_ALL_WITHOUT_LOCKING` persistence loading policy. 
This could be a performance issue if there are too many big items. Consider using different loading policy like `LOAD_PARTIAL_WITHOUT_LOCKING` to improve by customizing the WorkflowStateOptions.

Also note that DataObjects are not just for returning data to API, but also for sharing data across different StateExecutions. But if it's just to share data from waitUntil API to execute API in the same StateExecution, using `StateLocal` is preferred for efficiency reason.

## Search Attribute
Dex has the same concepts of Search Attribute.
Unlike Cadence/Temporal, SearchAttribute should be explicitly defined in WorkflowDefinition.



## Parallel execution with synchronization
In Cadence/Temporal, multi-threading is powerful for complicated applications. But the APIs are hard to understand, to use, and to debug. Especially each language/SDK has its own set of APIs without much consistency.

In Dex, there are just a few concepts that are very straightforward:
1) The `execute` API can go to multiple next states. All next states will be executed in parallel
2) `execute` API can also go back to any previous StateId, or the same StateId, to form a loop. The StateExecutionId is the unique identifier. 
3) Use `InterStateChannel` for synchronization communication. It's just like a signal channel that works internally.

Some notes:

1) Any state can decide to complete or fail the workflow, or just go to a dead end(no next state).
2) Because of above, there could be zero, or more than one state completing with data as workflow results. 
3) To get multiple state results from a workflow execution, use the special API `getComplexWorkflowResult` of client API.



## Non-workflow code
Check [Client APIs](Client-APIs.md) for all the APIs that are equivalent to Cadence/Temporal client APIs.

Features like `IdReusePolicy`, `CronSchedule`, `RetryPolicy` are also supported in Dex.

What's more, there are features that are impossible in Cadence/Temporal are provided like reset workflow by StateId or StateExecutionId. 
Because WorkflowState are explicitly defined, resetting API is a lot more friendly to use. 

## Testing
For unit testing, user code should be mocking all the dependencies in `WorkflowState` implementation, including the input/output of the 
`waitUntil` and `execute` API. Users should be able to use any standard testing frameworks/libraries. 

For integration test, Dex provides a SkipTimer API to fire any timer immediately. Although users can always implement this themselves,
the API provides a standard way and saves the effort of re-inventing the wheels.

## Anything else

Is that all? For now yes. We believe these are all you need to migrate to Dex from Cadence/Temporal.

The main philosophy of Dex is providing simple and easy to understand APIs to users(as minimist), as apposed to the complicated and also huge number APIs in Cadence/Temporal. 

So what about something else like:
* Timeout and backoff retry: State waitUntil/execute APIs have default timeout and infinite backoff retry. You can customize in StateOptions.  
* ChildWorkflow can be replaced with regular workflow + signal. See this [StackOverflow](https://stackoverflow.com/questions/74494134/should-i-use-child-workflow-or-use-activity-to-start-new-workflow) for why.
* SignalWithStart: Use start + signal API will be the same except for more exception handling work. We have seen a lot of people don't know how to use it correctly in Cadence/Temporal. We will consider provide it in a better way in the future.
* Long-running activity with stateful recovery(heartbeat details): this is indeed a good one that we want to add. But this is not very commonly used yet. Please let us know if you really need it. For now a workaround is use a sub workflow instead(the sub workflow can report back the status with a signal).

If you believe there is something else you really need, open a [ticket](https://github.com/indeedeng/iwf/issues) or join us in the [discussion](https://github.com/indeedeng/iwf/discussions).

