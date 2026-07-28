<!---
---

---
--->

<!---## DOCHUB-PATH: advanced-concepts/WorkflowStateOptions.mdx :DOCHUB-PATH ##--->

Users can customize the WorkflowState

## WorkflowState WaitUntil/Execute API timeout and retry policy

Retry is built-in in Dex WorkflowState. Returning any error will just let the state API(waitUntil/execute) retry until the attempts has maxed out the retryPolicy.

Generally, it's recommended to NOT catch retryable (e.g. 5xx status from REST APIs, service internal error or unavailable, timeouts), or a generic error (e.g. Exception in Java), otherwise you lose the benefits of the retry. In other words, it's anti-pattern to catch those exceptions (unless catch and re-throw after logging).

By default, the API timeout is 30s with infinite backoff retry. 
Users can customize the API timeout and retry policy:

- InitialIntervalSeconds: 1
- MaxInternalSeconds:100
- MaximumAttempts: 0
- MaximumAttemptsDurationSeconds: 0
- BackoffCoefficient: 2

Where zero means infinite attempts.

Both MaximumAttempts and MaximumAttemptsDurationSeconds are used for controlling the maximum attempts for the retry
policy.
MaximumAttempts is directly by number of attempts, where MaximumAttemptsDurationSeconds is by the total time duration of
all attempts including retries. It will be capped to the minimum if both are provided.



## State API failure handling/recovery after retries are exhausted 

By default, the workflow execution will fail when State APIs max out the retry attempts. But in some cases you want the
workflow to handle cleanup/compensation after all retries have failed(as SAGA pattern).

!!! Do not use this feature unless necessary -- in most cases, you should just let a workflow fail and get alerted -- which is simpler to do !!!

### Execute API
For Execute API, you can set `PROCEED_TO_CONFIGURED_STATE` as failure policy, with a `ProceededState` configured.
The proceeded state will take the same input from the original failed state.

When the current state fails, instead of failing the workflow, it will go to the ProceededState for recovery.

Then in the recovery(proceeded) state, it's up to state to continue to run the workflow, or fail the workflow. 

Thius failure policy are especially helpful for recovery logic. 

For example, a `DebitState` is making three API calls for the debit operation but failed at the 3rd one. You want to undo the first two. In that case, you can set a `UndoDebitState` as the recovery state for the DebitState. When DebitState fails, instead of failing workflow, it will proceed to `UndoDebitState` to let you undo the first two operations. 

<!---
import { FaJava, FaPython } from "react-icons/fa";
import Tabs from '@theme/Tabs';
import TabItem from '@theme/TabItem';

<div style={{
    "border": "1px darkgray solid",
    "borderRadius": "1rem",
    "padding": "0.5rem"
}}>
<Tabs>
    <TabItem value="java" label={<><FaJava size={26} /> Java</>}>
--->
<!--- ## GITHUB-ONLY ## --->
Example in Java SDK:
<!--- ## END-GITHUB-ONLY ## --->
```java
public class DebitState extends WorkflowState {
    @Override
    public WorkflowStateOptions getStateOptions() {
        return new WorkflowStateOptions()
                .setProceedToStateWhenExecuteRetryExhausted(UndoDebitState.class)
                // make sure the retry duration is less than the workflow timeout so that recovery state has a chance to run
                .setExecuteApiRetryPolicy(...); 
    }
   
    @Override
    public StateDecision execute(...){ 
       // make three API calls for a debit operation
    }
}
```

<!--- ## GITHUB-ONLY ## --->
In Golang SDK:
```go
type debitState struct{
    dex.WorkflowStateDefaultsNoWaitUntil
}

func (b debitState) GetStateOptions() *dex.StateOptions {
	return &dex.StateOptions{
           // make sure the retry duration is less than the workflow timeout so that recovery state has a chance to run options.
           ExecuteApiRetryPolicy: &dexpb.RetryPolicy{...},

           ExecuteApiFailureProceedState: undoDebitState{},
        }
}


func (b debitState) Execute(ctx dex.WorkflowContext, input dex.Object, commandResults dex.CommandResults, persistence dex.Persistence, communication dex.Communication) (*dex.StateDecision, error) {
       // make three API calls for a debit operation
}
```
<!--- ## END-GITHUB-ONLY ## --->

<!---
</TabItem>
<TabItem value="python" label={<><FaPython size={26} /> Python</>}>
--->
<!--- ## GITHUB-ONLY ## --->
In Python SDK:
<!--- ## END-GITHUB-ONLY ## --->
```python
class DebitState(WorkflowState[None]):
    def execute(
        self,
        ctx: WorkflowContext,
        input: T,
        command_results: CommandResults,
        persistence: Persistence,
        communication: Communication,
    ) -> StateDecision:
        # make three API calls for a debit operation
        return StateDecision...

    def get_state_options(self) -> WorkflowStateOptions:
        return WorkflowStateOptions(
            execute_api_retry_policy=RetryPolicy(...), //make sure recoveryState has enough time to run
            proceed_to_state_when_execute_retry_exhausted=UndoDebitState,
        )
```
<!---
</TabItem>
</Tabs>
</div>
--->

### WaitUntil API

For WaitUntil API, using `PROCEED_ON_API_FAILURE` for `WaitUntilApiFailurePolicy` will let workflow continue to invoke `execute`
API when the API fails with maxing out all the retry attempts.

See example here in [Java](../../sdk-java/src/test/java/io/dex/integ/basic/ProceedOnStateStartFailWorkflowState1.java#L45) and [Golang](../../sdk-go/integ/proceed_on_state_start_fail_workflow_state1.go#L36).

This is very uncommonly needed than the failure policy of Execute API. Currently not implemented in Python SDK yet.

## State/RPC API Context
There is a context object when invoking RPC or State APIs. It contains information like workflowId, startTime, etc.

For example, WorkflowState can utilize `attempts` or `firstAttemptTime` from the context to make some advanced logic.

## StateOptionsOverride 
To have a different WorkflowStateOptions, normally you just need to implement the method of the WorkflowState interface. 

But in some rare cases, you may need it to be more dynamic -- for example, different state executions could have a different retry policy, even they are from the same state definitions. 

To achieve this, you can provide an [stateOptionsOverride to the StateMovement of StateDecision](../../sdk-java/src/main/java/io/dex/core/StateDecision.java#L152).
