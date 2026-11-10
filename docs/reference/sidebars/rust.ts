import type {SidebarsConfig} from '@docusaurus/plugin-content-docs';

const sidebars: SidebarsConfig = {
  reference: [
    {type: 'link', label: '← Developer Reference', href: '/reference/'},
    'index',
    'flow',
    'start-flow-options',
    {
      type: 'category',
      label: 'Step',
      link: {type: 'doc', id: 'step'},
      items: ['step-options', 'step-decisions', 'waits'],
    },
    'failure-and-retries',
    'workers',
    'changelog',
  ],
};

export default sidebars;
