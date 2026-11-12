import type {SidebarsConfig} from '@docusaurus/plugin-content-docs';

const referenceComponents = [
  {component: 'server', label: 'Server (server-zd)'},
  {component: 'cli', label: 'CLI (dexcli)'},
  {component: 'web', label: 'Web (Dex Web)'},
  {component: 'go', label: 'Go SDK'},
  {component: 'python', label: 'Python SDK'},
  {component: 'java', label: 'Java SDK'},
  {component: 'typescript', label: 'TypeScript SDK'},
  {component: 'rust', label: 'Rust SDK'},
  {component: 'agents', label: 'Agents packages'},
];

const sidebars: SidebarsConfig = {
  docs: [
    'index',
    {
      type: 'category',
      label: 'Get Started',
      customProps: {section: 'intro'},
      items: [
        {type: 'doc', id: 'intro/get-started', label: 'Overview'},
        'quick-start/index',
        'intro/add-to-existing-code',
      ],
    },
    {
      type: 'category',
      label: 'Build with AI',
      customProps: {section: 'agent-skills'},
      // Dex Connector Contributor is linked from the overview, not listed here.
      items: [
        {type: 'doc', id: 'build-with-ai/dex-developer-skill', label: 'Overview'},
        'build-with-ai/dex-developer-skill/dex-app-builder',
        'build-with-ai/dex-developer-skill/dex-sdk',
      ],
    },
    {
      type: 'category',
      label: 'Durable Agents',
      customProps: {section: 'agent-frameworks'},
      // Each framework is a tab on these pages. The built-in loop comes last because most agents use a framework.
      items: [
        {type: 'doc', id: 'agent-frameworks', label: 'Overview'},
        'agent-frameworks/quick-start',
        'agent-frameworks/add-to-your-agent',
        'agent-frameworks/sessions',
        'agent-frameworks/tool-calls',
        'agent-frameworks/approvals',
        'agent-frameworks/connectors-as-tools',
        'agent-frameworks/production',
        'agent-frameworks/built-in-agent-loop',
      ],
    },
    {
      type: 'category',
      label: 'Concepts',
      customProps: {section: 'concepts'},
      items: [
        {type: 'doc', id: 'concepts/index', label: 'Overview'},
        'concepts/flow/index',
        'concepts/step/index',
        {
          type: 'category',
          label: 'Primitives',
          items: [
            {type: 'doc', id: 'concepts/primitives/index', label: 'Overview'},
            'concepts/primitives/attribute',
            'concepts/primitives/rpc',
            'concepts/primitives/channel',
            'concepts/primitives/stream',
            'concepts/primitives/timer',
            'concepts/primitives/subflow',
          ],
        },
        'concepts/client',
        'concepts/connector',
        'concepts/failover-and-deploys',
        'concepts/change-flow-code',
      ],
    },
    {
      type: 'category',
      label: 'Design Patterns',
      customProps: {section: 'design-patterns'},
      // The overview page groups these into Flow control, Reliability, Messaging, and Data.
      items: [
        {type: 'doc', id: 'design-patterns/index', label: 'Overview'},
        {
          type: 'category',
          label: 'Parallel Steps',
          items: [
            {type: 'doc', id: 'design-patterns/parallel', label: 'Overview'},
            'design-patterns/parallel/static',
            'design-patterns/parallel/dynamic',
            'design-patterns/parallel/await',
            'design-patterns/parallel/first-win',
          ],
        },
        {
          type: 'category',
          label: 'Parallel SubFlows',
          items: [
            {type: 'doc', id: 'design-patterns/parallel-subflows', label: 'Overview'},
            'design-patterns/parallel-subflows/basic',
            'design-patterns/parallel-subflows/wait-for-half',
            'design-patterns/parallel-subflows/long-lived-parent',
            'design-patterns/parallel-subflows/short-lived-parent',
            'design-patterns/parallel-subflows/partitioning',
            'design-patterns/parallel-subflows/back-pressure',
          ],
        },
        'design-patterns/interruptible',
        {
          type: 'category',
          label: 'Durable Timer',
          items: [
            {type: 'doc', id: 'design-patterns/durable-timer/index', label: 'Overview'},
            'design-patterns/durable-timer/cron',
            'design-patterns/durable-timer/reminder',
            'design-patterns/durable-timer/inactiveness-tracker-timer',
          ],
        },
        'design-patterns/deferred-choice',
        'design-patterns/critical-section',
        'design-patterns/cancel-case',
        {
          type: 'category',
          label: 'Failure Handling',
          items: [
            {type: 'doc', id: 'design-patterns/failure-handling', label: 'Overview'},
            'design-patterns/failure-handling/execute-failure-recovery',
            'design-patterns/failure-handling/manual-recovery',
            'design-patterns/failure-handling/graceful-timeout',
            'design-patterns/failure-handling/wait-for-failure-recovery',
          ],
        },
        'design-patterns/polling',
        {
          type: 'category',
          label: 'Drain Channels',
          items: [
            {type: 'doc', id: 'design-patterns/drain-channels', label: 'Overview'},
            'design-patterns/drain-internal-channels',
            'design-patterns/draining-channel-for-external-publishing',
          ],
        },
        {
          type: 'category',
          label: 'Responsive Update',
          items: [
            {type: 'doc', id: 'design-patterns/responsive-update', label: 'Overview'},
            'design-patterns/responsive-update/step-completion',
            'design-patterns/responsive-update/attribute-match',
            'design-patterns/responsive-update/stream',
          ],
        },
        'design-patterns/inbound-event-loop',
        {
          type: 'category',
          label: 'AttributeMap Partitioning',
          items: [
            {type: 'doc', id: 'design-patterns/attribute-map-partitioning', label: 'Overview'},
            'design-patterns/sequentially-chunked-attribute-map',
            'design-patterns/hash-partitioned-attribute-map',
          ],
        },
        'design-patterns/entity-store',
      ],
    },
    {
      type: 'category',
      label: 'Examples',
      customProps: {section: 'product-examples'},
      items: [
        {type: 'doc', id: 'use-cases/index', label: 'Overview'},
        'use-cases/ai-agent',
        'use-cases/order-support-agent',
        'use-cases/order-processing',
        'use-cases/subscription',
        'use-cases/user-onboarding-process',
        'use-cases/money-transfer',
        'use-cases/job-seeker-engagement',
        'use-cases/deal-dsl',
        'use-cases/microservice-orchestration',
        'use-cases/job-post',
        'use-cases/customer-refund',
      ],
    },
    {
      type: 'category',
      label: 'Run Dex',
      customProps: {section: 'components'},
      items: [
        {type: 'doc', id: 'components/index', label: 'Overview'},
        'components/run-locally',
        'components/application',
        'components/testing',
        'components/your-database',
        'components/server',
        'components/production',
        'components/cli',
        'components/web',
      ],
    },
    {
      type: 'category',
      label: 'Evaluate Dex',
      customProps: {section: 'correctness'},
      items: [
        {type: 'doc', id: 'intro/what-is-dex', label: 'Overview'},
        'intro/compared-to-database',
        'intro/compared-to-durable-execution',
        'intro/performance',
        'intro/limits',
        {
          type: 'category',
          label: 'Correctness and Expressiveness',
          items: [
            {type: 'doc', id: 'correctness-and-expressiveness/index', label: 'Overview'},
            'correctness-and-expressiveness/expressiveness-proof',
            'correctness-and-expressiveness/failure-transparency-proof',
            'correctness-and-expressiveness/workflow-patterns',
          ],
        },
      ],
    },
    {
      type: 'category',
      label: 'Developer Reference',
      customProps: {section: 'reference'},
            // Each entry expands into that component's own versioned sidebar. See plugins/shared-docs-sidebar.
      items: [
        {type: 'doc', id: 'reference/index', label: 'Overview'},
        ...referenceComponents.map(({component, label}) => ({
          type: 'link' as const,
          label,
          href: `/reference/${component}/`,
          customProps: {referenceComponent: component},
        })),
      ],
    },
    {type: 'doc', id: 'glossary', customProps: {section: 'glossary'}},
  ],
};

export default sidebars;
