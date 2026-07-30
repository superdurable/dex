# Dex documentation

## Design

* [Dex Design](design/Dex-Design.md)
* [ContinueAsNew in Temporal (or Cadence)](design/ContinueAsNew-in-Temporal-(or-Cadence)-workflow.md)
* [Transient step movement](design/transient-step-movement.md)
* [IDL renames (OpenAPI → dex.proto)](design/idl-renames.md)
* [Dex Web Phase 1](design/plan/design-web.md)
* [Multi-language Rust SDK Core](design/multi-language-rust-sdk-core.md)
* [Go SDK rewrite plan](design/plan/go-sdk-rewrite.md)

## Case studies / examples

* [User sign-up/registry in Python/Java](case-study/Use-case-user-signup-workflow.md)
* [Abstracted microservice orchestration in Java/Golang](case-study/Use-case-study-%E2%80%90%E2%80%90-Microservice-Orchestration.md)
* Employer & JobSeeker engagement in [Java](../examples/java/src/main/java/io/dex/workflow/engagement) or [Golang](../examples/go/workflows/engagement)
* Subscription Workflow in [Java](../examples/java/src/main/java/io/dex/workflow/subscription) or [Golang](../examples/go/workflows/subscription)

## Wiki

### Basic concepts

* [Basic concepts overview](wiki/Basic-concepts-overview.md)
* [WorkflowState](wiki/WorkflowState.md)
* [RPC](wiki/RPC.md)
* [Persistence](wiki/Persistence.md)
* [Client APIs](wiki/Client-APIs.md)
* [Compare with Cadence/Temporal](wiki/Compare-with-Cadence-Temporal.md)

### Advanced concepts

* [WorkflowOptions](wiki/WorkflowOptions.md)
* [WorkflowConfig](wiki/WorkflowConfig.md)
* [WorkflowContext](wiki/WorkflowContext.md)
* [WorkflowStateOptions](wiki/WorkflowStateOptions.md)
* [Conditional complete workflow with checking channel emptiness](wiki/Conditionally-complete-workflow-with-atomic-checking-on-signal-or-internal-channel.md)
* [WaitForStateExecutionCompletion](wiki/How-to-wait-for-a-workflow-state-to-complete.md)
* [Dex limitation](wiki/Dex-limitation.md)
* [Persistence Caching (experimental)](wiki/Persistence-Caching.md)
* [RPC locking](wiki/RPC-locking-What-does-the-atomicity-of-RPC-really-mean.md)
* [SignalChannel vs InternalChannel](wiki/SignalChannel-vs-InternalChannel.md)

### Operation

* [Dex application operation](wiki/Dex-Application-Operations.md)
* [Dex server operation](wiki/Dex-Server-Operations.md)
* [How to modify/version Dex workflow safely](wiki/%5BVersioning%5DHow-to-modify-workflow-code-without-breaking-changes.md)
* [How to change server config in docker](wiki/How-to-change-server-config-in-docker.md)

### FAQ

* [SignalChannel vs InternalChannel](wiki/SignalChannel-vs-InternalChannel.md)
* [Using Dex as storage system](wiki/What-are-Pros-and-Cons-of-using-Dex-as-a-database-for-permanent-data-storage.md)
* [How Dex works & design](design/Dex-Design.md)
* [Data Persistence vs StateExecutionLocal vs input](wiki/Using-persistence-vs-State-input-vs-StateExecutionLocal-to-pass-data.md)
* [RPC atomicity](wiki/RPC-locking-What-does-the-atomicity-of-RPC-really-mean.md)
* [Dex limitation](wiki/Dex-limitation.md)
* [Wait for workflow to complete](wiki/How-to-wait-for-a-workflow-to-complete.md)
* [Wait for workflow state to complete](wiki/How-to-wait-for-a-workflow-state-to-complete.md)
* [How does waitForStateExecutionCompletion works](wiki/How-does-waitForStateCompletion-work.md)
