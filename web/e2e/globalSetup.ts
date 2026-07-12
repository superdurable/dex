import path from 'node:path';
import net from 'node:net';
import { execa } from 'execa';
import { writeState, type E2eState } from './state';
import { listRuns } from '../app/api/_grpc/client';

// Base compose + the selected backend override (postgres default, or mongo) —
// only the chosen DB is started. See web/docker-compose.e2e.yaml.
const DB_BACKEND = process.env.DEX_DB_BACKEND || 'postgres';
const COMPOSE_ARGS = [
  '-f',
  path.resolve(__dirname, '..', 'docker-compose.e2e.yaml'),
  '-f',
  path.resolve(__dirname, '..', `docker-compose.${DB_BACKEND}.yaml`),
];
const COMPOSE_PROJECT = process.env.DEX_E2E_PROJECT || 'dex-web-e2e';
const NAMESPACE = process.env.E2E_NAMESPACE || 'default';
// FlowType the benchmark worker registers in `mode=sequential` runs. The SDK
// derives it from the Go struct via reflection — see
// `[benchmark/cmd/benchmarkworker/flows.go](../../benchmark/cmd/benchmarkworker/flows.go)`
// and `GetFinalFlowType` in `[sdk-go/dex/flow.go](../../sdk-go/dex/flow.go)`.
const FLOW_TYPE = process.env.E2E_FLOW_TYPE || 'main.sequentialBenchmarkFlow';
const OPS_PORT = Number(process.env.OPS_PORT || 7235);
const BENCH_PORT = Number(process.env.BENCH_PORT || 9123);
const OPS_ADDRESS = `127.0.0.1:${OPS_PORT}`;
const SKIP_STACK = process.env.E2E_SKIP_STACK === '1';

async function waitForTcp(host: string, port: number, timeoutMs: number): Promise<void> {
  const start = Date.now();
  let lastErr: Error | null = null;
  while (Date.now() - start < timeoutMs) {
    try {
      await new Promise<void>((resolve, reject) => {
        const sock = net.createConnection({ host, port });
        sock.once('connect', () => {
          sock.end();
          resolve();
        });
        sock.once('error', (err) => {
          sock.destroy();
          reject(err);
        });
        setTimeout(() => {
          sock.destroy();
          reject(new Error('connect timeout'));
        }, 2000);
      });
      return;
    } catch (err) {
      lastErr = err as Error;
      await sleep(1000);
    }
  }
  throw new Error(`timed out waiting for ${host}:${port} (${lastErr?.message ?? 'unknown'})`);
}

async function waitForHttp(url: string, timeoutMs: number): Promise<void> {
  const start = Date.now();
  let lastErr: Error | null = null;
  while (Date.now() - start < timeoutMs) {
    try {
      const resp = await fetch(url);
      if (resp.ok) return;
      lastErr = new Error(`HTTP ${resp.status}`);
    } catch (err) {
      lastErr = err as Error;
    }
    await sleep(1000);
  }
  throw new Error(`timed out waiting for ${url} (${lastErr?.message ?? 'unknown'})`);
}

function sleep(ms: number): Promise<void> {
  return new Promise((r) => setTimeout(r, ms));
}

async function triggerRuns(runs: number, numSteps: number): Promise<string[]> {
  const url = `http://127.0.0.1:${BENCH_PORT}/trigger?mode=sequential&runs=${runs}&numSteps=${numSteps}&stateSize=64`;
  const resp = await fetch(url);
  if (!resp.ok) {
    throw new Error(`benchmark trigger failed: HTTP ${resp.status} ${await resp.text()}`);
  }
  const body = (await resp.json()) as { run_ids?: string[] };
  return body.run_ids ?? [];
}

async function pollForCompleted(
  namespace: string,
  flowType: string,
  runIds: string[],
  timeoutMs: number,
): Promise<string | null> {
  const start = Date.now();
  const wanted = new Set(runIds);
  while (Date.now() - start < timeoutMs) {
    try {
      const resp = await listRuns({
        namespace,
        flow_type: flowType,
        status: 4, // RunStatusCompleted
        order_by: 0,
        limit: 100,
        page_token: '',
      });
      for (const r of resp.runs ?? []) {
        if (wanted.has(r.run_id)) return r.run_id;
      }
    } catch (_err) {
      // backend might still be warming up
    }
    await sleep(1500);
  }
  return null;
}

export default async function globalSetup(): Promise<void> {
  process.env.OPS_SERVICE_ADDRESS = OPS_ADDRESS;

  if (!SKIP_STACK) {
    console.log(`[globalSetup] backend=${DB_BACKEND} docker compose ${COMPOSE_ARGS.join(' ')} -p ${COMPOSE_PROJECT} up -d --build`);
    await execa('docker', ['compose', ...COMPOSE_ARGS, '-p', COMPOSE_PROJECT, 'up', '-d', '--build'], {
      stdio: 'inherit',
      timeout: 5 * 60_000,
    });
  } else {
    console.log('[globalSetup] E2E_SKIP_STACK=1, assuming stack is already running');
  }

  console.log(`[globalSetup] waiting for OpsService on ${OPS_ADDRESS}...`);
  await waitForTcp('127.0.0.1', OPS_PORT, 90_000);

  console.log(`[globalSetup] waiting for benchmark /healthz on :${BENCH_PORT}...`);
  await waitForHttp(`http://127.0.0.1:${BENCH_PORT}/healthz`, 60_000);

  console.log('[globalSetup] triggering benchmark runs...');
  const runIds = await triggerRuns(5, 3);
  console.log(`[globalSetup] triggered ${runIds.length} runs`);

  console.log('[globalSetup] polling for at least one Completed run...');
  const completedRunId = await pollForCompleted(NAMESPACE, FLOW_TYPE, runIds, 90_000);
  if (completedRunId) {
    console.log(`[globalSetup] completed run id = ${completedRunId}`);
  } else {
    console.warn(`[globalSetup] no run reached Completed within timeout; show-spec will fall back to runIds[0]`);
  }

  const state: E2eState = {
    composeArgs: COMPOSE_ARGS,
    composeProject: COMPOSE_PROJECT,
    runIds,
    completedRunId: completedRunId ?? runIds[0] ?? null,
    namespace: NAMESPACE,
    flowType: FLOW_TYPE,
    opsAddress: OPS_ADDRESS,
    benchPort: BENCH_PORT,
    startedAt: new Date().toISOString(),
  };
  writeState(state);
  console.log('[globalSetup] state persisted to e2e/.state.json');
}
