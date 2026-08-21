<!---
---

---
--->

<!---## DOCHUB-PATH: advanced-concepts :DOCHUB-PATH ##--->

Dex let you deeply customize the workflow behaviors with the below options.

#### IdReusePolicy for WorkflowId

At any given time, there can be only one WorkflowExecution running for a specific workflowId.
A new WorkflowExecution can be initiated using the same workflowId by setting the appropriate `IdReusePolicy` in
WorkflowOptions.

* `ALLOW_IF_NO_RUNNING` 
    * Allow starting workflow if there is no execution running with the workflowId
    * This is the **default policy** if not specified in WorkflowOptions
* `ALLOW_IF_PREVIOUS_EXITS_ABNORMALLY`
    * Allow starting workflow if a previous Workflow Execution with the same Workflow Id does not have a Completed
      status.
      Use this policy when there is a need to re-execute a Failed, Timed Out, Terminated or Cancelled workflow
      execution.
* `DISALLOW_REUSE` 
    * Not allow to start a new workflow execution with the same workflowId.
* `ALLOW_TERMINATE_IF_RUNNING`
    * Always allow starting workflow no matter what -- Dex server will terminate the current running one if it exists.

Note that the behavior is limited by Cadence/Temporal workflow retention, which is configured at domain/namespace level.
If a workflow is deleted after retention expired, the workflowId can be started regardless of any Policy

#### RetryPolicy for workflow

Workflow execution can have a backoff retry policy which will retry on failed or timeout.

By default, there is no retry policy.

#### Initial Search Attributes

Client can specify some initial search attributes when starting the workflow.

By default, there is no initial search attributes.
