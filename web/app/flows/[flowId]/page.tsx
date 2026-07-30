import { CurrentRunRedirect } from '../CurrentRunRedirect';

export default async function CurrentFlowPage({
  params,
}: {
  params: Promise<{ flowId: string }>;
}) {
  const { flowId } = await params;
  return <CurrentRunRedirect flowId={flowId} />;
}
