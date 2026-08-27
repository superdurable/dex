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
        'design-patterns/drain-internal-channels',
        'design-patterns/draining-channel-for-external-publishing',
        'design-patterns/interruptible',
        'design-patterns/intervention',
        'design-patterns/parallel',
        'design-patterns/parent-child',
        'design-patterns/polling',
        'design-patterns/recovery',
        'design-patterns/reminders',
        'design-patterns/resettable-timer',
        'design-patterns/scalable-parallel',
        'design-patterns/entity-store',
        'design-patterns/timeout',
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
