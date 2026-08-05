-- Copyright (c) 2022-2026 Super Durable, Inc.
--
-- Permission is hereby granted, free of charge, to any person obtaining a copy
-- of this software and associated documentation files (the "Software"), to deal
-- in the Software without restriction, including without limitation the rights
-- to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
-- copies of the Software, and to permit persons to whom the Software is
-- furnished to do so, subject to the following conditions:
--
-- The above copyright notice and this permission notice shall be included in
-- all copies or substantial portions of the Software.
--
-- THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
-- IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
-- FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
-- AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
-- LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
-- OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
-- THE SOFTWARE.

CREATE TABLE IF NOT EXISTS dataset_deal_processes (
    process_id TEXT PRIMARY KEY,
    definition JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS dataset_deal_executions (
    flow_id TEXT PRIMARY KEY,
    latest_run_id TEXT NOT NULL,
    process_id TEXT NOT NULL REFERENCES dataset_deal_processes(process_id),
    buyer_id TEXT NOT NULL,
    process_definition JSONB NOT NULL,
    state_data JSONB NOT NULL,
    current_state TEXT NOT NULL DEFAULT '',
    target_state TEXT NOT NULL DEFAULT '',
    current_action_phase TEXT NOT NULL DEFAULT '',
    current_action_index_to_execute INTEGER NOT NULL DEFAULT 0,
    pending_condition_state TEXT NOT NULL DEFAULT '',
    pending_condition_name TEXT NOT NULL DEFAULT '',
    pending_condition_phase TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL,
    version BIGINT NOT NULL DEFAULT 1,
    last_step_execution_id TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ,
    CHECK (current_action_phase IN ('', 'pre', 'post')),
    CHECK (current_action_index_to_execute >= 0),
    CHECK (pending_condition_phase IN ('', 'pre', 'post')),
    CHECK (status IN ('PROCESSING', 'WAITING', 'COMPLETED'))
);

CREATE INDEX IF NOT EXISTS dataset_deal_executions_process_idx
    ON dataset_deal_executions (process_id, created_at DESC, flow_id);

CREATE INDEX IF NOT EXISTS dataset_deal_executions_buyer_process_idx
    ON dataset_deal_executions (buyer_id, process_id, created_at DESC, flow_id);

CREATE INDEX IF NOT EXISTS dataset_deal_executions_status_idx
    ON dataset_deal_executions (status, created_at DESC, flow_id);

CREATE INDEX IF NOT EXISTS dataset_deal_executions_current_state_idx
    ON dataset_deal_executions (current_state, created_at DESC, flow_id);

CREATE INDEX IF NOT EXISTS dataset_deal_executions_pending_condition_idx
    ON dataset_deal_executions (pending_condition_name, created_at DESC, flow_id);
