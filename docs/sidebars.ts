import type {SidebarsConfig} from '@docusaurus/plugin-content-docs';

const sidebars: SidebarsConfig = {
  docs: [
    'index',
    {
      type: 'category',
      label: 'Intro and Quick Start',
      items: ['intro/what-is-durable-execution', 'intro/what-is-dex', 'quick-start/index'],
    },
    {
      type: 'category',
      label: 'Primitives',
      link: {type: 'doc', id: 'primitives/index'},
      items: [
        'primitives/flow/index',
        'primitives/step/index',
        'primitives/attribute',
        'primitives/rpc',
        'primitives/channel',
        'primitives/stream',
        'primitives/timer',
        'primitives/subflow',
        'primitives/client',
      ],
    },
    {
      type: 'category',
      label: 'Components',
      link: {type: 'doc', id: 'primitives/components/index'},
      items: [
        'primitives/components/application',
        'primitives/components/server',
        'primitives/components/cli',
        'primitives/components/web',
        'primitives/components/connector',
        'primitives/components/external-attribute-sync',
      ],
    },
    {
      type: 'category',
      label: 'Agent Frameworks',
      link: {type: 'doc', id: 'agent-frameworks'},
      items: ['agent-frameworks/deploy-agents'],
    },
    {
      type: 'category',
      label: 'Agent Skills',
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
      link: {type: 'doc', id: 'reference/index'},
      items: [
        {
          type: 'category',
          label: 'SDK Common',
          link: {type: 'doc', id: 'reference/sdk-common/index'},
          items: ['reference/sdk-common/flows-and-steps'],
        },
        {
          type: 'category',
          label: 'Server (server-zd)',
          items: [
            {type: 'link', label: 'Overview', href: '/reference/server/'},
            {type: 'link', label: 'Runtime Data', href: '/reference/server/runtime-data/'},
            {type: 'link', label: 'Runtime Client', href: '/reference/server/runtime-client/'},
            {type: 'link', label: 'Configuration', href: '/reference/server/configuration/'},
            {type: 'link', label: 'Kubernetes', href: '/reference/server/kubernetes/'},
            {type: 'link', label: 'High Availability', href: '/reference/server/high-availability/'},
            {type: 'link', label: 'Object Storage', href: '/reference/server/object-storage/'},
            {type: 'link', label: 'Authentication', href: '/reference/server/authentication/'},
            {type: 'link', label: 'Workers', href: '/reference/server/workers/'},
            {type: 'link', label: 'Search', href: '/reference/server/search/'},
            {type: 'link', label: 'Attribute Store Sync', href: '/reference/server/attribute-store-sync/'},
            {type: 'link', label: 'Metrics', href: '/reference/server/metrics/'},
            {type: 'link', label: 'Upgrades', href: '/reference/server/upgrades/'},
          ],
        },
        {
          type: 'category',
          label: 'CLI (dexcli)',
          items: [
            {type: 'link', label: 'Overview', href: '/reference/cli/'},
          ],
        },
        {
          type: 'category',
          label: 'Web (Dex Web)',
          items: [
            {type: 'link', label: 'Overview', href: '/reference/web/'},
            {type: 'link', label: 'Dex Web', href: '/reference/web/dex-web/'},
            {type: 'link', label: 'Flow Visualization', href: '/reference/web/flow-visualization/'},
            {type: 'link', label: 'Local Connector Setup', href: '/reference/web/local-connector-setup/'},
            {type: 'link', label: 'Debugging', href: '/reference/web/debugging/'},
          ],
        },
        {
          type: 'category',
          label: 'Go SDK',
          items: [
            {type: 'link', label: 'Overview', href: '/reference/go/'},
            {type: 'link', label: 'Workers', href: '/reference/go/workers/'},
          ],
        },
        {
          type: 'category',
          label: 'Python SDK',
          items: [
            {type: 'link', label: 'Overview', href: '/reference/python/'},
            {type: 'link', label: 'Workers', href: '/reference/python/workers/'},
          ],
        },
        {
          type: 'category',
          label: 'Java SDK',
          items: [
            {type: 'link', label: 'Overview', href: '/reference/java/'},
            {type: 'link', label: 'Workers', href: '/reference/java/workers/'},
          ],
        },
        {
          type: 'category',
          label: 'TypeScript SDK',
          items: [
            {type: 'link', label: 'Overview', href: '/reference/typescript/'},
            {type: 'link', label: 'Workers', href: '/reference/typescript/workers/'},
          ],
        },
        {
          type: 'category',
          label: 'Rust SDK',
          items: [
            {type: 'link', label: 'Overview', href: '/reference/rust/'},
            {type: 'link', label: 'Workers', href: '/reference/rust/workers/'},
          ],
        },
      ],
    },
    'glossary',
  ],
};

export default sidebars;
