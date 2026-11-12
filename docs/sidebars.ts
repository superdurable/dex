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
      link: {type: 'doc', id: 'intro/get-started'},
      // Build with AI is the coding-agent path; the full skill pages are in Developer Reference > Dex Skills.
      items: [
        'quick-start/index',
        'intro/add-to-existing-code',
        'components/testing',
        'build-with-ai/dex-developer-skill',
      ],
    },
    {
      type: 'category',
      label: 'Durable Agents',
      customProps: {section: 'agent-frameworks'},
      // The deep pages are in Developer Reference > Agents in Depth, linked from How Durable Agents Work.
      link: {type: 'doc', id: 'agent-frameworks'},
      items: [
        'agent-frameworks/quick-start',
        'agent-frameworks/add-to-your-agent',
        'agent-frameworks/framework-support',
        'agent-frameworks/how-it-works',
        'agent-frameworks/production',
      ],
    },
    {
      type: 'category',
      label: 'Concepts',
      customProps: {section: 'concepts'},
      link: {type: 'doc', id: 'concepts/index'},
      items: [
        'concepts/flow/index',
        'concepts/step/index',
        {
          type: 'category',
          label: 'Primitives',
          link: {type: 'doc', id: 'concepts/primitives/index'},
          items: [
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
        'components/your-database',
      ],
    },
    {
      type: 'category',
      label: 'Design Patterns',
      customProps: {section: 'design-patterns'},
      // The overview page groups these into Flow control, Reliability, Messaging, and Data.
      link: {type: 'doc', id: 'design-patterns/index'},
      items: [
        {
          type: 'category',
          label: 'Parallel Steps',
          link: {type: 'doc', id: 'design-patterns/parallel'},
          items: [
            'design-patterns/parallel/static',
            'design-patterns/parallel/dynamic',
            'design-patterns/parallel/await',
            'design-patterns/parallel/first-win',
          ],
        },
        {
          type: 'category',
          label: 'Parallel SubFlows',
          link: {type: 'doc', id: 'design-patterns/parallel-subflows'},
          items: [
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
          link: {type: 'doc', id: 'design-patterns/durable-timer/index'},
          items: [
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
          link: {type: 'doc', id: 'design-patterns/failure-handling'},
          items: [
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
          link: {type: 'doc', id: 'design-patterns/drain-channels'},
          items: [
            'design-patterns/drain-internal-channels',
            'design-patterns/draining-channel-for-external-publishing',
          ],
        },
        {
          type: 'category',
          label: 'Responsive Update',
          link: {type: 'doc', id: 'design-patterns/responsive-update'},
          items: [
            'design-patterns/responsive-update/step-completion',
            'design-patterns/responsive-update/attribute-match',
            'design-patterns/responsive-update/stream',
          ],
        },
        'design-patterns/inbound-event-loop',
        {
          type: 'category',
          label: 'AttributeMap Partitioning',
          link: {type: 'doc', id: 'design-patterns/attribute-map-partitioning'},
          items: [
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
      link: {type: 'doc', id: 'use-cases/index'},
      items: [
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
      link: {type: 'doc', id: 'components/index'},
      items: [
        'components/dex-cloud',
        'components/self-host',
        'components/production',
        'components/operate-and-debug',
        'concepts/change-flow-code',
      ],
    },
    {
      type: 'category',
      label: 'Evaluate Dex',
      customProps: {section: 'correctness'},
      link: {type: 'doc', id: 'intro/evaluate'},
      items: [
        'intro/what-is-dex',
        'intro/compared-to-database',
        'intro/compared-to-durable-execution',
        'intro/performance',
        'intro/limits',
        {
          type: 'category',
          label: 'Correctness and Expressiveness',
          link: {type: 'doc', id: 'correctness-and-expressiveness/index'},
          items: [
            'correctness-and-expressiveness/step-guarantees',
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
      link: {type: 'doc', id: 'reference/index'},
      items: [
        ...referenceComponents.map(({component, label}) => ({
          type: 'link' as const,
          label,
          href: `/reference/${component}/`,
          customProps: {referenceComponent: component},
        })),
        {
          type: 'category',
          label: 'Running Dex in Depth',
          link: {type: 'doc', id: 'reference/running-dex-in-depth'},
          items: [
            'components/run-locally',
            'components/application',
            'components/server',
            'components/cli',
            'components/web',
            'concepts/failover-and-deploys',
          ],
        },
        {
          type: 'category',
          label: 'Agents in Depth',
          link: {type: 'doc', id: 'reference/agents-in-depth'},
          items: [
            'agent-frameworks/sessions',
            'agent-frameworks/tool-calls',
            'agent-frameworks/approvals',
            'agent-frameworks/connectors-as-tools',
            'agent-frameworks/built-in-agent-loop',
            'agent-frameworks/framework-details',
          ],
        },
        {
          type: 'category',
          label: 'Dex Skills',
          link: {type: 'doc', id: 'reference/dex-skills'},
          items: [
            'build-with-ai/dex-developer-skill/dex-app-builder',
            'build-with-ai/dex-developer-skill/dex-sdk',
            'build-with-ai/dex-developer-skill/dex-connector-contributor',
          ],
        },
      ],
    },
    {type: 'doc', id: 'glossary', customProps: {section: 'glossary'}},
  ],
};

export default sidebars;
