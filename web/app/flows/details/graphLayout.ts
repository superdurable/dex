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
  nodes.forEach((node) => graph.setNode(node.id, { width: 220, height: 90 }));
  edges.forEach((edge) => graph.setEdge(edge.source, edge.target));
  dagre.layout(graph);
  return nodes.map((node) => {
    const position = graph.node(node.id);
    return {
      ...node,
      position: { x: position.x - 110, y: position.y - 45 },
    };
  });
}
