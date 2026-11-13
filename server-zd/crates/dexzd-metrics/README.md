# dexzd-metrics

## Purpose

Own the fixed, process-local OpenMetrics registry and typed recording handles for a server-zd node.

The registry is built once per node. Recording uses atomic counters and gauges without an engine lock. Every handle exists at every metrics level; the level only selects which families the registry exports. The `FAMILY_LEVELS` table in `src/metrics_level.rs` assigns each family its level. At the verbose level, Linux scrapes also include resident memory, CPU time, open file descriptors, and process start time from `/proc/self`; these four process metrics are absent on other platforms.
