# Slot lock and registry-version experiment

This unsafe local experiment used the in-memory repair source **579ca2373**, with
only the [recorded patch](unsafe-local-experiment.patch). It skipped both the shared
Attribute slot lock and the per-transaction registry-version read, using the cached
snapshot instead. It was never committed to server source and was reverted.

| PostgreSQL, 1M runs, ten Attributes | Safe | Experiment | Change |
| --- | ---: | ---: | ---: |
| Runs/s | 1,662.7 | 1,628.4 | -2.1% |
| Seed seconds | 343.86 | 358.72 | +4.3% |
| Catch-up seconds | 257.55 | 255.38 | -0.8% |
| WAL bytes/run | 4,525.75 | 4,500.67 | -0.6% |

There was no demonstrated throughput benefit. This single shared-host trial does
not isolate the advisory lock from the version read or explain the historical
baseline regression. The [safe comparison](../in-memory-repair/postgres-1000000-a10/search.json)
and [experiment](postgres-1000000-a10/search.json) both converged to exactly
1,000,000 runs, 950,000 Completed and 50,000 Running, with zero pending repairs,
rebuild requests, repair overflows, skipped documents, and ERROR-level server lines.

[Restoration evidence](restoration.txt), the [empty restored source diff](source-after-restore.diff),
and the [restored binary hash](restored-server-binary-sha256.txt) confirm the safe
source and binary were restored. The experiment container was removed. Raw server
logs remain local and gitignored; their hashes and counts are in the point summary.
