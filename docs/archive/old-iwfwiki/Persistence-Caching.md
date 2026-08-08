NOTE: this is an experimental feature.

By default, remote procedure calls (RPCs) will load data/search attributes with the Cadence/Temporal [query API](https://docs.temporal.io/workflows#query),
which is not optimized for very high request volume (~>100 requests per second) on a single workflow execution. Such request volumes could cause
too many history replays, especially when workflows are closed. This could in turn produce undesirable latency and load.

You can enable **caching** to support those high-volume requests.

Note:
* With caching enabled read-after-write access will become *eventually consistent*, unless `bypassCachingForStrongConsistency=true` is set in RPC options
* Caching will introduce an extra event in history (upsertMemo operation for WorkflowPropertiesModified event) for updating the persisted data attributes
* Caching will be more useful for read-only RPC (no persistence.SetXXX API or communication API calls in RPC implementation) or GetDataAttributes API.
  * A read-only RPC can still invoke any other RPCs (like calling other microservices, or DB operation) in the RPC implementation
* Caching is currently only supported if the backend is Temporal, because [Cadence doesn't support mutable memo](https://github.com/uber/cadence/issues/3729)
