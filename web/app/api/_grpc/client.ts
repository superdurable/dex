// Server-only module: imports grpc-js (Node-native). Do NOT import from a
// client component or it will fail to bundle.
import path from 'node:path';
import fs from 'node:fs';
import * as grpc from '@grpc/grpc-js';
import * as protoLoader from '@grpc/proto-loader';

// We use grpc-js + dynamic proto loading so the WebUI does not require any
// codegen step. The proto file lives in `protocol-grpc/protos/dex.proto`
// in the repo. The path is resolvable two ways:
//   1. DEX_PROTO_PATH env var (absolute path) — wins when set.
//   2. Walk up from process.cwd() (which is `web/` under `next dev`) looking
//      for a `protocol-grpc/protos/dex.proto`. This works for both
//      `npm run dev` and the Playwright runner.
function resolveProtoPath(): string {
  const explicit = process.env.DEX_PROTO_PATH;
  if (explicit && fs.existsSync(explicit)) {
    return explicit;
  }
  let dir = process.cwd();
  for (let i = 0; i < 6; i++) {
    const candidate = path.join(dir, 'protocol-grpc', 'protos', 'dex.proto');
    if (fs.existsSync(candidate)) return candidate;
    const parent = path.dirname(dir);
    if (parent === dir) break;
    dir = parent;
  }
  throw new Error(
    `Cannot locate dex.proto. Set DEX_PROTO_PATH or run from the dex repo (cwd=${process.cwd()}).`,
  );
}

export interface RunSummaryWire {
  run_id: string;
  namespace: string;
  flow_type: string;
  task_list_name: string;
  status: number;
  start_time_ms: string;
  updated_at_ms: string;
}

export interface ListRunsRequestWire {
  namespace: string;
  flow_type: string;
  // proto3 `optional int32 status` — omit the key entirely to mean "any
  // status" (server skips the filter). proto-loader respects field
  // presence when the property is not set on the JS object.
  status?: number;
  order_by: number;
  limit: number;
  page_token: string;
}

export interface ListRunsResponseWire {
  runs: RunSummaryWire[];
  next_page_token: string;
}

export interface GetHistoryEventsRequestWire {
  namespace: string;
  run_id: string;
  after_id: string | number;
  limit: number;
}

export interface HistoryEventWire {
  id: string;
  occurred_at_ms: string;
  worker_id: string;
  // Exactly one of these is set (the proto oneof is decoded by proto-loader as
  // the underlying field name being non-null).
  run_start?: unknown;
  run_stop?: unknown;
  step_execute_completed?: unknown;
  step_wait_for_completed?: unknown;
  channel_publish?: unknown;
  steps_unblocked?: unknown;
  run_fork?: unknown;
}

export interface GetHistoryEventsResponseWire {
  events: HistoryEventWire[];
}

// --- RunsService.GetRun wire types ---
//
// Mirrors dex.proto messages GetRunRequest / GetRunResponse. We surface
// only the fields the show page consumes (live status + state +
// unconsumed_channel_messages + a few counters in the footer); other fields
// from GetRunResponse (active_step_executions, step_execution_id_counters,
// version, …) are typed as unknown / specific scalars only when needed so
// adding them later doesn't require widening this interface.
//
// All long fields come back as strings due to the protoLoader `longs: String`
// setting; mappers convert them to JS numbers (safe for ms timestamps and
// the small counters used here).

export interface GetRunRequestWire {
  namespace: string;
  run_id: string;
  status_filter?: number[];
}

// dex.ChannelMessages: `{ values: [Value, ...] }`. Each Value is the
// same proto-loader oneof shape that flattenValue() in mappers.ts handles.
export interface ChannelMessagesWire {
  values?: unknown[];
}

// pb.ActiveStepExecution as it arrives from proto-loader. Used to surface
// in-flight steps (e.g. a long sleep inside Execute) on the live graph
// view, since those write NO history event between dispatch and Execute
// completion.
export interface ActiveStepExecutionWire {
  // pb.StepExecutionStatus enum: 0=INVOKING_WAIT_FOR, 1=WAITING_FOR_CONDITION,
  // 2=INVOKING_EXECUTE. Drives the live "Running" badge on the graph.
  status?: number;
  // Server-authoritative provenance: which step spawned this execution via
  // NextSteps. Empty for starting steps. Used to draw the incoming edge
  // for an in-flight node that has no StepExecuteCompleted yet.
  from_step_exe_id?: string;
  // Live wait-condition tree + most recent condition_results echo. Reused
  // by the live-state panel and the graph's WaitFor section.
  wait_for_condition?: unknown;
  condition_results?: unknown[];
  wait_for_retry_state?: StepRetryStateWire;
  execute_retry_state?: StepRetryStateWire;
}

export interface StepRetryStateWire {
  first_attempt_time_ms?: string | number;
  current_attempts?: number;
  last_error?: string;
  last_error_stack_trace?: string;
}

export interface StepOptionsSnapshotWire {
  wait_for_method_timeout_ms?: string | number;
  execute_method_timeout_ms?: string | number;
  wait_for_method_retry_policy?: StepRetryPolicySnapshotWire;
  execute_method_retry_policy?: StepRetryPolicySnapshotWire;
}

export interface StepRetryPolicySnapshotWire {
  max_attempts?: number;
  initial_interval_ms?: string | number;
  backoff_coefficient?: number;
  maximum_interval_ms?: string | number;
  total_timeout_ms?: string | number;
}

export interface GetRunResponseWire {
  found: boolean;
  run_id: string;
  namespace: string;
  flow_type: string;
  task_list_name: string;
  status: number;
  state?: Record<string, unknown>;
  unconsumed_channel_messages?: Record<string, ChannelMessagesWire>;
  // Live snapshot of every step the engine considers active for this run
  // (anything not retired into history). Surfaced so the WebUI can render
  // INVOKING_EXECUTE steps (e.g. a long sleep) as Running nodes BEFORE
  // their StepExecuteCompleted history event lands — see buildGraph.ts.
  active_step_executions?: Record<string, ActiveStepExecutionWire>;
  worker_request_counter?: string | number;
  version?: string | number;
  server_timestamp_ms?: string | number;
  durable_timer_fired?: boolean;
  external_channel_message_counter?: string | number;
}

interface OpsServiceClient extends grpc.Client {
  ListRuns(
    req: ListRunsRequestWire,
    cb: (err: grpc.ServiceError | null, resp: ListRunsResponseWire) => void,
  ): void;
  GetHistoryEvents(
    req: GetHistoryEventsRequestWire,
    cb: (err: grpc.ServiceError | null, resp: GetHistoryEventsResponseWire) => void,
  ): void;
}

export interface ForkRunRequestWire {
  namespace: string;
  run_id: string;
  to_event_id: string | number;
  reason?: string;
}

export interface ForkRunResponseWire {}

interface RunsServiceClient extends grpc.Client {
  GetRun(
    req: GetRunRequestWire,
    cb: (err: grpc.ServiceError | null, resp: GetRunResponseWire) => void,
  ): void;
  ForkRun(
    req: ForkRunRequestWire,
    cb: (err: grpc.ServiceError | null, resp: ForkRunResponseWire) => void,
  ): void;
}

// loadDEXPackage parses dex.proto once per process. Returned
// `dex` package is reused by every service-client builder so we don't
// re-parse the proto file per service. Cached at module scope.
let cachedPackage: {
  OpsService: new (addr: string, creds: grpc.ChannelCredentials) => OpsServiceClient;
  RunsService: new (addr: string, creds: grpc.ChannelCredentials) => RunsServiceClient;
} | null = null;

function loadDEXPackage() {
  if (cachedPackage) return cachedPackage;
  const protoPath = resolveProtoPath();
  const repoRoot = path.dirname(path.dirname(path.dirname(protoPath)));
  const def = protoLoader.loadSync(protoPath, {
    keepCase: true,
    longs: String,
    enums: Number,
    defaults: true,
    oneofs: true,
    includeDirs: [path.dirname(protoPath), repoRoot],
  });
  const pkg = grpc.loadPackageDefinition(def) as unknown as {
    dex: typeof cachedPackage extends infer T ? (T extends null ? never : T) : never;
  };
  cachedPackage = pkg.dex as unknown as NonNullable<typeof cachedPackage>;
  return cachedPackage;
}

let cachedOpsClient: OpsServiceClient | null = null;
let cachedRunsClient: RunsServiceClient | null = null;

export function opsClient(): OpsServiceClient {
  if (!cachedOpsClient) {
    const address = process.env.OPS_SERVICE_ADDRESS || '127.0.0.1:7235';
    cachedOpsClient = new (loadDEXPackage().OpsService)(address, grpc.credentials.createInsecure());
  }
  return cachedOpsClient;
}

// runsClient targets the run-service gRPC address (default 127.0.0.1:7233).
// Separate from opsClient because RunsService and OpsService are typically
// served on different ports and may even run on different processes / pods
// — see docs/ops-service-design.md and the DEX_GRPC_LISTEN_ADDRESS /
// DEX_OPS_GRPC_LISTEN_ADDRESS env vars in the server config.
export function runsClient(): RunsServiceClient {
  if (!cachedRunsClient) {
    const address = process.env.RUN_SERVICE_ADDRESS || '127.0.0.1:7233';
    cachedRunsClient = new (loadDEXPackage().RunsService)(address, grpc.credentials.createInsecure());
  }
  return cachedRunsClient;
}

export function listRuns(req: ListRunsRequestWire): Promise<ListRunsResponseWire> {
  return new Promise((resolve, reject) => {
    opsClient().ListRuns(req, (err, resp) => {
      if (err) return reject(err);
      resolve(resp);
    });
  });
}

export function getHistoryEvents(req: GetHistoryEventsRequestWire): Promise<GetHistoryEventsResponseWire> {
  return new Promise((resolve, reject) => {
    opsClient().GetHistoryEvents(req, (err, resp) => {
      if (err) return reject(err);
      resolve(resp);
    });
  });
}

export function getRun(req: GetRunRequestWire): Promise<GetRunResponseWire> {
  return new Promise((resolve, reject) => {
    runsClient().GetRun(req, (err, resp) => {
      if (err) return reject(err);
      resolve(resp);
    });
  });
}

export function forkRun(req: ForkRunRequestWire): Promise<ForkRunResponseWire> {
  return new Promise((resolve, reject) => {
    runsClient().ForkRun(req, (err, resp) => {
      if (err) return reject(err);
      resolve(resp);
    });
  });
}
