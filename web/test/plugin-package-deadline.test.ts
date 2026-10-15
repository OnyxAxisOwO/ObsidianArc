// Boot waits for every package's browser half before the first paint. A
// package whose code is accepted and never answers must not hold the whole
// page on its spinner: it is left out, as one that failed would be.

import { afterEach, expect, it, vi } from 'vitest';
import { loadPlugins, plugins, setModuleImporter } from '../src/plugins/registry';
import example from './fixtures/example.plugin';

afterEach(async () => {
  vi.useRealTimers();
  setModuleImporter(null);
  await loadPlugins(undefined);
  vi.restoreAllMocks();
});

it('leaves out a package whose module never answers, and loads the rest', async () => {
  vi.useFakeTimers();
  const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
  setModuleImporter((url) => (url.includes('stuck')
    ? new Promise(() => {})
    : Promise.resolve({ default: () => example })));

  const loading = loadPlugins({ stuck: { _ui: '/stuck.js' }, example: { _ui: '/example.js' } });
  await vi.advanceTimersByTimeAsync(10_000);
  await loading;

  expect(plugins().map((plugin) => plugin.name)).toEqual(['example']);
  expect(warn).toHaveBeenCalledWith('plugin stuck could not be loaded', expect.any(Error));
});

it('leaves out a package whose factory never returns', async () => {
  vi.useFakeTimers();
  const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
  setModuleImporter((url) => (url.includes('stuck')
    ? Promise.resolve({ default: () => new Promise(() => {}) })
    : Promise.resolve({ default: () => example })));

  const loading = loadPlugins({ stuck: { _ui: '/stuck.js' }, example: { _ui: '/example.js' } });
  await vi.advanceTimersByTimeAsync(10_000);
  await loading;

  expect(plugins().map((plugin) => plugin.name)).toEqual(['example']);
  expect(warn).toHaveBeenCalledWith('plugin stuck could not be loaded', expect.any(Error));
});
