import type {SidebarsConfig} from '@docusaurus/plugin-content-docs';

// Topic pages follow the Concepts sidebar, so a reader finds each concept under the same name.
const sidebars: SidebarsConfig = {
  reference: [
    {type: 'link', label: '← Developer Reference', href: '/reference/'},
    'flow',
    'start-flow-options',
    {
      type: 'category',
      label: 'Step methods',
      link: {type: 'doc', id: 'step'},
      items: ['step-options', 'step-decisions', 'waits'],
    },
    {
      type: 'category',
      label: 'Primitives',
      link: {type: 'doc', id: 'primitives'},
      items: ['attribute', 'rpc', 'channel', 'stream', 'timer', 'subflow'],
    },
    'client',
    'failure-and-retries',
    'workers',
    'changelog',
  ],
};

export default sidebars;
