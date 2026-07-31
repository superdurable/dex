import type { FlowStatus } from '@/lib/types';

export function StatusBadge({ status }: { status: FlowStatus | string }) {
  const tone = status.toLowerCase().replaceAll(' ', '-');
  return <span className={`status-badge status-${tone}`}>{status || 'Unknown'}</span>;
}
