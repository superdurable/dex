import type {SidebarsConfig} from '@docusaurus/plugin-content-docs';

const sidebars: SidebarsConfig = {
  reference: [
    {type: 'link', label: '← Developer Reference', href: '/reference/'},
    'index',
    'install',
    'event-stream',
    'workers',
    'session-limits',
    'approvals',
    'connector-tools',
    'failures',
    'spend-limits',
    'upgrades',
    'credentials',
    'dex-web',
    'changelog',
  ],
};

export default sidebars;
