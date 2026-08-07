# Storage Pattern Implementation

This package demonstrates a singleton workflow pattern that acts as a persistent storage service.
It is important to remember that there is a strict storage size limit of 4MB for the workflow.

## Key Components

1. **StorageWorkflow**: A long-running singleton workflow that maintains state and handles storage operations through RPC calls
   from the Controller.
2. **Storage**: Class defining the storage operations.
3. **AddStorageItemRequest**: Request object for adding items to the storage workflow.

## API Endpoints

The storage pattern exposes three main operations:

- **Add Item**:
    - `POST /design-pattern/storage/add`
    - Adds a new item to the storage data

- **Get Item**:
    - `GET /design-pattern/storage/get`
    - Gets a specific item from the storage data (or null if it doesn't exist)

- **Remove Item**:
    - `POST /design-pattern/storage/remove`
    - Removes a specific item from the storage data

## Implementation Details

- Uses RPC (Remote Procedure Call) mechanism to interact with the workflow
- Automatically starts the singleton workflow if not running
- Implement RPC locking in the cases where race conditions may occur (notable example: fetching and then re-setting a
  persistence data attribute)
- If RPC is using a List/Map, a wrapper class is needed due to a limitation of Jackson, which can handle single objects. The
  wrapper class is used to wrap the List/Map and pass it as a single object containing the collection.
- Not all storage workflows have to singletons. If you are using non-singleton storage workflows, consider using
  *initialSearchAttribute* to make the workflows easier to search (e.g. per hiringEventId, jobseekerId, etc.) since these won't
  be able to have a static getWorkflowId() method.

## Usage Example

The storage pattern provides a way to maintain persistent state within a workflow, allowing for storage operations through
workflow RPCs.
- This is not useful for
    - storing large amounts data that will exceed the 4MB limit.
    - storing data that can be passed within the workflow's execution or within its own data persistence schema.
    - small workflows (<100KB), but has a large number of them (e.g. 10K or unbounded)
    - workflows that need flexible query with indexes, maybe for filtering, aggregation, or ordering
    - high volume of read QPS (>100QPS)
- This is useful if you
    - have long-lived data that needs to persist across workflow executions.
    - wanted to replace a small database.

I would encourage any team building POC/demo to use this. Also, many product MVP could start with this as the traffic/data
volume usually start with small, as long as there is a plan to switch later.
