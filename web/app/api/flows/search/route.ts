import { NextResponse } from 'next/server';
import { grpcErrorResponse } from '../../_grpc/http';
import { searchFlows } from '../../_grpc/client';
import { mapSearchEntry } from '../../_grpc/mappers';

export const runtime = 'nodejs';
export const dynamic = 'force-dynamic';

export async function POST(request: Request) {
  let body: { query?: string; pageSize?: number; nextPageToken?: string };
  try {
    body = await request.json() as typeof body;
  } catch {
    return NextResponse.json({ error: 'Invalid JSON body' }, { status: 400 });
  }
  if ((body.pageSize ?? 50) < 0) {
    return NextResponse.json({ error: 'pageSize must be non-negative' }, { status: 400 });
  }
  try {
    const response = await searchFlows({
      query: body.query ?? '',
      page_size: body.pageSize ?? 50,
      next_page_token: body.nextPageToken ?? '',
    });
    return NextResponse.json({
      flows: Array.isArray(response.flow_runs)
        ? response.flow_runs.map(mapSearchEntry)
        : [],
      nextPageToken: typeof response.next_page_token === 'string'
        ? response.next_page_token
        : '',
    });
  } catch (error) {
    return grpcErrorResponse(error, 'SearchFlows');
  }
}
