import { NextResponse } from 'next/server';
import { forkRun } from '../../_grpc/client';

export const runtime = 'nodejs';
export const dynamic = 'force-dynamic';

// POST /api/runs/fork — restore a run to a past history event (ForkRun).
export async function POST(req: Request) {
  let body: Record<string, unknown>;
  try {
    body = (await req.json()) as Record<string, unknown>;
  } catch {
    return NextResponse.json({ error: 'invalid JSON body' }, { status: 400 });
  }

  const namespace = typeof body.namespace === 'string' ? body.namespace : '';
  const runId = typeof body.runId === 'string' ? body.runId : '';
  const toEventId = body.toEventId;
  const reason = typeof body.reason === 'string' ? body.reason : '';

  if (!namespace || !runId) {
    return NextResponse.json({ error: 'namespace and runId are required' }, { status: 400 });
  }
  if (typeof toEventId !== 'number' || !Number.isInteger(toEventId) || toEventId <= 0) {
    return NextResponse.json({ error: 'toEventId must be a positive integer' }, { status: 400 });
  }

  try {
    await forkRun({
      namespace,
      run_id: runId,
      to_event_id: toEventId,
      reason,
    });
    return NextResponse.json({ ok: true });
  } catch (err) {
    const message = err instanceof Error ? err.message : 'unknown error';
    return NextResponse.json(
      { error: `RunsService.ForkRun failed: ${message}` },
      { status: 502 },
    );
  }
}
