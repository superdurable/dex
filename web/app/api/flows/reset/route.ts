import { NextResponse } from 'next/server';
import { resetFlow } from '../../_grpc/client';
import { grpcErrorResponse } from '../../_grpc/http';

export const runtime = 'nodejs';
export const dynamic = 'force-dynamic';

export async function POST(request: Request) {
  let body: {
    flowId?: string;
    runId?: string;
    resetType?: number;
    historyEventId?: number;
    reason?: string;
    stepType?: string;
    stepExecutionId?: string;
    historyEventTime?: string;
  };
  try {
    body = await request.json() as typeof body;
  } catch {
    return NextResponse.json({ error: 'Invalid JSON body' }, { status: 400 });
  }
  if (!body.flowId || !body.runId || !body.resetType || !body.reason?.trim()) {
    return NextResponse.json(
      { error: 'flowId, runId, resetType, and reason are required' },
      { status: 400 },
    );
  }
  try {
    const response = await resetFlow({
      flow_id: body.flowId,
      run_id: body.runId,
      reset_type: body.resetType,
      history_event_id: body.historyEventId ?? 0,
      reason: body.reason,
      step_type: body.stepType ?? '',
      step_execution_id: body.stepExecutionId ?? '',
      history_event_time: body.historyEventTime ?? '',
    });
    return NextResponse.json({ runId: response.run_id ?? '' });
  } catch (error) {
    return grpcErrorResponse(error, 'ResetFlow');
  }
}
