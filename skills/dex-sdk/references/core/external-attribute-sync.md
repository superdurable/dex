# External Attribute Sync

External Attribute Sync projects selected Attribute writes into PostgreSQL tables that the application owns. Each destination is an Attribute Store. It is a Dex Server feature, not a Worker feature. The application and its reports query the tables with SQL.

## Contract

- The Flow stays the source of truth. The table is an eventual, latest-state projection. Never read it to make a Flow decision.
- A Flow opts in per Attribute or AttributeMap with the sync-to-Attribute-Store option, and names one or more configured stores in its Flow configuration at start. Initial Attribute values also project.
- PostgreSQL is the only store type.
- Dex never creates, migrates, or backfills a destination. Create every table, and the Dex fencing table, before the Server starts. An invalid destination fails Server startup.
- Delivery is asynchronous and at least once, for one Flow at a time. Every write is an idempotent upsert keyed by the Flow ID.
- Writes never block a Step, RPC, or start commit. Rows normally appear shortly after the commit.
- While a store is unavailable, Flows keep running. Failed writes retry for a bounded time, then drop with an error log. A projection failure never rolls back Flow state.
- Dex does not replay old Attributes, re-send a dropped batch, or backfill a newly configured store. Make important projections reconcilable from the Flow.
- After time travel, Dex rewrites the run's synced rows from the restored state.
- Deleting an Attribute keeps the row and projects NULL. Dex never deletes rows when a Flow closes. Give the table its own retention.
- Hard-delete Attributes cannot be synced.
- Update or delete projected data only through the Flow.

## Flow close

A naturally closing Flow stays running until its pending synced writes are written or dropped, and until transactional or locking RPCs admitted earlier finish. WaitForFlow keeps waiting meanwhile. A dropped write still needs reconciliation, so completion does not prove the table is current.

StopFlow closes the run at once; remaining writes keep syncing after the close.

## Row keys, column names, and AttributeMap tables

A store can split each Flow ID into the table's key columns with a row key template, map Attribute names to column names, and write each instance of an AttributeMap as a row of its own table. Settle these choices together with the Flow ID format before the first write.

- A row key template makes the Flow ID format a storage contract. Choose the Flow ID format and the template together.
- A Flow ID that does not match the template writes no row, and a retry cannot fix it. Select such a store only on Flow types whose IDs match.
- Attributes never write the key columns.
- Dex never migrates rows. A later change to the template or to the Flow ID format is an application data migration.
- Several stores may write one table. Each write sets only its own columns. Give each column one owning Flow type.
- An AttributeMap table holds one row per instance. Writing an instance replaces its row, and deleting the instance deletes the row.
- Write each instance as one complete value. A partial value erases the columns it omits.
- The store row and its instance rows commit together. Do not add foreign keys between them: a violation fails the whole batch and holds back later writes.
- A value with no matching column, or one that does not convert, is skipped with a log entry. The Flow never sees that error. Before you sync, verify how every Attribute name and every instance name maps to a column, especially for a dynamic map.

## Schema changes

- Add a column before the Workers that write it start.
- Never remove or change a column that a Flow still writes. Later writes to it are skipped or retried.
- Watch the Server log for skipped writes, skipped batches, and exhausted retries.

The configuration format, the row key template syntax, column naming, value conversion, schema refresh, retry settings, the fencing table statement, the SQL type contract, and metrics are in the [Server Attribute Store reference](https://docs.superdurable.io/reference/server/attribute-store-sync). For local development, `dexcli dev` accepts an Attribute Store configuration file; see the [CLI reference](https://docs.superdurable.io/reference/cli/#start-locally).

## Self-check

- Is the Flow the only writer of each projected fact?
- Do the Flow ID format and the row key template match for every Flow type that selects the store?
- Are tables and the fencing table created before the Server starts?
- Can the projection be reconciled after a dropped write?
- Does no Flow decision read the projection?
