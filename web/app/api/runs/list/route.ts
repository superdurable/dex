import { NextResponse } from 'next/server';
import { listRuns, type ListRunsRequestWire } from '../../_grpc/client';
import { mapRun } from '../../_grpc/mappers';

export const runtime = 'nodejs';
export const dynamic = 'force-dynamic';

interface ListBody {
  namespace?: string;
  // Empty string / undefined = "any flow type" (server skips filter).
  flowType?: string;
  // null / undefined = "any status" (server skips filter via proto3 field
  // presence). Set to a numeric RunStatus to filter.
  status?: number | null;
  orderBy?: number;
  limit?: number;
  pageToken?: string;
}

export async function POST(req: Request) {
  let body: ListBody;
  try {
    body = (await req.json()) as ListBody;
  } catch {
    return NextResponse.json({ error: 'invalid JSON body' }, { status: 400 });
  }

  if (!body.namespace) {
    return NextResponse.json({ error: 'namespace is required' }, { status: 400 });
  }

  // Build the wire request with proto3 field-presence semantics: omit the
  // `status` key entirely when the caller didn't pick one. proto-loader
  // serializes `optional int32 status` as "not present" in that case, and
  // OpsService treats that as "any status".
  const wire: ListRunsRequestWire = {
    namespace: body.namespace,
    flow_type: body.flowType ?? '',
    order_by: body.orderBy ?? 0,
    limit: body.limit ?? 50,
    page_token: body.pageToken ?? '',
  };
  if (typeof body.status === 'number') {
    wire.status = body.status;
  }

  try {
    const resp = await listRuns(wire);
    return NextResponse.json({
      runs: (resp.runs ?? []).map(mapRun),
      nextPageToken: resp.next_page_token ?? '',
    });
  } catch (err) {
    const message = err instanceof Error ? err.message : 'unknown error';
    return NextResponse.json({ error: `OpsService.ListRuns failed: ${message}` }, { status: 502 });
  }
}
