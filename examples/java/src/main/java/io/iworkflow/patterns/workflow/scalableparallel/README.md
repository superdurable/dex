# Scalable Parallelism Control Pattern

Here we demonstrate a Parent-Child Workflow Pattern implemented using the iWF.</br>
This pattern demonstrates how workflows can be designed with scalable parallelism control.

This is an advanced design pattern for high scalability needs. For simpler approach for controlling parallelism, you can check out [parallel](../parallel) design pattern or [parentchild](../parentchild)

## Overview

This design pattern can accept unlimited requests, and then dispatch them into different parents based on partitioning, and then each parent will control the parallelism of executing child workflows. So total concurrency = numberOfParents * numOfChildrenPerParent. By scaling up numberOfParents,you can have any number of total concurrency/parallelism of executing child workflows. 

We can consider the Parent Workflow to be a controller and the Child Workflow a task processor. This is particularly useful in scenarios where a simple task needs to be handled multiple times and those tasks can be parallelized. Limits on how many Parent workflows can be created and how many Child workflows each of them can control can and should be applied. All tasks are queued and processed in a FIFO manner. The queue also has a limit on how many tasks can be stored in it at a time. If the limit is reached, further requests will be rejected.

Because of request can be rejected, the request will be first sent to RequestReceiverWorkflow as "buffer". The request workflow will then keep on retrying to send to parent if being rejected.

### Key Components

NOTE: Term `request` and `task` are used in a specific context. Request consist of multiple tasks. For example, in a line-by-line CSV processing scenario, `request` would be seen as a file to process and `task` would be a single line that is processed by a ChildWorkflow.

1. **RequestReceiverWorkflow**: The workflow handling the incoming requests. It sends all received request to a randomly chosen ParentWorkflow. If the ParentWorkflow's request queue has not enough capacity to take on new tasks, it will reject the request. The RequestReceiverWorkflow will retry to send again later. It may retry on a different parent workflow which has capacity. RequestReceiverWorkflow is buffering the requests until handed off to a ParentWorkflow.
2. **ParentWorkflow**: The controller workflow that manages the child workflows. It receives requests from the RequestReceiverWorkflow and starts a ChildWorkflow for each task. It also manages the queue of tasks and the number of active ChildWorkflows.
    - **Request Queue** (internal channel): The queue of tasks that the ParentWorkflow manages. It has a limit on how many tasks can be queued at a time. If the limit is reached, the further requests will be rejected.
    - **Child Complete** (internal channel): The channel that is used to signal the completion of a ChildWorkflow back to the ParentWorkflow. It allows the ParentWorkflow to know when it is safe to start a new ChildWorkflow.
3. **ChildWorkflow**: The processing workflow that handles the actual task. It receives the task from the ParentWorkflow and processes it.

### Scalability

The pattern is designed to be scalable in multiple ways. The main variables that can be scaled are:
- `NUM_PARENT_WORKFLOWS`:
  - Number of parent (controller) workflows to control the concurrent processing of tasks (ChildWorkflows)
- `CONCURRENCY_PER_PARENT_WORKFLOW`:
  - The number of parallel child workflows that each parent workflow can control
- `MAX_BUFFERED_TASKS`
  - Maximum number of elements in the `TASK_QUEUE` as buffer, before processing

### Endpoints

The application exposes the following REST endpoints:

- **Start Request Workflow**:
    - `GET /design-pattern/parallelism/start?workflowId={workflowId}&numOfChildWfs={number}`
    - Starts the RequestReceiverWorkflow with the specified `workflowId` by requesting a `number` of tasks to be processed. In a real-world scenario, this endpoint would take an object or a reference to the object (URL path) to be processed.

## Use Cases and Considerations

### **Use Cases**

- **CSV File Processing**: If a request object is a CSV file, the workflow can process each row of the CSV file as a separate task.
- **Large Data Processing**: If the request object is a large dataset, the workflow can analyze the data in chunks. 
  - NOTE: It is suggested to not pass and store the actual data in iWF workflow (>100KB), but use a reference instead (e.g. S3 ObjectID to the CSV file and the row index).
- **Any Other Parallelizable Task**: Any task that can be parallelized and does not require a specific order of execution can be processed using this pattern.

### **Considerations**

- **Temporal Workflow Executions**: For a single workflow execution, Temporal has a hard limit of 2000 pending (in-parallel) activities, and recommended threshold of 500 for optimal performance. An activity is mapped to a State API execution in iWF. This means it is recommended to have no more than 500 in-parallel state executions for an iWF workflow execution. Having less than 100 in-parallel state executions is even more optimal for iWF using localActivity optimization, because Temporal will count them all as one action.
- **Temporal Workflow History Size**: There is also a limitation in Temporal history per execution. It cannot exceed 50MB, and should be within a few MBs. The snapshot of the workflow execution cannot exceed that.
- **Downstream Services Limits**: Services used for processing data may also have concurrency limits.

## Workflow Details

### Request Receiver Workflow

- **States**:
    - `RequestState`: Receives a request and starts assigns it to a ParentWorkflow.

### Parent Workflow

- **States in the order of execution**:
    - `InitState`: Initializes the workflow.
    - `LoopForMessageState`: The main logic lives in here: It will wait for messages from TASK_QUEUE and/or messages of childWorkflow completion. If the CONCURRENCY_PER_PARENT_WORKFLOW is not met, then consume a next message for TASK_QUEUE to start a child workflow. At the sametime, check if there are any childWorkflow are being waited -- if so, then wait for the completion messages of the child workflow. 

The parent workflow completes immediately when there are no child workflows running AND no task in the TAKS_QUEUE.

### Child Workflow

- **States**:
    - `ProcessingState`: Performs a task. In real world, it could be any long running and complex workflow. At the end, it should send message to parent to inform the completion via RPC and internalChannel.

## Conclusion

The Parent-Child Workflow Pattern provides a scalable and efficient way to handle parallel processing of tasks. It is particularly useful for scenarios where the processing of tasks can be parallelized and does not require a specific order of execution. However, it is important to consider the limitations of Temporal and downstream services when designing workflows. By understanding these limitations and the described considerations, you can design workflows that are efficient in terms of resource usage and performance.
