import { NextResponse } from 'next/server';
import { getRun } from '../../_grpc/client';
import { mapGetRun } from '../../_grpc/mappers';

export const runtime = 'nodejs';
export const dynamic = 'force-dynamic';

// GET /api/runs/get?namespace=...&runId=...
//
// Calls RunsService.GetRun for the live, authoritative view of a run:
// status + state + unconsumed_channel_messages + counters. Polled by the
// show page (every 2s while status is non-terminal) so the badge / state
// panel / pending-channels section stay in sync with what the server holds.
//
// Polling-friendly:
//   - GET (cacheable to "no-store" via dynamic = 'force-dynamic')
//   - tiny request payload (just two query params)
//   - one gRPC round trip per call (no pagination loop)
export async function GET(req: Request) {
  const url = new URL(req.url);
  const namespace = url.searchParams.get('namespace');
  const runId = url.searchParams.get('runId');
  if (!namespace || !runId) {
    return NextResponse.json({ error: 'namespace and runId are required' }, { status: 400 });
  }

  try {
    const resp = await getRun({ namespace, run_id: runId });
    if (!resp.found) {
      return NextResponse.json({ error: `run ${runId} not found in ${namespace}` }, { status: 404 });
    }
    return NextResponse.json(mapGetRun(resp));
  } catch (err) {
    const message = err instanceof Error ? err.message : 'unknown error';
    return NextResponse.json(
      { error: `RunsService.GetRun failed: ${message}` },
      { status: 502 },
    );
  }
}
