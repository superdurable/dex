# Variant: shed without repair

Source: **dcf3492c09fe583df0dc8988ea3ccbf139a37458**. The owner replaced this policy after the PostgreSQL 100k point. The 1M point was canceled during seeding and its container was removed; no partial throughput result is published.

PostgreSQL, 100k runs, ten Attributes: seed **28.86 s**, catch-up **22.27 s**, **1,955.8 runs/s**, **768.71 WAL bytes/run**. Coalesced **9,575**, dropped intermediate **0**, close **81,965**, start **89,925**, rebuild requests **0**. Final projected **17,194** runs: **16,485** Completed, **709** Running (including **34** stale closes), and **82,806** absent. The drops count changes, not unique runs.

This policy never repaired dropped changes. Catch-up used the kept-change visibility barrier, so it did not imply complete search visibility. The [primary report](../README.md) uses bounded idle repair and exact final convergence. Raw scrapes, counts, engine metadata, binary hash, and container-removal receipts are retained here; compressed server logs remain local and gitignored.
