import { runStatusName } from './utils';

const COLORS: Record<number, string> = {
  0: 'bg-gray-100 text-gray-700 ring-gray-300',
  1: 'bg-yellow-100 text-yellow-800 ring-yellow-300',
  2: 'bg-blue-100 text-blue-800 ring-blue-300',
  3: 'bg-purple-100 text-purple-800 ring-purple-300',
  4: 'bg-green-100 text-green-800 ring-green-300',
  5: 'bg-red-100 text-red-800 ring-red-300',
};

export default function StatusBadge({ status }: { status: number }) {
  const cls = COLORS[status] ?? 'bg-gray-100 text-gray-700 ring-gray-300';
  return (
    <span
      data-testid="status-badge"
      data-status={status}
      className={`inline-flex items-center px-2 py-0.5 rounded-full text-xs font-medium ring-1 ring-inset ${cls}`}
    >
      {runStatusName(status)}
    </span>
  );
}
