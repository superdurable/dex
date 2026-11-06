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

A PostgreSQL store has three optional design settings. Settle them together with the Flow ID format before the first write.

```yaml
attributeStore:
  stores:
    orders:
      type: postgres
      dsn: postgres://user:password@host/database
      tableName: public.orders
      rowKey: order-{customer_id}-{order_id}
      columnNaming: snake_case
      attributeMaps:
        LineItems:
          tableName: public.order_line_items
          instanceKeyColumn: line_item_id
```

**`rowKey` makes the Flow ID format a storage contract.** The template splits each Flow ID into the table's primary-key columns.

- `{column}` captures text into that key column. `{*}` matches without storing. Each placeholder captures up to the next literal text; the last captures the rest. Literal text must separate two placeholders, and no capture may be empty.
- `order-{customer_id}-{order_id}` splits `order-c1-o-7` into `customer_id` = `c1` and `order_id` = `o-7`.
- A Flow ID that does not match skips its whole batch with an error log. A retry cannot fix it. Select a `rowKey` store only on Flow types whose IDs match.
- The table's primary key must be exactly the template's columns, all character columns. Attributes cannot write a key column.
- Several stores may name one table. Each upsert sets only its own batch's columns. Give each column one owning Flow type.
- Dex never migrates rows, so a later change to the template or the Flow ID format is an application data migration.

**`columnNaming: snake_case`** maps each Attribute, and each field of a map instance, to its snake_case column: `DeploymentURL` becomes `deployment_url`. Two names in one batch that map to one column are both filtered. Without it, names must equal column names.

**`attributeMaps`** writes each instance of a named AttributeMap as a row of its own table, keyed by the store's key columns plus the instance-key column.

- Writing an instance replaces its row. Deleting the instance deletes the row.
- A JSON object value writes each top-level field to the column of the same name; omitted columns return to their default or NULL. Any other value goes to a column named `value`.
- Write each instance as one complete value. A partial write erases the omitted columns.
- A field without a column is skipped with a warning. A value that does not convert is logged and skipped.
- The store row and its instance rows commit in one transaction. Do not add foreign keys between them: a violation fails the whole batch and holds back later writes.
- Without this setting, an instance is projected as an ordinary Attribute named by the map and the key, and an instance without a matching column is filtered. Verify how every physical instance name maps before syncing a dynamic map.

## Schema changes

- A new nullable or defaulted column needs no Server restart, as long as its migration completes before the Workers that write it start.
- Removing or changing a column makes later writes filtered or retried.
- Watch the Server log for filtered writes, skipped batches, and exhausted retries.

Configuration keys, retry settings, the fencing table statement, the SQL type contract, and metrics are in the [Server Attribute Store reference](https://docs.superdurable.io/reference/server/attribute-store-sync). For local development, `dexcli dev` accepts an Attribute Store configuration file; see the [CLI reference](https://docs.superdurable.io/reference/cli/#start-locally).

## Self-check

- Is the Flow the only writer of each projected fact?
- Do the Flow ID format and the `rowKey` template match for every Flow type that selects the store?
- Are tables and the fencing table created before the Server starts?
- Can the projection be reconciled after a dropped write?
- Does no Flow decision read the projection?
