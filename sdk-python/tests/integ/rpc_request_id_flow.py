# Portions of this file are derived from indeedeng/iwf-java-sdk.
# Those portions are licensed under the Apache License, Version 2.0.
# See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
#
# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications are licensed under the Sustainable Use License 1.0.
# Third-Party Materials remain under the Apache License, Version 2.0.
# See LICENSE and LEGACY_NOTICES.md.

import threading
from collections import Counter

from dex import Attribute, Context, Flow, PersistenceSchema, RPCResult, rpc


class RpcRequestIdFlow(Flow[None]):
    total = Attribute("request-id-total", int)
    processed = Attribute("request-id-processed", bool)

    def __init__(self) -> None:
        self._handler_runs_lock = threading.Lock()
        self._handler_runs: Counter[tuple[str, str]] = Counter()

    def get_flow_type(self) -> str:
        # Other SDK suites may share the test Server; a unique type keeps their Workers apart.
        return "PythonRpcRequestIdFlow"

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(self.total, self.processed)

    def handler_runs(self, flow_id: str, rpc_name: str) -> int:
        with self._handler_runs_lock:
            return self._handler_runs[(flow_id, rpc_name)]

    @rpc(is_transactional=True)
    def add_transactional(self, context: Context, delta: int) -> RPCResult[int]:
        self._record_handler_run(context, "add_transactional")
        return self._add(context, delta)

    @rpc
    def add_non_transactional(self, context: Context, delta: int) -> RPCResult[int]:
        self._record_handler_run(context, "add_non_transactional")
        return self._add(context, delta)

    @rpc
    def mark_processed_non_transactional(self, context: Context) -> RPCResult[str]:
        return self._mark_processed(context)

    @rpc(is_transactional=True)
    def mark_processed_transactional(self, context: Context) -> RPCResult[str]:
        return self._mark_processed(context)

    @rpc
    def get_total(self, context: Context) -> RPCResult[int]:
        return RPCResult(self.total.get(context) or 0)

    def _add(self, context: Context, delta: int) -> RPCResult[int]:
        updated_total = (self.total.get(context) or 0) + delta
        self.total.set(context, updated_total)
        return RPCResult(updated_total)

    def _mark_processed(self, context: Context) -> RPCResult[str]:
        if self.processed.get(context):
            return RPCResult("already_processed")
        self.processed.set(context, True)
        return RPCResult("accepted")

    def _record_handler_run(self, context: Context, rpc_name: str) -> None:
        with self._handler_runs_lock:
            self._handler_runs[(context.flow_id, rpc_name)] += 1
