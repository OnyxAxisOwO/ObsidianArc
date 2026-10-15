// The plugin screens read "not found" only once the plugins have answered. A
// /api/site that cannot be read is an answer too: no plugin is coming, so the
// screen must stop waiting for one rather than keep its loading title for ever.

import { afterEach, expect, it, vi } from 'vitest';
import * as authApi from '../src/api/auth';
import { plugins, pluginsSettled } from '../src/plugins/registry';
import { startSession } from '../src/stores/session';

afterEach(() => {
  vi.restoreAllMocks();
});

it('settles the plugins when /api/site cannot be read', async () => {
  vi.spyOn(console, 'warn').mockImplementation(() => undefined);
  vi.spyOn(authApi, 'fetchMe').mockRejectedValue(new Error('offline'));
  vi.spyOn(authApi, 'fetchSite').mockRejectedValue(new Error('offline'));

  await startSession();

  expect(pluginsSettled()).toBe(true);
  expect(plugins()).toEqual([]);
});
