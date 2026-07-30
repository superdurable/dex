import { NextResponse } from 'next/server';
import { waitForHistoryEvent } from '../../_grpc/client';
import { grpcErrorResponse } from '../../_grpc/http';
import { jsonValue } from '../../_grpc/mappers';

export const runtime = 'nodejs';
export const dynamic = 'force-dynamic';
export const maxDuration = 70;

export async function GET(request: Request) {
  const url = new URL(request.url);
  const flowId = url.searchParams.get('flowId');
  const runId = url.searchParams.get('runId');
  const nextInternalEventId = Number(url.searchParams.get('nextInternalEventId') || 0);
  if (!flowId || !runId || nextInternalEventId <= 0) {
    return NextResponse.json(
      { error: 'flowId, runId, and positive nextInternalEventId are required' },
      { status: 400 },
    );
  }
  try {
    return NextResponse.json(jsonValue(await waitForHistoryEvent({
      flow_id: flowId,
      run_id: runId,
      next_internal_event_id: nextInternalEventId,
    }, request.signal)));
  } catch (error) {
    return grpcErrorResponse(error, 'WaitForHistoryEvent');
  }
}
