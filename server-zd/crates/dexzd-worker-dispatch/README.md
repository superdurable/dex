# dexzd-worker-dispatch

## Purpose

Pull Worker protocol server for server-zd: Worker registration, task queues, matching, cancellation, and keepalive.

`WorkerDispatch` owns in-memory Worker streams. Construct it with `MatchingConfig` and a `WallClock`, then register `service_for_tenant(0)` with tonic. The tenant ID comes from trusted server routing; Workers send the existing protocol unchanged.

`dispatch` accepts a Flow ID, Flow type, optional sticky Worker ID, method deadline, and one unmodified WaitFor, Execute, or RPC request. Its handle yields ordered non-final frames and one final outcome. Dropping or cancelling the handle requests task cancellation; an unresponsive Worker stream closes after one second. `shutdown` asks idle Workers to reconnect immediately and busy Workers to reconnect after their current task.

A dispatch that finds no idle stream waits on its queue. Waiting dispatches are woken in the order the dispatches started, and a dispatch keeps that place when it falls back from the sticky queue to the Flow type queue. A dispatch that arrives while a stream is idle can still take that stream before a woken dispatch does.

Each stream that becomes idle wakes only the longest-waiting dispatch that can use it, so an idle stream normally causes a single wakeup however many dispatches wait. A dispatch cannot use a stream that rejected its task for capacity until the backoff ends, or any stream of a Worker ID that rejected it as unknown. Finding the dispatch to wake scans the waiters under the matching lock and passes over every waiter that cannot use the stream. A woken dispatch that takes a different stream, or ends first, wakes another dispatch for the stream it was woken for.

Worker frames and dispatch events each use a 32-frame buffer, so a slow engine applies backpressure to the Worker. A waiting dispatch returns `ServerClosing` when matching shuts down.

The registry is local to one node and is intentionally not persisted. The engine must redispatch unfinished tasks after a restart.
