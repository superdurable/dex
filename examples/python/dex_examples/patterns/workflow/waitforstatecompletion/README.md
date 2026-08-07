# Wait For State Completion

Starts a flow and waits until a specific step has finished before reading the
data it persisted. Callers get a synchronous-feeling API on top of an
asynchronous flow.

## Steps

1. `PersistData` — upserts the job seeker into the data store, saves it to the
   `job_seeker_data` attribute, then moves to `UpdateExternalSystem`.
2. `UpdateExternalSystem` — pushes the same record to an external system and
   completes the flow.

## RPCs

- `get_job_seeker_data` — returns the persisted record, raising if `PersistData`
  has not written it yet.

## Usage

The caller starts the flow, waits for `PersistData` to complete, then invokes
`get_job_seeker_data`. Waiting on the step rather than the whole flow means the
caller does not block on the slower external-system update.

## Persistence

- `job_seeker_data` — holds the persisted `JobSeekerData`.
