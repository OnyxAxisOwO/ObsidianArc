// A compiled-in plugin's browser half is awaited by boot the same way a
// package is, so a half that is accepted and never answered must not hold the
// first paint. The loader is passed in here because the build's own glob has
// no plugin to stall.

import { afterEach, describe, expect, it, vi } from 'vitest';
import type { ArcPlugin } from '../src/plugins/types';
import { loadPluginModule } from '../src/plugins/registry';

afterEach(() => {
  vi.useRealTimers();
});

describe('loadPluginModule deadline', () => {
  it('leaves the plugin out once its half has not answered by the deadline', async () => {
    vi.useFakeTimers();
    const stalled = loadPluginModule('demo', () => new Promise<{ default: ArcPlugin }>(() => {}));

    await vi.advanceTimersByTimeAsync(60_000);

    await expect(stalled).resolves.toBeNull();
  });

  it('keeps a half that answers in time', async () => {
    const half = { name: 'demo' } as ArcPlugin;

    await expect(loadPluginModule('demo', async () => ({ default: half }))).resolves.toBe(half);
  });
});
