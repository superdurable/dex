Similar to How-to-wait-for-a-workflow-to-complete.md 

External application and make a blocking API call to wait for a state execution to complete.

To wait for a workflow state execution to complete, the stateExecutionId must be provided as `waitForCompletionStateExecutionIds` in the startWorkflowOptions.

Then use `client.waitForStateExecution(stateClass)` API to wait for the completion. 

See this [example](../../sdk-java/src/test/java/io/dex/integ/TimerTest.java#L33) in Java integ test.

By default, the API will wait For the first execution. But a state could have multiple executions, and you may need to wait for the 2nd,3rd or other executions of the state.In that case, you can use `client.waitForStateExecution(stateClass, stateExecutionNumber)` to wait for the certain one by number.

`stateExecutionNumber` is a sequential number maintained by the server. Dex also allow you to specify a key instead of the number. This is helpful in some cases to have full control. See this [PR](https://github.com/indeedeng/iwf-java-sdk/pull/247) for how to use this feature.


Note1: the state that is waited for, must be registered to the workflowOptions on startWorkflow API. This is a limitation until https://github.com/indeedeng/iwf/issues/349

Note2: that currently, this feature cannot be used with reusing workflowId for more than one executions. See https://github.com/indeedeng/iwf/issues/404 for improvement which require a new field in Temporal API: https://github.com/temporalio/temporal/issues/6348