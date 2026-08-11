# Entity Store

`UserProfileFlow` keeps the authoritative profile in Dex while selected Attributes
are projected asynchronously to PostgreSQL through the `entityStore` Attribute Store.

The Flow ID is the `user_profiles.user_id` primary key. Initial values, updates, and
deletions all opt in through the same Attribute definitions. Clearing a profile writes
SQL `NULL` without rolling back the durable Flow Attribute deletion if projection fails.

Use the shared [PostgreSQL setup](https://github.com/superdurable/dex/tree/main/examples/entity-store)
to run this example.
