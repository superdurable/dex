import { NextResponse } from 'next/server';
import { getHistoryEvents } from '../../_grpc/client';
import { mapHistoryEvent } from '../../_grpc/mappers';

export const runtime = 'nodejs';
export const dynamic = 'force-dynamic';

const PAGE_LIMIT = 1000;
const MAX_PAGES = 50; // hard cap so a runaway client can't make us paginate forever

export async function GET(req: Request) {
  const url = new URL(req.url);
  const namespace = url.searchParams.get('namespace');
  const runId = url.searchParams.get('runId');
  if (!namespace || !runId) {
    return NextResponse.json({ error: 'namespace and runId are required' }, { status: 400 });
  }

  try {
    let afterId = 0;
    const all: ReturnType<typeof mapHistoryEvent>[] = [];
    for (let page = 0; page < MAX_PAGES; page++) {
      const resp = await getHistoryEvents({
        namespace,
        run_id: runId,
        after_id: afterId,
        limit: PAGE_LIMIT,
      });
      const events = (resp.events ?? []).map(mapHistoryEvent);
      all.push(...events);
      if (events.length < PAGE_LIMIT) break;
      afterId = events[events.length - 1].id;
    }
    return NextResponse.json({ events: all });
  } catch (err) {
    const message = err instanceof Error ? err.message : 'unknown error';
    return NextResponse.json(
      { error: `OpsService.GetHistoryEvents failed: ${message}` },
      { status: 502 },
    );
  }
}
