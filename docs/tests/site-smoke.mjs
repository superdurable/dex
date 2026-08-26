import assert from 'node:assert/strict';
import {readFile, access} from 'node:fs/promises';
import {fileURLToPath} from 'node:url';
import {dirname, join} from 'node:path';

const root = join(dirname(fileURLToPath(import.meta.url)), '..', 'build');
const home = await readFile(join(root, 'index.html'), 'utf8');
const cloud = await readFile(join(root, 'cloud', 'index.html'), 'utf8');
const production = await readFile(join(root, 'production', 'index.html'), 'utf8');
const sitemap = await readFile(join(root, 'sitemap.xml'), 'utf8');

assert.match(home, /Super Durable home/);
assert.match(home, /https:\/\/superdurable\.io\/dex/);
assert.match(home, />Docs</);
assert.match(home, />Dex BYOC</);
assert.match(home, /github-star-link/);
assert.match(home, /Toggle color theme/);
assert.match(home, />Book a call\s*</);
assert.match(home, />Dex OSS</);
assert.match(home, />Dex Cloud \/ BYOC</);

const navbar = home.match(/<nav aria-label="Main"[\s\S]*?<\/nav><div role="presentation"/)?.[0] ?? '';
assert.doesNotMatch(navbar, />Team</);
assert.match(home, /footer-links[\s\S]*?>Team</);

assert.doesNotMatch(home, /product-bar/);
const docsMenu = home.match(/<nav class="desktop-nav"[\s\S]*?<\/nav>/)?.[0] ?? '';
assert.match(docsMenu, />Docs</);
assert.match(docsMenu, />Dex OSS</);
assert.match(docsMenu, />Dex Cloud \/ BYOC</);
assert.doesNotMatch(docsMenu, /Coming Soon/);
assert.doesNotMatch(docsMenu, /iconExternalLink/);
assert.match(docsMenu, />Dex BYOC</);
assert.match(docsMenu, /https:\/\/superdurable\.io\/byoc/);
assert.doesNotMatch(docsMenu, />Services</);
assert.doesNotMatch(docsMenu, /consulting/i);
assert.match(docsMenu, /Open-source Dex guides, concepts, and references/);
assert.match(home, /English/);
assert.match(home, /中文/);

const zhHome = await readFile(join(root, 'zh-Hans', 'index.html'), 'utf8');
assert.match(zhHome, /English/);
assert.match(zhHome, /中文/);

assert.match(cloud, /Dex Cloud \/ BYOC/);
assert.match(cloud, /Coming Soon/);
assert.match(cloud, /Explore Dex OSS Docs/);
assert.match(cloud, /https:\/\/superdurable\.io\/byoc/);
assert.match(production, /rel="canonical" href="https:\/\/docs\.superdurable\.io\/production\/"/);
assert.match(sitemap, /<loc>https:\/\/docs\.superdurable\.io\/production\/<\/loc>/);
assert.doesNotMatch(sitemap, /<loc>https:\/\/docs\.superdurable\.io\/production<\/loc>/);

await Promise.all([
  access(join(root, 'intro', 'what-is-durable-execution', 'index.html')),
  access(join(root, 'intro', 'what-is-dex', 'index.html')),
  access(join(root, 'quick-start', 'index.html')),
  access(join(root, 'primitives', 'index.html')),
  access(join(root, 'primitives', 'step', 'index.html')),
  access(join(root, 'references', 'cli', 'index.html')),
  access(join(root, 'zh-Hans', 'intro', 'what-is-durable-execution', 'index.html')),
  access(join(root, 'zh-Hans', 'intro', 'what-is-dex', 'index.html')),
  access(join(root, 'zh-Hans', 'quick-start', 'index.html')),
]);

console.log('Docs shell, product navigation, cloud page, and representative routes passed smoke checks.');
