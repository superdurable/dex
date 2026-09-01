# Flow Definition renderer

This private package owns the Flow Definition Graph v1 TypeScript contract,
compound layout, React renderer, and renderer styles. Dex Web and the product
documentation import the same package so checked-in examples cannot drift from
the interactive Flow Rendering page.

Consumers must provide React 19, React Flow, and Dagre. The repository uses a
local file dependency and preserves symlinks in Vite, Docusaurus, and
TypeScript so each consumer resolves its own locked dependency versions.

```tsx
import {
  FlowDefinitionGraphView,
  type FlowDefinitionGraph,
} from '@superdurable/flow-definition-renderer';

<FlowDefinitionGraphView displayName={graph.flow.name} graph={graph} />
```
