import assert from 'node:assert/strict';
import { existsSync, readFileSync, readdirSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import test from 'node:test';

const docs = fileURLToPath(new URL('../', import.meta.url));
const dist = path.join(docs, '.vitepress/dist');
const origin = 'https://docs.invalid';
const siteBase = `/${(process.env.DOCS_BASE || '/').replace(/^\/+|\/+$/g, '')}`.replace('//', '/');
const base = siteBase === '/' ? '/' : `${siteBase}/`;

function filesIn(directory) {
  return readdirSync(directory, { withFileTypes: true }).flatMap(entry => {
    const file = path.join(directory, entry.name);
    return entry.isDirectory() ? filesIn(file) : [file];
  });
}

function decodeEntities(value) {
  return value.replace(/&(?:amp|quot|apos|lt|gt|#\d+|#x[\da-f]+);/gi, entity => {
    const named = { '&amp;': '&', '&quot;': '"', '&apos;': "'", '&lt;': '<', '&gt;': '>' };
    if (named[entity]) return named[entity];
    if (entity.startsWith('&#x')) return String.fromCodePoint(parseInt(entity.slice(3, -1), 16));
    return String.fromCodePoint(Number(entity.slice(2, -1)));
  });
}

assert.ok(existsSync(dist), 'Build the documentation first: npm --prefix docs run build');
const pages = new Map(filesIn(dist).filter(file => file.endsWith('.html')).map(file => [
  path.relative(dist, file).split(path.sep).join('/'), readFileSync(file, 'utf8'),
]));

test('every public Markdown page is rendered with a main heading', () => {
  const sources = ['index.md', ...['guide', 'features', 'admin', 'api', 'architecture']
    .flatMap(folder => filesIn(path.join(docs, folder)))];
  for (const source of sources) {
    const relative = path.isAbsolute(source) ? path.relative(docs, source) : source;
    if (!relative.endsWith('.md')) continue;
    const target = relative.split(path.sep).join('/').replace(/\.md$/, '.html');
    const html = pages.get(target);
    assert.ok(html, `Missing page: ${target}`);
    assert.equal([...html.matchAll(/<h1\b/g)].length, 1, `${target}: expected one main heading`);
    assert.match(html, /<main\b/, `${target}: missing main landmark`);
    assert.match(html, /<html[^>]+lang="zh-CN"/, `${target}: missing page language`);
  }
});

// Frontmatter links in the custom homepage bypass VitePress's Markdown link check.
for (const [name, html] of pages) {
  test(`${name}: internal links, anchors and images resolve`, () => {
    const route = name === 'index.html' ? base : `${base}${name.replace(/\.html$/, '')}`;
    for (const match of html.matchAll(/<(a|img)\b[^>]*?\b(?:href|src)="([^"]+)"/g)) {
      const href = decodeEntities(match[2]);
      const url = new URL(href, `${origin}${route}`);
      if (url.origin !== origin) continue;
      assert.ok(url.pathname.startsWith(base), `${name}: target escapes configured base ${href}`);
      let target = decodeURIComponent(url.pathname.slice(base.length));
      if (!target || target.endsWith('/')) target += 'index.html';
      else if (!path.posix.extname(target)) target += '.html';
      assert.ok(existsSync(path.join(dist, target)), `${name}: missing target ${href}`);
      if (!url.hash || !pages.has(target)) continue;
      const id = decodeURIComponent(url.hash.slice(1));
      const ids = [...pages.get(target).matchAll(/\bid="([^"]+)"/g)].map(item => decodeEntities(item[1]));
      assert.ok(ids.includes(id), `${name}: missing anchor ${href}`);
    }
  });
}
