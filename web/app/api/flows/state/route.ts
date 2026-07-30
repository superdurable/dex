import { NextResponse } from 'next/server';
import { getFlowState } from '../../_grpc/client';
import { grpcErrorResponse } from '../../_grpc/http';
import { mapFlowState } from '../../_grpc/mappers';

export const runtime = 'nodejs';
export const dynamic = 'force-dynamic';

export async function GET(request: Request) {
  const url = new URL(request.url);
  const flowId = url.searchParams.get('flowId');
  const runId = url.searchParams.get('runId');
  if (!flowId || !runId) {
    return NextResponse.json({ error: 'flowId and runId are required' }, { status: 400 });
  }
  try {
    return NextResponse.json(mapFlowState(await getFlowState({
      flow_id: flowId,
      run_id: runId,
    })));
  } catch (error) {
    return grpcErrorResponse(error, 'GetFlowState');
  }
}
