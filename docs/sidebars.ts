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
      label: 'Intro and Quick Start',
      customProps: {section: 'intro'},
      items: ['intro/what-is-dex', 'quick-start/index', 'intro/compared-to-database'],
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
        {
          type: 'category',
          label: 'Correctness and Expressiveness',
          link: {type: 'doc', id: 'concepts/correctness-and-expressiveness/index'},
          items: [
            'concepts/correctness-and-expressiveness/expressiveness-proof',
            'concepts/correctness-and-expressiveness/workflow-patterns',
          ],
        },
      ],
    },
    {
      type: 'category',
      label: 'Components',
      customProps: {section: 'components'},
      link: {type: 'doc', id: 'components/index'},
      items: [
        'components/application',
        'components/server',
        'components/cli',
        'components/web',
        'components/connector',
        'components/external-attribute-sync',
      ],
    },
    {
      type: 'category',
      label: 'Agent Frameworks',
      customProps: {section: 'agent-frameworks'},
      link: {type: 'doc', id: 'agent-frameworks'},
      items: [
        'agent-frameworks/built-in-agent-loop',
        'agent-frameworks/claude-agent-sdk',
        'agent-frameworks/openai-agents-sdk',
        'agent-frameworks/pydantic-ai',
        'agent-frameworks/langchain-langgraph',
        'agent-frameworks/google-adk',
        'agent-frameworks/vercel-ai-sdk',
        'agent-frameworks/mastra',
      ],
    },
    {
      type: 'category',
      label: 'Agent Skills',
      customProps: {section: 'agent-skills'},
      collapsed: false,
      items: [
        'build-with-ai/dex-developer-skill',
        'build-with-ai/dex-developer-skill/dex-app-builder',
        'build-with-ai/dex-developer-skill/dex-sdk',
        'build-with-ai/dex-developer-skill/dex-connector-contributor',
      ],
    },
    {
      type: 'category',
      label: 'Design Patterns',
      customProps: {section: 'design-patterns'},
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
        'design-patterns/deferred-choice',
        'design-patterns/critical-section',
        'design-patterns/cancel-case',
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
      label: 'Product Examples',
      customProps: {section: 'product-examples'},
      link: {type: 'doc', id: 'use-cases/index'},
      items: [
        'use-cases/ai-agent',
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
      label: 'Developer Reference',
      customProps: {section: 'reference'},
      link: {type: 'doc', id: 'reference/index'},
      // Each entry expands into that component's own versioned sidebar. See plugins/shared-docs-sidebar.
      items: referenceComponents.map(({component, label}) => ({
        type: 'link' as const,
        label,
        href: `/reference/${component}/`,
        customProps: {referenceComponent: component},
      })),
    },
    {type: 'doc', id: 'glossary', customProps: {section: 'glossary'}},
  ],
};

export default sidebars;
