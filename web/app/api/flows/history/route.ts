import { NextResponse } from 'next/server';
import { getHistoryEvents } from '../../_grpc/client';
import { grpcErrorResponse } from '../../_grpc/http';
import { mapHistoryEvent } from '../../_grpc/mappers';

export const runtime = 'nodejs';
export const dynamic = 'force-dynamic';

export async function GET(request: Request) {
  const url = new URL(request.url);
  const flowId = url.searchParams.get('flowId');
  const runId = url.searchParams.get('runId');
  if (!flowId || !runId) {
    return NextResponse.json({ error: 'flowId and runId are required' }, { status: 400 });
  }
  const startInternalEventId = Number(url.searchParams.get('startInternalEventId') || 0);
  const estimatePageSize = Number(url.searchParams.get('estimatePageSize') || 100);
  if (startInternalEventId < 0 || estimatePageSize < 0) {
    return NextResponse.json({ error: 'history cursors must be non-negative' }, { status: 400 });
  }
  try {
    const response = await getHistoryEvents({
      flow_id: flowId,
      run_id: runId,
      start_internal_event_id: startInternalEventId,
      estimate_page_size: estimatePageSize,
      next_page_token: Buffer.from(url.searchParams.get('nextPageToken') || '', 'base64'),
    });
    const token = Buffer.isBuffer(response.next_page_token)
      ? response.next_page_token.toString('base64')
      : '';
    return NextResponse.json({
      events: Array.isArray(response.events) ? response.events.map(mapHistoryEvent) : [],
      nextPageToken: token,
      nextInternalEventId: Number(response.next_internal_event_id) || 0,
    });
  } catch (error) {
    return grpcErrorResponse(error, 'GetHistoryEvents');
  }
}
