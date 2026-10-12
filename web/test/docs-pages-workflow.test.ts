// The docs Pages workflow's token scopes. The build job runs every npm
// lifecycle script the docs dependencies declare, so it must not hold a token
// that can publish the site, and its checkout must not leave that token in
// .git/config. The file is read as text: the repository has no YAML parser,
// and the workflow's shape is simple enough to hold to by its lines.

import { readFileSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

const workflow = readFileSync(path.resolve(__dirname, '../../.github/workflows/docs-pages.yml'), 'utf8');

function section(from: RegExp, to?: RegExp): string {
  const start = workflow.search(from);
  if (start < 0) throw new Error(`Missing section: ${from}`);
  const rest = workflow.slice(start);
  if (!to) return rest;
  const end = rest.slice(1).search(to);
  return end < 0 ? rest : rest.slice(0, end + 1);
}

const topLevel = section(/^name:/m, /^jobs:/m);
const build = section(/^ {2}build:/m, /^ {2}deploy:/m);
const deploy = section(/^ {2}deploy:/m);

describe('docs pages workflow permissions', () => {
  it('grants the workflow read access to contents and nothing else', () => {
    expect(topLevel).toMatch(/^permissions:\n {2}contents: read\n/m);
    expect(topLevel).not.toMatch(/pages: write/);
    expect(topLevel).not.toMatch(/id-token: write/);
  });

  it('keeps the Pages and OIDC write scopes out of the build job', () => {
    expect(build).not.toMatch(/pages: write/);
    expect(build).not.toMatch(/id-token: write/);
  });

  it('grants the Pages and OIDC write scopes to the deploy job only', () => {
    expect(deploy).toMatch(/permissions:\n {6}contents: read\n {6}pages: write\n {6}id-token: write\n/);
  });

  it('does not persist the checkout token into the build job git config', () => {
    expect(build).toMatch(/uses: actions\/checkout@v4\n {8}with:\n {10}persist-credentials: false\n/);
  });
});
