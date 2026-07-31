import { RunDetailsPage } from '../../RunDetailsPage';

export default async function FlowRunPage({
  params,
}: {
  params: Promise<{ flowId: string; runId: string }>;
}) {
  const { flowId, runId } = await params;
  return <RunDetailsPage flowId={flowId} runId={runId} />;
}
