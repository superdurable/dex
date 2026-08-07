# Drain Internal Channels Flow

One branch of the flow (the producer) publishes commands to an internal channel
while another branch (the consumer) loops receiving them. The consumer must not
be shut down abruptly or messages would be lost, so the producer sends a final
"drain" message that tells the consumer it is safe to complete.

## Use cases

- A consumer that reads messages and upserts them into a data store.

## Steps

1. `Init` — starts `UpsertMongoRecord` and `ProcessData` as two parallel
   branches.
2. `ProcessData` — publishes documents to the internal channel, calls the fake
   external service, and loops until it has run four times.
3. `UpsertMongoRecord` — waits for documents on the internal channel and upserts
   each one into a fake MongoDB.
4. `Finalize` — publishes a final document with `final_command=True` so
   `UpsertMongoRecord` can gracefully complete, then completes the flow.

With more than one producer branch, `Finalize` would first wait for every
producer to finish before sending the final message.

## Channels

- `upsert_mongo_data_internal_channel` — carries `MongoDocument` values between
  steps.

## Persistence

- `process_data_state_execution_counter` — an `int` attribute tracking how many
  times `ProcessData` has run.
