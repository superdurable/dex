# How waitForStateCompletion works

What is **Signal-With-Start** in [Temporal](https://docs.temporal.io/encyclopedia/workflow-message-passing#signal-with-start):

- Signal a workflow if it's running (already started), otherwise, start and signal it atomically
- Temporal’s definition:
  - Signal-With-Start is a great tool for lazily initializing Workflows. When you send this operation, if there is a running Workflow Execution with the given Workflow Id, it will be Signaled. Otherwise, a new Workflow Execution starts and is immediately sent the Signal.

For Dex’s **waitForStateExecutionCompletion** API, there are several cases:

**Case 1:**
- API Service waits for a state that has not been completed.
  - The service will start the **waitForStateCompletion** workflow (child workflow), and then long-poll waiting for it.
  - The waiting could timeout, and then the client would retry again waiting.
    - The service will try to start the same **waitForStateCompletion** workflow again, but will ignore the already started error. And then do another long-poll waiting for it.
  - At some time, the state will be completed, and workflowImpl will Signal-With-Start the **waitForStateCompletion** workflow.
    - Because the **waitForStateCompletion** workflow is already running, the Signal-With-Start is essentially doing a signal only.
  - Then the **waitForStateCompletion** workflow will complete.
  - Then the long-poll API will be unblocked, and return to the client (SDK).

The _only_ purpose of the child workflow (**waitForStateCompletion**) is just waiting for it to complete. 

**Case 2:**
- The state that is awaited was completed, before a client makes any API call to wait for it.
  - workflowImpl will Signal-With-Start the **waitForStateCompletion** workflow.
    - Behind the scene, it's start + signal the **waitForStateCompletion** workflow (atomically).
    - The **waitForStateCompletion** workflow will be started, and also completed _immediately_.
- Later, at some point, a client makes an API call to wait for the state.
  - The service will try to start the **waitForStateCompletion** workflow, but it's already started. It will ignore the error. 
  - The service will then wait for the **waitForStateCompletion** to complete, and it returns immediately (because it's already completed).
