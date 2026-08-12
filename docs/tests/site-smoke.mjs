import assert from 'node:assert/strict';
import {readFile, access} from 'node:fs/promises';
import {fileURLToPath} from 'node:url';
import {dirname, join} from 'node:path';

const root = join(dirname(fileURLToPath(import.meta.url)), '..', 'build');
const home = await readFile(join(root, 'index.html'), 'utf8');
const cloud = await readFile(join(root, 'cloud', 'index.html'), 'utf8');

assert.match(home, /Super Durable home/);
assert.match(home, /https:\/\/superdurable\.io\/dex/);
assert.match(home, />Docs</);
assert.match(home, />Services</);
assert.match(home, /github-star-link/);
assert.match(home, /Toggle color theme/);
assert.match(home, />Book a call</);
assert.match(home, /product-bar/);
assert.match(home, />Dex OSS</);
assert.match(home, />Dex Cloud \/ BYOC</);

const navbar = home.match(/<header[\s\S]*?<\/header>/)?.[0] ?? '';
assert.doesNotMatch(navbar, />Team</);
assert.match(home, /footer-links[\s\S]*?>Team</);

const productMenu = home.match(/product-switcher-menu[\s\S]*?<\/div>/)?.[0] ?? '';
assert.doesNotMatch(productMenu, /Coming Soon/);
assert.match(cloud, /Dex Cloud \/ BYOC/);
assert.match(cloud, /Coming Soon/);
assert.match(cloud, /Explore Dex OSS Docs/);
assert.match(cloud, /https:\/\/superdurable\.io\/byoc/);

await Promise.all([
  access(join(root, 'intro', 'what-is-dex', 'index.html')),
  access(join(root, 'quick-start', 'index.html')),
  access(join(root, 'references', 'rpc', 'index.html')),
]);

console.log('Docs shell, product navigation, cloud page, and representative routes passed smoke checks.');
