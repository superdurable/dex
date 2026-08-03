import dagre from 'dagre';
import type { Edge, Node } from '@xyflow/react';

export function layoutGraph<NodeData extends Record<string, unknown>>(
  nodes: Array<Node<NodeData>>,
  edges: Edge[],
  direction: 'TB' | 'LR' = 'TB',
): Array<Node<NodeData>> {
  const graph = new dagre.graphlib.Graph();
  graph.setDefaultEdgeLabel(() => ({}));
  graph.setGraph({ rankdir: direction, ranksep: 80, nodesep: 44, marginx: 30, marginy: 30 });
  nodes.forEach((node) => graph.setNode(node.id, {
    width: typeof node.style?.width === 'number' ? node.style.width : 220,
    height: node.data.model && typeof node.data.model === 'object'
      && (node.data.model as { kind?: string }).kind === 'step' ? 158 : 90,
  }));
  edges.forEach((edge) => graph.setEdge(edge.source, edge.target));
  dagre.layout(graph);
  return nodes.map((node) => {
    const position = graph.node(node.id);
    const width = typeof node.style?.width === 'number' ? node.style.width : 220;
    const height = node.data.model && typeof node.data.model === 'object'
      && (node.data.model as { kind?: string }).kind === 'step' ? 158 : 90;
    return {
      ...node,
      position: { x: position.x - width / 2, y: position.y - height / 2 },
    };
  });
}
