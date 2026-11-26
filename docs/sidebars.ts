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
  {component: 'dex-app', label: 'Dex app'},
];

const sidebars: SidebarsConfig = {
  docs: [
    'index',
    {
      type: 'category',
      label: 'Quick Start',
      customProps: {section: 'intro'},
      // The header opens the Quick Start, which owns the install. The home page is the docs hub.
      // Build with AI is the coding-agent path; the full skill pages are in Developer Reference > Dex Skills.
      link: {type: 'doc', id: 'quick-start/index'},
      items: [
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
      label: 'Run Dex',
      customProps: {section: 'components'},
      link: {type: 'doc', id: 'components/index'},
      items: [
        'components/dex-cloud',
        'components/managed-dex-app',
        'components/self-host',
        'components/production',
        'components/operate-and-debug',
        'concepts/change-flow-code',
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
      // The overview page lists these groups in the same order.
      link: {type: 'doc', id: 'design-patterns/index'},
      items: [
        'design-patterns/workflow-dsl',
        {
          type: 'category',
          label: 'Parallel Steps',
          link: {type: 'doc', id: 'design-patterns/parallel'},
          items: [
            'design-patterns/parallel/static',
            'design-patterns/parallel/dynamic',
            'design-patterns/parallel/await',
            'design-patterns/parallel/first-win',
            'design-patterns/parallel/critical-section',
          ],
        },
        {
          type: 'category',
          label: 'SubFlows',
          link: {type: 'doc', id: 'design-patterns/subflows'},
          items: [
            'design-patterns/subflows/basic',
            'design-patterns/subflows/wait-for-half',
            'design-patterns/subflows/long-lived-parent',
            'design-patterns/subflows/short-lived-parent',
            'design-patterns/subflows/partitioning',
            'design-patterns/subflows/back-pressure',
            'design-patterns/subflows/cancel-case',
            'design-patterns/subflows/child-flows-with-business-identity',
          ],
        },
        {
          type: 'category',
          label: 'Durable Timer',
          link: {type: 'doc', id: 'design-patterns/durable-timer/index'},
          items: [
            'design-patterns/durable-timer/cron',
            'design-patterns/durable-timer/reminder',
            'design-patterns/durable-timer/polling-with-wait-for-timer',
            'design-patterns/durable-timer/polling-with-long-running-step-heartbeat',
          ],
        },
        {
          type: 'category',
          label: 'External Interaction',
          link: {type: 'doc', id: 'design-patterns/external-interaction'},
          items: [
            'design-patterns/external-interaction/decision-with-deadline',
            'design-patterns/external-interaction/idle-timer',
            'design-patterns/external-interaction/idle-timer-with-resolution',
            'design-patterns/external-interaction/control-long-running-work',
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
          label: 'Data Storage',
          link: {type: 'doc', id: 'design-patterns/data-storage'},
          items: [
            'design-patterns/data-storage/entity-flow',
            'design-patterns/data-storage/entity-flow-attribute-sync',
            'design-patterns/data-storage/sequentially-chunked-attribute-map',
            'design-patterns/data-storage/hash-partitioned-attribute-map',
          ],
        },
        {
          type: 'category',
          label: 'Failure Handling',
          link: {type: 'doc', id: 'design-patterns/failure-handling'},
          items: [
            'design-patterns/failure-handling/execute-failure-recovery',
            'design-patterns/failure-handling/fail-fast-on-permanent-errors',
            'design-patterns/failure-handling/manual-recovery',
            'design-patterns/failure-handling/graceful-timeout',
            'design-patterns/failure-handling/wait-for-failure-recovery',
          ],
        },
      ],
    },
    {
      type: 'category',
      label: 'Examples',
      customProps: {section: 'product-examples'},
      link: {type: 'doc', id: 'use-cases/index'},
      items: [
        'use-cases/signup-review-agent',
        'use-cases/release-watch-agent',
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
      label: 'Evaluate Dex',
      customProps: {section: 'correctness'},
      link: {type: 'doc', id: 'intro/evaluate'},
      items: [
        {
          type: 'category',
          label: 'Dex vs. Temporal',
          link: {type: 'doc', id: 'intro/compared-to-temporal'},
          items: [
            'intro/compared-to-temporal-performance',
            'intro/compared-to-temporal-operations',
            'intro/compared-to-temporal-features',
            'intro/compared-to-temporal-shared-features',
            'intro/compared-to-temporal-programming-model',
          ],
        },
        'intro/performance',
        'intro/limits',
        {
          type: 'category',
          label: 'Correctness and Expressiveness',
          link: {type: 'doc', id: 'correctness-and-expressiveness/index'},
          items: [
            'concepts/step/advanced-guarantees',
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
          label: 'Dex Skills in Depth',
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
