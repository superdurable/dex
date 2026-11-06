import type {SidebarsConfig} from '@docusaurus/plugin-content-docs';

const sidebars: SidebarsConfig = {
  reference: [
    {type: 'link', label: '← Developer Reference', href: '/reference/'},
    'index',
    'runtime-data',
    'configuration',
    'kubernetes',
    'high-availability',
    'object-storage',
    'authentication',
    'workers',
    'search',
    'attribute-store-sync',
    'metrics',
    'upgrades',
  ],
};

export default sidebars;
