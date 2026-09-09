import assert from 'node:assert/strict';
import {readFile, access} from 'node:fs/promises';
import {fileURLToPath} from 'node:url';
import {dirname, join} from 'node:path';

const root = join(dirname(fileURLToPath(import.meta.url)), '..', 'build');
const home = await readFile(join(root, 'index.html'), 'utf8');
const cloud = await readFile(join(root, 'cloud', 'index.html'), 'utf8');
const cron = await readFile(join(root, 'design-patterns', 'durable-timer', 'cron', 'index.html'), 'utf8');
const whyDex = await readFile(join(root, 'intro', 'what-is-dex', 'index.html'), 'utf8');
const production = await readFile(join(root, 'production', 'index.html'), 'utf8');
const dexDeveloperSkill = await readFile(join(root, 'build-with-ai', 'dex-developer-skill', 'index.html'), 'utf8');
const zhCron = await readFile(join(root, 'zh-Hans', 'design-patterns', 'durable-timer', 'cron', 'index.html'), 'utf8');
const zhDexDeveloperSkill = await readFile(join(root, 'zh-Hans', 'build-with-ai', 'dex-developer-skill', 'index.html'), 'utf8');
const zhWhyDex = await readFile(join(root, 'zh-Hans', 'intro', 'what-is-dex', 'index.html'), 'utf8');
const zhSubflow = await readFile(join(root, 'zh-Hans', 'primitives', 'subflow', 'index.html'), 'utf8');
const sitemap = await readFile(join(root, 'sitemap.xml'), 'utf8');

assert.match(home, /Super Durable home/);
assert.match(home, /https:\/\/superdurable\.io\/dex/);
assert.match(home, /https:\/\/github\.com\/superdurable/);
assert.match(home, /Toggle color theme/);
assert.match(home, />Book a call\s*</);
assert.doesNotMatch(home, /github-star-link|github-star-stat/);

const navbar = home.match(/<nav aria-label="Main"[\s\S]*?<\/nav><div role="presentation"/)?.[0] ?? '';
assert.doesNotMatch(navbar, />Team</);
assert.match(home, /footer-links[\s\S]*?>Team</);

assert.doesNotMatch(home, /product-bar/);
const desktopNav = home.match(/<nav class="desktop-nav"[\s\S]*?<\/nav>/)?.[0] ?? '';
assert.match(desktopNav, />Dex</);
assert.match(desktopNav, />GitHub/);
assert.match(desktopNav, /https:\/\/github\.com\/superdurable/);
assert.doesNotMatch(desktopNav, /Docs|BYOC|Star Us|github-star-stat/);

const footer = home.match(/<footer class="site-footer"[\s\S]*?<\/footer>/)?.[0] ?? '';
assert.match(footer, />Dex</);
assert.match(footer, />Team</);
assert.match(footer, /Process-centric apps, powered by Dex/);
assert.doesNotMatch(footer, />Docs<|BYOC|Star Us on GitHub/);
assert.match(home, /English/);
assert.match(home, /中文/);
assert.match(home, /Build with AI/);
assert.match(home, /Dex Developer Skill/);

const primitivesPosition = home.indexOf('>Primitives<');
const buildWithAIPosition = home.indexOf('>Build with AI<');
const designPatternsPosition = home.indexOf('>Design Patterns<');
assert.ok(primitivesPosition >= 0);
assert.ok(buildWithAIPosition > primitivesPosition);
assert.ok(designPatternsPosition > buildWithAIPosition);

const zhHome = await readFile(join(root, 'zh-Hans', 'index.html'), 'utf8');
assert.match(zhHome, /English/);
assert.match(zhHome, /中文/);

assert.match(cloud, /Dex Cloud \/ BYOC/);
assert.match(cloud, /Coming Soon/);
assert.match(cloud, /Explore Dex OSS Docs/);
assert.match(cloud, /https:\/\/superdurable\.io\/byoc/);
assert.match(cron, /flow-definition-canvas/);
assert.match(cron, /CronScheduleFlow/);
assert.match(zhCron, /flow-definition-canvas/);
assert.match(zhCron, /CronScheduleFlow/);
assert.match(whyDex, /flow-definition-canvas/);
assert.match(whyDex, /OrderProcessingFlow/);
assert.match(zhWhyDex, /flow-definition-canvas/);
assert.match(zhWhyDex, /OrderProcessingFlow/);
assert.match(production, /rel="canonical" href="https:\/\/docs\.superdurable\.io\/production\/"/);
assert.match(dexDeveloperSkill, /Build Dex applications with AI/);
assert.match(dexDeveloperSkill, /superdurable\/skill-dex-developer/);
assert.match(zhDexDeveloperSkill, /使用 AI 构建 Dex 应用/);
assert.match(zhSubflow, /rel="canonical" href="https:\/\/docs\.superdurable\.io\/zh-Hans\/primitives\/subflow\/"/);
assert.match(sitemap, /<loc>https:\/\/docs\.superdurable\.io\/production\/<\/loc>/);
assert.doesNotMatch(sitemap, /<loc>https:\/\/docs\.superdurable\.io\/production<\/loc>/);

await Promise.all([
  access(join(root, 'intro', 'what-is-durable-execution', 'index.html')),
  access(join(root, 'intro', 'what-is-dex', 'index.html')),
  access(join(root, 'quick-start', 'index.html')),
  access(join(root, 'primitives', 'index.html')),
  access(join(root, 'primitives', 'step', 'index.html')),
  access(join(root, 'references', 'cli', 'index.html')),
  access(join(root, 'build-with-ai', 'dex-developer-skill', 'index.html')),
  access(join(root, 'zh-Hans', 'intro', 'what-is-durable-execution', 'index.html')),
  access(join(root, 'zh-Hans', 'intro', 'what-is-dex', 'index.html')),
  access(join(root, 'zh-Hans', 'quick-start', 'index.html')),
  access(join(root, 'zh-Hans', 'build-with-ai', 'dex-developer-skill', 'index.html')),
]);

console.log('Docs shell, simplified product navigation, cloud page, and representative routes passed smoke checks.');
