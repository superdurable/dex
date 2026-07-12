-- Copyright (c) 2025 superdurable
-- SPDX-License-Identifier: MIT
--
-- Reset all dex data for a fresh benchmark run (Postgres). Truncates every
-- table across the six per-store databases but preserves the schema (tables +
-- indexes), so you do NOT need to re-run v0.sql afterwards. Mirrors
-- mongo/schema/reset-data.js. Run:
--   psql "${DEX_POSTGRES_URI}" -f reset-data.sql
--
-- WARNING: destructive. All runs, tasks, blobs, shard leases, tasklist
-- metadata + queues, visibility rows, and history events are permanently
-- deleted.

\connect dex_runs
TRUNCATE runs, immediate_tasks, timer_tasks, opsfifo_tasks, task_dlq;

\connect dex_blobs
TRUNCATE blobs;

\connect dex_shards
TRUNCATE shards;

\connect dex_tasklists
TRUNCATE tasklist_metadata, tasklist_tasks;

\connect dex_visibility
TRUNCATE visibility;

\connect dex_history
TRUNCATE history;
