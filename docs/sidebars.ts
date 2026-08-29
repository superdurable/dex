import type {SidebarsConfig} from '@docusaurus/plugin-content-docs';

const sidebars: SidebarsConfig = {
  docs: [
    'index',
    {
      type: 'category',
      label: 'Intro and Quick Start',
      collapsed: false,
      items: ['intro/what-is-durable-execution', 'intro/what-is-dex', 'quick-start/index'],
    },
    {
      type: 'category',
      label: 'Primitives',
      link: {type: 'doc', id: 'primitives/index'},
      collapsed: false,
      items: [
        {
          type: 'category',
          label: 'Flow',
          link: {type: 'doc', id: 'primitives/flow/index'},
          items: ['primitives/flow/basic', 'primitives/flow/flow-options'],
        },
        {
          type: 'category',
          label: 'Step',
          link: {type: 'doc', id: 'primitives/step/index'},
          items: [
            'primitives/step/basic',
            'primitives/step/waiting-condition',
            'primitives/step/step-decision',
            'primitives/step/step-options',
          ],
        },
        'primitives/attribute',
        'primitives/rpc',
        'primitives/channel',
        'primitives/stream',
        'primitives/timer',
        'primitives/subflow',
        {
          type: 'category',
          label: 'Client',
          link: {type: 'doc', id: 'primitives/client/index'},
          items: ['primitives/client/basic', 'primitives/client/advanced'],
        },
      ],
    },
    {
      type: 'category',
      label: 'Design Patterns',
      link: {type: 'doc', id: 'design-patterns/index'},
      items: [
        'design-patterns/cron',
        {
          type: 'category',
          label: 'Drain Channels',
          items: [
            'design-patterns/drain-internal-channels',
            'design-patterns/draining-channel-for-external-publishing',
          ],
        },
        'design-patterns/interruptible',
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
        {
          type: 'category',
          label: 'Polling and Iteration',
          link: {type: 'doc', id: 'design-patterns/polling'},
          items: [
            'design-patterns/polling/backoff',
            'design-patterns/polling/timer',
            'design-patterns/polling/iteration',
          ],
        },
        'design-patterns/reminders',
        'design-patterns/resettable-timer',
        'design-patterns/entity-store',
        'design-patterns/wait-for-step-completion',
      ],
    },
    {
      type: 'category',
      label: 'Product Examples',
      link: {type: 'doc', id: 'use-cases/index'},
      items: [
        'use-cases/money-transfer',
        'use-cases/microservice-orchestration',
        'use-cases/engagement',
        'use-cases/subscription',
        'use-cases/polling',
        'use-cases/signup',
        'use-cases/job-post',
        'use-cases/shortlist-candidates',
        'use-cases/dataset-deal',
        'use-cases/ai-agent-email',
      ],
    },
    {
      type: 'category',
      label: 'Production',
      link: {type: 'doc', id: 'production/index'},
      items: [
        'production/application-operations',
        'production/server-operations',
        'production/metrics',
        'production/attribute-store',
        'production/versioning',
      ],
    },
    {
      type: 'category',
      label: 'References',
      link: {type: 'doc', id: 'references/index'},
      items: ['references/cli'],
    },
    'glossary',
  ],
};

export default sidebars;
