import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';

// The SDK ships a copy of the declaration types so a plugin author has
// completion without this repository's source. Two copies of one contract
// drift, so this holds them together: every declaration here is in the SDK's
// file, word for word. What is allowed to differ is what the SDK cannot
// import — the host a package is handed, and the icon type.

const strip = (source: string): string => source
  .replace(/\/\*[\s\S]*?\*\//g, '')
  .replace(/\/\/.*$/gm, '')
  .replace(/\s+/g, ' ');

const server = strip(readFileSync(resolve(__dirname, '../src/plugins/types.ts'), 'utf8'));
const sdk = strip(readFileSync(resolve(__dirname, '../../sdk/web/arc-plugin.d.ts'), 'utf8'));

const DIFFERS = new Set(['PluginHost', 'PluginFactory']);

describe('the SDK\'s copy of the plugin declaration types', () => {
  const declarations = server.split(/ (?=export (?:interface|type) )/)
    .map((d) => d.trim())
    .filter((d) => d.startsWith('export '));

  it('finds the declarations to hold together', () => {
    expect(declarations.length).toBeGreaterThan(10);
  });

  for (const declaration of declarations) {
    const name = /^export (?:interface|type) (\w+)/.exec(declaration)![1]!;
    if (DIFFERS.has(name)) continue;
    it(`declares ${name} exactly as the server does`, () => {
      expect(sdk).toContain(declaration);
    });
  }

  it('gives a package the same host the page lends', () => {
    // The names, at least: the SDK's shapes are looser because it cannot
    // import the client and the icon set.
    const hostOf = (source: string): string => {
      const from = source.indexOf('export interface PluginHost');
      return source.slice(from, source.indexOf('export type PluginFactory'));
    };
    for (const member of ['api:', 'ApiError:', 'icons:', 'strings', 'format:', 'language()']) {
      expect(hostOf(server)).toContain(member);
      expect(hostOf(sdk)).toContain(member);
    }
  });
});
