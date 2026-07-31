import { NextResponse } from 'next/server';
import { getFlowSummary } from '../../_grpc/client';
import { grpcErrorResponse } from '../../_grpc/http';
import { mapSummary } from '../../_grpc/mappers';

export const runtime = 'nodejs';
export const dynamic = 'force-dynamic';

export async function GET(request: Request) {
  const url = new URL(request.url);
  const flowId = url.searchParams.get('flowId');
  if (!flowId) return NextResponse.json({ error: 'flowId is required' }, { status: 400 });
  try {
    return NextResponse.json(mapSummary(await getFlowSummary({
      flow_id: flowId,
      run_id: url.searchParams.get('runId') ?? '',
    })));
  } catch (error) {
    return grpcErrorResponse(error, 'GetFlowSummary');
  }
}
