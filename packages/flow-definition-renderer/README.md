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

The renderer fits the full definition into the viewport by default. Channel,
Attribute, and Stream relations remain hidden until their resource or a related
Step, WaitFor, Execute decision, RPC, or timeout handler is selected. The RPC
legend control affects only RPC nodes; Flow timeout handlers always remain
visible as part of the Flow.
